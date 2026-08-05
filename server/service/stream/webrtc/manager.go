package webrtc

import (
	"NanoKVM-Server/common"
	"NanoKVM-Server/service/stream"
	"NanoKVM-Server/service/vm"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
	log "github.com/sirupsen/logrus"
)

const (
	// rtpMTU keeps a packet inside a normal path MTU once the RTP, UDP and IP
	// headers are added.
	rtpMTU = 1200

	// videoPayloadType and videoSSRC are placeholders: pion rewrites both per
	// binding from what the peer negotiated.
	videoPayloadType = 100
	videoSSRC        = 0x1234ABCD

	// clockRate is the RTP clock for H.264.
	clockRate = 90000
)

func NewWebRTCManager() *WebRTCManager {
	m := &WebRTCManager{
		clients:      make(map[*websocket.Conn]*Client),
		videoSending: false,
		videoPacketizer: rtp.NewPacketizer(
			rtpMTU,
			videoPayloadType,
			videoSSRC,
			&codecs.H264Payloader{},
			rtp.NewRandomSequencer(),
			clockRate,
		),
		audioPacketizer: rtp.NewPacketizer(
			rtpMTU,
			audioPayloadType,
			audioSSRC,
			&codecs.G711Payloader{},
			rtp.NewRandomSequencer(),
			audioClockRate,
		),
	}
	m.updateClientSnapshotLocked()

	return m
}

// storeClient records the client and reports whether it is new to the
// manager's map. The bool is bookkeeping only now: it does not gate the
// writer start, because a reconnect reuses the same *Client after
// RemoveClient deleted its map entry, and the map alone cannot tell that
// apart from a client the manager has never seen. See Client.startWriters.
func (m *WebRTCManager) storeClient(ws *websocket.Conn, client *Client) (int, uint64, bool) {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	_, exists := m.clients[ws]
	m.clients[ws] = client

	count := m.updateClientSnapshotLocked()
	m.viewerVersion++

	return count, m.viewerVersion, !exists
}

func (m *WebRTCManager) AddClient(ws *websocket.Conn, client *Client) {
	count, version, _ := m.storeClient(ws, client)

	// The writer-start guard lives on the Client, not here: ICE reaches
	// Connected and then Completed for one handshake, and can also flap
	// Connected -> Disconnected -> Connected on a blip, and signalling calls
	// AddClient on all of them, sometimes with the same *Client after
	// RemoveClient already closed its slots. Starting the writers twice would
	// put two goroutines on one slot, and the second close of the done
	// channel panics the server.
	client.startWriters()

	vm.UpdateHdmiViewerSnapshot("webrtc", count, version)

	log.Debugf("added client %s, total clients: %d", ws.RemoteAddr(), count)
}

func (m *WebRTCManager) RemoveClient(ws *websocket.Conn) {
	m.mutex.Lock()
	client, exists := m.clients[ws]
	delete(m.clients, ws)
	count := m.updateClientSnapshotLocked()
	m.viewerVersion++
	version := m.viewerVersion
	m.mutex.Unlock()
	vm.UpdateHdmiViewerSnapshot("webrtc", count, version)

	if exists {
		client.stop()
	}

	m.stopAudioStreamIfIdle()

	log.Debugf("removed client %s, total clients: %d", ws.RemoteAddr(), count)
}

func (m *WebRTCManager) GetClientCount() int {
	return len(m.getClients())
}

func (m *WebRTCManager) updateClientSnapshotLocked() int {
	clients := make([]*Client, 0, len(m.clients))
	for _, client := range m.clients {
		clients = append(clients, client)
	}
	m.clientSnapshot.Store(&clients)

	return len(clients)
}

func (m *WebRTCManager) getClients() []*Client {
	clients := m.clientSnapshot.Load()
	if clients == nil {
		return nil
	}

	return *clients
}

func (m *WebRTCManager) StartVideoStream() {
	m.mutex.Lock()
	if m.videoSending || len(m.clients) == 0 {
		m.mutex.Unlock()
		return
	}
	m.videoSending = true
	m.mutex.Unlock()

	go m.sendVideoStream()
	log.Debugf("start sending h264 stream")
}

func (m *WebRTCManager) stopVideoStreamIfIdle() bool {
	m.mutex.Lock()
	defer m.mutex.Unlock()

	if len(m.clients) > 0 {
		return false
	}

	m.videoSending = false
	return true
}

func (m *WebRTCManager) sendVideoStream() {
	screen := common.GetScreen()
	common.CheckScreen()
	values := screen.Snapshot()
	fps := values.FPS
	duration := time.Second / time.Duration(fps)

	vision := common.GetKvmVision()

	ticker := time.NewTicker(duration)
	defer ticker.Stop()

	for range ticker.C {
		clients := m.getClients()
		if len(clients) == 0 {
			if m.stopVideoStreamIfIdle() {
				log.Debugf("stop sending h264 stream")
				return
			}

			continue
		}

		values = screen.Snapshot()

		data, result := vision.ReadH264(values.Width, values.Height, values.BitRate)
		stream.UpdateCaptureStatus(stream.CaptureModeH264, result)
		if result < 0 || len(data) == 0 {
			continue
		}

		// Packetized once for everyone. Cutting the same frame up again for
		// each viewer copies the whole payload per client, which is real work
		// on a board with one core and no memory to spare.
		samples := uint32(duration.Seconds() * clockRate)
		packets := m.videoPacketizer.Packetize(data, samples)

		isKeyFrame := result == 3

		// Handing the frame over never blocks: a client that is behind drops
		// it and waits for the next keyframe.
		for _, client := range clients {
			client.enqueue(packets, isKeyFrame)
		}

		if values.FPS != fps && values.FPS != 0 {
			fps = values.FPS
			duration = time.Second / time.Duration(fps)
			ticker.Reset(duration)
		}

		stream.GetFrameRateCounter().Update()
	}
}
