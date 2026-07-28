package mjpeg

import (
	"NanoKVM-Server/common"
	"NanoKVM-Server/service/stream"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

type Streamer struct {
	mutex          sync.Mutex
	clients        map[*gin.Context]*client
	clientSnapshot atomic.Pointer[[]*client]
	running        int32
	frameMutex     sync.RWMutex
	latestFrame    LatestFrame
	cacheRefs      int32
}

func NewStreamer() *Streamer {
	s := &Streamer{
		clients: make(map[*gin.Context]*client),
	}
	s.updateClientSnapshotLocked()

	return s
}

func (s *Streamer) AddClient(c *gin.Context) *client {
	client := newClient(c)
	go client.write()

	s.mutex.Lock()
	s.clients[c] = client
	s.updateClientSnapshotLocked()
	s.mutex.Unlock()

	if atomic.CompareAndSwapInt32(&s.running, 0, 1) {
		go s.run()
		log.Debug("mjpeg stream started")
	}

	return client
}

func (s *Streamer) RemoveClient(c *gin.Context) {
	s.mutex.Lock()
	client, exists := s.clients[c]
	delete(s.clients, c)
	count := s.updateClientSnapshotLocked()
	s.mutex.Unlock()

	if exists {
		client.stop()
	}

	log.Debugf("mjpeg connection removed, remaining clients: %d", count)
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

	vision := common.GetKvmVision()

	ticker := time.NewTicker(time.Second / time.Duration(fps))
	defer ticker.Stop()

	for range ticker.C {
		clients := s.getClients()
		if len(clients) == 0 {
			log.Debug("mjpeg stream stopped due to no clients")
			return
		}

		values = screen.Snapshot()

		data, result := vision.ReadMjpeg(values.Width, values.Height, values.Quality)
		stream.UpdateCaptureStatus(stream.CaptureModeMJPEG, result)
		if result < 0 || result == 5 || len(data) == 0 {
			continue
		}

		if s.frameCacheEnabled() {
			s.setLatestFrame(data, values.Width, values.Height)
		}

		// Handing the frame over never blocks: a client that is behind gets
		// the newest frame and the older one is dropped.
		for _, client := range clients {
			client.enqueue(data)
		}

		if values.FPS != fps && values.FPS != 0 {
			fps = values.FPS
			ticker.Reset(time.Second / time.Duration(fps))
		}

		stream.GetFrameRateCounter().Update()
	}
}

// setLatestFrame caches the frame for the screenshot API. The capture loop
// hands over a freshly allocated slice per frame and nobody mutates it, so the
// cache shares it rather than copying a whole JPEG on every tick. getLatestFrame
// still copies, so callers cannot reach back into it.
func (s *Streamer) setLatestFrame(data []byte, width uint16, height uint16) {
	s.frameMutex.Lock()
	defer s.frameMutex.Unlock()

	s.latestFrame = LatestFrame{
		Data:       data,
		Width:      width,
		Height:     height,
		CapturedAt: time.Now(),
	}
}

func (s *Streamer) clearLatestFrame() {
	s.frameMutex.Lock()
	defer s.frameMutex.Unlock()

	s.latestFrame = LatestFrame{}
}

func (s *Streamer) enableLatestFrameCache() {
	atomic.AddInt32(&s.cacheRefs, 1)
}

func (s *Streamer) disableLatestFrameCache() {
	for {
		current := atomic.LoadInt32(&s.cacheRefs)
		if current <= 0 {
			return
		}

		if atomic.CompareAndSwapInt32(&s.cacheRefs, current, current-1) {
			if current == 1 {
				s.clearLatestFrame()
			}
			return
		}
	}
}

func (s *Streamer) frameCacheEnabled() bool {
	return atomic.LoadInt32(&s.cacheRefs) > 0
}

func (s *Streamer) getLatestFrame() (LatestFrame, bool) {
	if !s.frameCacheEnabled() {
		return LatestFrame{}, false
	}

	s.frameMutex.RLock()
	defer s.frameMutex.RUnlock()

	if len(s.latestFrame.Data) == 0 {
		return LatestFrame{}, false
	}

	return LatestFrame{
		Data:       append([]byte(nil), s.latestFrame.Data...),
		Width:      s.latestFrame.Width,
		Height:     s.latestFrame.Height,
		CapturedAt: s.latestFrame.CapturedAt,
	}, true
}
