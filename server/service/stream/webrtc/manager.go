package webrtc

import (
	"NanoKVM-Server/common"
	"NanoKVM-Server/service/stream"
	"NanoKVM-Server/service/vm"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
	log "github.com/sirupsen/logrus"
)

// viewerSource names this stream in the idle capture accounting. A client
// switching between stream types is briefly counted on both.
const viewerSource = "webrtc"

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
		videoSending: 0,
		videoPacketizer: rtp.NewPacketizer(
			rtpMTU,
			videoPayloadType,
			videoSSRC,
			&codecs.H264Payloader{},
			rtp.NewRandomSequencer(),
			clockRate,
		),
	}
	m.updateClientSnapshotLocked()

	return m
}

func (m *WebRTCManager) AddClient(ws *websocket.Conn, client *Client) {
	go client.write()

	m.mutex.Lock()
	m.clients[ws] = client
	count := m.updateClientSnapshotLocked()
	m.mutex.Unlock()

	// Outside the lock: resuming capture is slow, and this lock is on the
	// path that hands frames to every other viewer.
	vm.SetViewerCount(viewerSource, count)

	log.Debugf("added client %s, total clients: %d", ws.RemoteAddr(), count)
}

func (m *WebRTCManager) RemoveClient(ws *websocket.Conn) {
	m.mutex.Lock()
	client, exists := m.clients[ws]
	delete(m.clients, ws)
	count := m.updateClientSnapshotLocked()
	m.mutex.Unlock()

	// Outside the lock: resuming capture is slow, and this lock is on the
	// path that hands frames to every other viewer.
	vm.SetViewerCount(viewerSource, count)

	if exists {
		client.stop()
	}

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
	if atomic.CompareAndSwapInt32(&m.videoSending, 0, 1) {
		go m.sendVideoStream()
		log.Debugf("start sending h264 stream")
	}
}

func (m *WebRTCManager) sendVideoStream() {
	defer atomic.StoreInt32(&m.videoSending, 0)

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
			log.Debugf("stop sending h264 stream")
			return
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
