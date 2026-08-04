package audio

import (
	"context"
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
	stopped bool

	closerMutex sync.Mutex
	closer      io.ReadCloser

	ctx    context.Context
	cancel context.CancelFunc
}

func NewSource() *Source {
	ctx, cancel := context.WithCancel(context.Background())
	return &Source{
		newCmd:     newArecord,
		minBackoff: 200 * time.Millisecond,
		maxBackoff: 5 * time.Second,
		ctx:        ctx,
		cancel:     cancel,
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
// child has failed restartLimit times inside restartWindow. It blocks.
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

		if delivered := s.runOnce(chunk, handle); delivered {
			// The child produced audio, so the next failure is a fresh one.
			backoff = s.minBackoff
			windowStart = time.Now()
			restarts = 0
		} else {
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
		case <-s.ctx.Done():
			return
		}

		if backoff *= 2; backoff > s.maxBackoff {
			backoff = s.maxBackoff
		}
	}
}

// runOnce starts one child and reads it to exhaustion. It reports whether the
// child delivered any audio at all.
func (s *Source) runOnce(chunk []byte, handle func([]byte)) bool {
	cmd := s.newCmd()

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Errorf("failed to open the audio capture pipe: %s", err)
		return false
	}

	if err := cmd.Start(); err != nil {
		log.Errorf("failed to start audio capture: %s", err)
		return false
	}

	if !s.setCmd(cmd) {
		// Stop arrived between the check and here.
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = stdout.Close()

		return false
	}

	// Store the closer so Stop can close it to unblock the read.
	s.closerMutex.Lock()
	s.closer = stdout
	s.closerMutex.Unlock()

	defer func() {
		s.closerMutex.Lock()
		s.closer = nil
		s.closerMutex.Unlock()
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
	s.clearCmd()

	return delivered
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
	s.cancel()

	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}

	// Close the pipe to unblock any pending read.
	s.closerMutex.Lock()
	if s.closer != nil {
		_ = s.closer.Close()
	}
	s.closerMutex.Unlock()
}

func (s *Source) isStopped() bool {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	return s.stopped
}

// setCmd records the running child, and reports false if Stop already ran.
func (s *Source) setCmd(cmd *exec.Cmd) bool {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if s.stopped {
		return false
	}

	s.cmd = cmd

	return true
}

func (s *Source) clearCmd() {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	s.cmd = nil
}
