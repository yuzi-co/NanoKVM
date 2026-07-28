package stream

import (
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"
)

var (
	counter     *FrameRateCounter
	counterOnce sync.Once
)

const fpsFile = "/kvmapp/kvm/now_fps"

type FrameRateCounter struct {
	frameCount atomic.Int32
	fps        atomic.Int32

	// published is the last value successfully written to disk. Only the
	// publishing goroutine touches these two.
	published    int32
	hasPublished bool
}

// publishFPS writes fps to path, but only when it differs from the last value
// that made it to disk. The counter ticks forever, so rewriting an unchanged
// value just burns write cycles on the card the device boots from. Reports
// whether anything was written.
func (f *FrameRateCounter) publishFPS(path string, fps int32) bool {
	if f.hasPublished && f.published == fps {
		return false
	}

	data := strconv.FormatInt(int64(fps), 10)
	if err := os.WriteFile(path, []byte(data), 0o666); err != nil {
		log.Errorf("failed to write fps: %s", err)
		return false
	}

	f.published = fps
	f.hasPublished = true

	return true
}

func GetFrameRateCounter() *FrameRateCounter {
	counterOnce.Do(func() {
		counter = &FrameRateCounter{}

		go func() {
			ticker := time.NewTicker(3 * time.Second)
			defer ticker.Stop()

			for range ticker.C {
				currentCount := counter.frameCount.Swap(0)
				counter.fps.Store(currentCount / 3)

				counter.publishFPS(fpsFile, counter.fps.Load())
			}
		}()
	})

	return counter
}

func (f *FrameRateCounter) Update() {
	f.frameCount.Add(1)
}

func (f *FrameRateCounter) GetFPS() int32 {
	return f.fps.Load()
}
