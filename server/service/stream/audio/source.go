package audio

import (
	"io"
	"os/exec"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

const (
	// CaptureDevice names the card rather than its index. The index depends on
	// probe order and moves when the gadget is rebuilt; the name does not.
	CaptureDevice = "hw:UAC1Gadget,0"

	// ChunkBytes is 20 ms of 48 kHz stereo S16_LE: 960 frames of 4 bytes.
	ChunkBytes = 960 * 4

	// restartLimit and restartWindow decide when a failing child stops being
	// worth retrying.
	restartLimit  = 5
	restartWindow = time.Minute

	// minRunDuration is the minimum time a child must run to reset the failure
	// budget. A child that emits a single chunk and exits is not a healthy
	// restart; it is a crash loop.
	minRunDuration = 5 * time.Second
)

// Source owns the arecord child process and hands its output out in chunks.
type Source struct {
	// newCmd builds the child. It is a field so that tests can supply a
	// command which does not need ALSA.
	newCmd func() *exec.Cmd

	minBackoff time.Duration
	maxBackoff time.Duration

	mutex   sync.Mutex
	cmd     *exec.Cmd
	stdout  io.ReadCloser
	stopped bool
	done    chan struct{}
}

func NewSource() *Source {
	return &Source{
		newCmd:     newArecord,
		minBackoff: 200 * time.Millisecond,
		maxBackoff: 5 * time.Second,
		done:       make(chan struct{}),
	}
}

// newArecord reads the gadget and writes raw samples to stdout.
//
// The period is pinned to 960 frames, which is 20 ms and the same size as a
// chunk. Left to ALSA, the period comes from the driver's default and decides
// how much delay sits in front of the encoder. The gadget advertises
// PERIOD_SIZE [32 1024], so 960 is inside its range.
func newArecord() *exec.Cmd {
	return exec.Command("arecord",
		"-D", CaptureDevice,
		"-f", "S16_LE",
		"-r", "48000",
		"-c", "2",
		"-t", "raw",
		"--period-size=960",
	)
}

// Run calls handle with each full chunk until Stop is called, or until the
// child has failed restartLimit times inside restartWindow. The slice handed
// to handle is reused on the next read and is only valid until handle returns.
// Run blocks.
func (s *Source) Run(handle func([]byte)) {
	chunk := make([]byte, ChunkBytes)
	backoff := s.minBackoff

	var restarts int
	windowStart := time.Now()

	for {
		if s.isStopped() {
			return
		}

		if time.Since(windowStart) > restartWindow {
			windowStart = time.Now()
			restarts = 0
		}

		delivered, uptime := s.runOnce(chunk, handle)

		if delivered && uptime > minRunDuration {
			// The child produced audio and ran long enough, so the next failure
			// is a fresh one.
			backoff = s.minBackoff
			windowStart = time.Now()
			restarts = 0
		} else {
			if !delivered || uptime <= minRunDuration {
				log.Warnf("audio capture exited (uptime=%v, delivered=%v), restart %d/%d",
					uptime, delivered, restarts+1, restartLimit)
			}

			restarts++
			if restarts >= restartLimit {
				log.Errorf("audio capture failed %d times, giving up", restarts)
				return
			}
		}

		if s.isStopped() {
			return
		}

		select {
		case <-time.After(backoff):
		case <-s.done:
			return
		}

		if backoff *= 2; backoff > s.maxBackoff {
			backoff = s.maxBackoff
		}
	}
}

// runOnce starts one child and reads it to exhaustion. It reports whether the
// child delivered any audio and how long it ran.
func (s *Source) runOnce(chunk []byte, handle func([]byte)) (bool, time.Duration) {
	startTime := time.Now()

	cmd := s.newCmd()

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Errorf("failed to open the audio capture pipe: %s", err)
		return false, time.Since(startTime)
	}

	if err := cmd.Start(); err != nil {
		log.Errorf("failed to start audio capture: %s", err)
		return false, time.Since(startTime)
	}

	// Store cmd and stdout together under one lock so Stop can close both
	// atomically, avoiding the window where one is recorded and the other is not.
	s.mutex.Lock()
	if s.stopped {
		s.mutex.Unlock()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = stdout.Close()
		return false, time.Since(startTime)
	}
	s.cmd = cmd
	s.stdout = stdout
	s.mutex.Unlock()

	defer func() {
		s.mutex.Lock()
		s.cmd = nil
		s.stdout = nil
		s.mutex.Unlock()
	}()

	var delivered bool

	for {
		if _, err := io.ReadFull(stdout, chunk); err != nil {
			break
		}

		delivered = true
		handle(chunk)
	}

	_ = cmd.Wait()

	return delivered, time.Since(startTime)
}

// Stop kills the child and stops the loop. It is safe to call more than once,
// and it is the only thing that unblocks a read while the host plays nothing.
func (s *Source) Stop() {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.stopped {
		return
	}

	s.stopped = true

	// Close the done channel to wake the select in Run, and kill and close the
	// pipe under the same lock so there is no window where one is recorded and
	// the other is not.
	close(s.done)

	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}

	if s.stdout != nil {
		_ = s.stdout.Close()
	}
}

func (s *Source) isStopped() bool {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	return s.stopped
}
