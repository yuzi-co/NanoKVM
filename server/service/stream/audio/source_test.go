//go:build linux

package audio

import (
	"os/exec"
	"sync"
	"testing"
	"time"
)

// collector gathers what Run hands out, from whichever goroutine calls it.
type collector struct {
	mutex  sync.Mutex
	chunks [][]byte
}

func (c *collector) handle(chunk []byte) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.chunks = append(c.chunks, append([]byte(nil), chunk...))
}

func (c *collector) count() int {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	return len(c.chunks)
}

func TestRunDeliversFullChunks(t *testing.T) {
	source := NewSource()
	// Two chunks of zeros, then exit. head -c reads from /dev/zero.
	source.newCmd = func() *exec.Cmd {
		return exec.Command("sh", "-c", "head -c 7680 /dev/zero")
	}

	got := &collector{}

	done := make(chan struct{})
	go func() {
		source.Run(got.handle)
		close(done)
	}()

	time.Sleep(500 * time.Millisecond)
	source.Stop()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after Stop")
	}

	if got.count() < 2 {
		t.Fatalf("got %d chunks, want at least 2", got.count())
	}

	if n := len(got.chunks[0]); n != ChunkBytes {
		t.Errorf("first chunk is %d bytes, want %d", n, ChunkBytes)
	}
}

func TestRunReturnsAfterStop(t *testing.T) {
	source := NewSource()
	// A child that never writes and never exits, which is what arecord does
	// while the host plays nothing.
	source.newCmd = func() *exec.Cmd {
		return exec.Command("sh", "-c", "sleep 60")
	}

	done := make(chan struct{})
	go func() {
		source.Run(func([]byte) {})
		close(done)
	}()

	time.Sleep(200 * time.Millisecond)
	source.Stop()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after Stop")
	}
}

func TestRunGivesUpAfterRepeatedFailures(t *testing.T) {
	source := NewSource()
	source.minBackoff = time.Millisecond
	source.maxBackoff = time.Millisecond

	var starts int
	var mutex sync.Mutex

	source.newCmd = func() *exec.Cmd {
		mutex.Lock()
		starts++
		mutex.Unlock()

		return exec.Command("sh", "-c", "exit 1")
	}

	done := make(chan struct{})
	go func() {
		source.Run(func([]byte) {})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		source.Stop()
		t.Fatal("Run kept restarting a child that always fails")
	}

	mutex.Lock()
	defer mutex.Unlock()

	if starts < restartLimit || starts > restartLimit+1 {
		t.Errorf("started the child %d times, want %d to %d", starts, restartLimit, restartLimit+1)
	}
}

func TestStopBeforeRun(t *testing.T) {
	source := NewSource()
	// This child would run indefinitely, but Stop is called before Run.
	source.newCmd = func() *exec.Cmd {
		return exec.Command("sh", "-c", "sleep 60")
	}

	source.Stop()

	done := make(chan struct{})
	go func() {
		source.Run(func([]byte) {})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return immediately when Stop was called before Run")
	}
}

func TestStopCalledTwice(t *testing.T) {
	source := NewSource()
	source.newCmd = func() *exec.Cmd {
		return exec.Command("sh", "-c", "sleep 60")
	}

	done := make(chan struct{})
	go func() {
		source.Run(func([]byte) {})
		close(done)
	}()

	time.Sleep(100 * time.Millisecond)
	source.Stop()
	source.Stop() // Should be safe to call again

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after calling Stop twice")
	}
}

func TestMinRunDurationReset(t *testing.T) {
	source := NewSource()
	source.minBackoff = time.Millisecond
	source.maxBackoff = time.Millisecond

	var starts int
	var mutex sync.Mutex

	source.newCmd = func() *exec.Cmd {
		mutex.Lock()
		starts++
		mutex.Unlock()

		// Emit one chunk quickly, then exit. This is shorter than minRunDuration.
		return exec.Command("sh", "-c", "head -c 3840 /dev/zero")
	}

	done := make(chan struct{})
	go func() {
		source.Run(func([]byte) {})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		source.Stop()
		t.Fatal("Run kept restarting a child that ran too briefly")
	}

	mutex.Lock()
	defer mutex.Unlock()

	if starts < restartLimit || starts > restartLimit+1 {
		t.Errorf("started the child %d times, want %d to %d", starts, restartLimit, restartLimit+1)
	}
}
