//go:build linux

package audio

import (
	"os/exec"
	"testing"
	"time"
)

// A source that gives up must end the stream by itself. If it does not, the
// consumer blocks on a channel nothing will close, and the manager goes on
// believing audio is being sent, so it never starts a replacement.
func TestStreamClosesFramesWhenTheSourceGivesUp(t *testing.T) {
	stream := NewStream()
	stream.source.minBackoff = time.Millisecond
	stream.source.maxBackoff = time.Millisecond
	stream.source.newCmd = func() *exec.Cmd {
		return exec.Command("sh", "-c", "exit 1")
	}

	stream.Start()

	done := make(chan struct{})
	go func() {
		for range stream.Frames() { //nolint:revive // draining is the point
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		stream.Stop()
		t.Fatal("the frame channel stayed open after capture gave up")
	}
}

// Stop after the source already gave up must not panic on a second close.
func TestStreamStopIsSafeAfterTheSourceGivesUp(t *testing.T) {
	stream := NewStream()
	stream.source.minBackoff = time.Millisecond
	stream.source.maxBackoff = time.Millisecond
	stream.source.newCmd = func() *exec.Cmd {
		return exec.Command("sh", "-c", "exit 1")
	}

	stream.Start()

	for range stream.Frames() { //nolint:revive // draining is the point
	}

	stream.Stop()
	stream.Stop()
}
