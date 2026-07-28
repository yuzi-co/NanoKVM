package direct

import (
	"encoding/binary"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"

	"NanoKVM-Server/common"
	"NanoKVM-Server/service/stream"
)

type Streamer struct {
	mutex          sync.Mutex
	clients        map[*websocket.Conn]*client
	clientSnapshot atomic.Pointer[[]*client]
	running        int32
}

func newStreamer() *Streamer {
	s := &Streamer{
		clients: make(map[*websocket.Conn]*client),
	}
	s.updateClientSnapshotLocked()

	return s
}

func (s *Streamer) addClient(ws *websocket.Conn) *client {
	c := newClient(ws)
	go c.write()

	s.mutex.Lock()
	s.clients[ws] = c
	s.updateClientSnapshotLocked()
	s.mutex.Unlock()

	if atomic.CompareAndSwapInt32(&s.running, 0, 1) {
		go s.run()
		log.Debug("h264 stream started")
	}

	return c
}

func (s *Streamer) removeClient(ws *websocket.Conn) {
	s.mutex.Lock()
	c, exists := s.clients[ws]
	delete(s.clients, ws)
	count := s.updateClientSnapshotLocked()
	s.mutex.Unlock()

	if exists {
		c.stop()
	}

	log.Debugf("h264 websocket disconnected, remaining clients: %d", count)
}

func (s *Streamer) updateClientSnapshotLocked() int {
	clients := make([]*client, 0, len(s.clients))
	for _, c := range s.clients {
		clients = append(clients, c)
	}
	s.clientSnapshot.Store(&clients)

	return len(clients)
}

func (s *Streamer) getClients() []*client {
	clients := s.clientSnapshot.Load()
	if clients == nil {
		return nil
	}

	return *clients
}

func (s *Streamer) run() {
	defer atomic.StoreInt32(&s.running, 0)

	screen := common.GetScreen()
	common.CheckScreen()
	values := screen.Snapshot()
	fps := values.FPS

	ticker := time.NewTicker(time.Second / time.Duration(fps))
	defer ticker.Stop()

	vision := common.GetKvmVision()
	startTime := time.Now()

	for range ticker.C {
		clients := s.getClients()
		if len(clients) == 0 {
			log.Debug("h264 stream stopped due to no clients")
			return
		}

		values = screen.Snapshot()

		data, result := vision.ReadH264(values.Width, values.Height, values.BitRate)
		stream.UpdateCaptureStatus(stream.CaptureModeDirect, result)
		if result < 0 || len(data) == 0 {
			continue
		}

		isKeyFrame := result == 3
		timestamp := time.Since(startTime).Microseconds()

		s.send(clients, isKeyFrame, timestamp, data)

		if values.FPS != fps && values.FPS != 0 {
			fps = values.FPS
			ticker.Reset(time.Second / time.Duration(fps))
		}

		stream.GetFrameRateCounter().Update()
	}
}

// send hands the frame to every client's writer goroutine. The message is
// built once and shared, so it must not be modified afterwards.
func (s *Streamer) send(clients []*client, isKeyFrame bool, timestamp int64, data []byte) {
	message := make([]byte, 0, 1+8+len(data))

	flag := byte(0)
	if isKeyFrame {
		flag = 1
	}
	message = append(message, flag)

	var tsBytes [8]byte
	binary.LittleEndian.PutUint64(tsBytes[:], uint64(timestamp))
	message = append(message, tsBytes[:]...)

	message = append(message, data...)

	for _, c := range clients {
		c.enqueue(message, isKeyFrame)
	}
}
