// Package audio reads what the managed host plays into the UAC1 USB gadget and
// hands it out as 20 ms frames of G.711 mu-law.
package audio

import (
	"bytes"
	"os"
	"os/exec"
	"sync"

	log "github.com/sirupsen/logrus"
)

// FrameSamples is 20 ms at 8 kHz, and one mu-law byte per sample makes a frame
// 160 bytes.
const FrameSamples = OutputRate / 50

// cardName is the id the UAC1 gadget registers under.
const cardName = "UAC1Gadget"

// cardsPath is a variable so tests can point it at a fixture.
var cardsPath = "/proc/asound/cards"

var (
	arecordOnce    sync.Once
	arecordPresent bool
)

// Available reports whether audio can be captured right now.
//
// The card appears and disappears while the process runs, because the settings
// page rebuilds the USB gadget, so this must not be cached.
func Available() bool {
	arecordOnce.Do(func() {
		if _, err := exec.LookPath("arecord"); err != nil {
			log.Warnf("audio is off: arecord is not installed: %s", err)
			return
		}

		arecordPresent = true
	})

	if !arecordPresent {
		return false
	}

	cards, err := os.ReadFile(cardsPath)
	if err != nil {
		return false
	}

	return bytes.Contains(cards, []byte(cardName))
}

// Stream turns the capture device into mu-law frames.
type Stream struct {
	source    *Source
	decimator *Decimator
	frames    chan []byte

	// samples and frame are scratch buffers. Only the source goroutine
	// touches them.
	samples []int16
	frame   []byte

	mutex     sync.Mutex
	started   bool
	closeOnce sync.Once
	done      chan struct{}
}

func NewStream() *Stream {
	return &Stream{
		source:    NewSource(),
		decimator: NewDecimator(),
		// Four frames of slack. A consumer further behind than 80 ms is not
		// going to catch up, and buffering only adds delay.
		frames:  make(chan []byte, 4),
		samples: make([]int16, 0, FrameSamples),
		frame:   make([]byte, 0, FrameSamples),
		done:    make(chan struct{}),
	}
}

// Start begins capture. Frames arrive on Frames until Stop.
func (s *Stream) Start() {
	s.mutex.Lock()
	if s.started {
		s.mutex.Unlock()
		return
	}
	s.started = true
	s.mutex.Unlock()

	go func() {
		defer close(s.done)

		s.source.Run(s.consume)

		// Run returns for two reasons: Stop killed the child, or the child
		// failed too often and the source gave up. Closing here covers the
		// second one. Without it, a stream that gave up leaves its consumer
		// blocked on a channel that never closes, and the manager still
		// believes audio is being sent, so it never starts a new one.
		s.closeFrames()
	}()
}

// closeFrames ends the channel exactly once, whichever path gets there first.
func (s *Stream) closeFrames() {
	s.closeOnce.Do(func() {
		close(s.frames)
	})
}

// Stop kills the child and closes Frames.
//
// Killing the child is what ends the read. While the host plays nothing,
// arecord blocks, so nothing in the read path notices that the last viewer has
// gone.
//
// The wait on done is what makes closing frames safe: it means the source
// goroutine, and therefore consume, has finished. A stream that was never
// started has no such goroutine to wait for.
func (s *Stream) Stop() {
	s.source.Stop()

	s.mutex.Lock()
	started := s.started
	s.mutex.Unlock()

	if started {
		<-s.done
	}

	s.closeFrames()
}

func (s *Stream) Frames() <-chan []byte {
	return s.frames
}

// consume converts one capture chunk and offers the frame. It never blocks: a
// consumer that is behind loses 20 ms rather than stalling capture.
func (s *Stream) consume(chunk []byte) {
	s.samples = s.decimator.Process(chunk, s.samples[:0])
	s.frame = EncodeULaw(s.samples, s.frame[:0])

	// The buffer is reused, so the channel gets a copy.
	frame := make([]byte, len(s.frame))
	copy(frame, s.frame)

	select {
	case s.frames <- frame:
	default:
	}
}
