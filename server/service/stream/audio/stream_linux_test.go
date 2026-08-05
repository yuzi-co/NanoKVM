//go:build linux

package audio

import (
	"io"
	"os/exec"
	"testing"
	"time"

	log "github.com/sirupsen/logrus"
)

// deliveringStream returns a stream whose child produces capture chunks for
// as long as it is left alone, the way arecord does while the host plays.
//
// yes writes a byte and a newline forever, so the pipe never runs dry and the
// reader in runOnce is always inside a read or about to enter one. That is the
// state Stop has to interrupt in production.
func deliveringStream() *Stream {
	stream := NewStream()
	stream.source.minBackoff = time.Millisecond
	stream.source.maxBackoff = time.Millisecond
	stream.source.newCmd = func() *exec.Cmd {
		return exec.Command("yes")
	}

	return stream
}

// Stopping a source that is delivering is what production does every time the
// last listener leaves. Nothing else exercises Stop against a live read: the
// other tests stop a source that has already given up, and a child that has
// exited cannot show that killing it is what ends the read.
func TestStopEndsAStreamThatIsDelivering(t *testing.T) {
	stream := deliveringStream()
	stream.Start()

	// Wait for real frames, so the stop below lands on a running child rather
	// than on one that has not started yet.
	for range 2 {
		select {
		case frame, ok := <-stream.Frames():
			if !ok {
				t.Fatal("the frame channel closed before capture delivered anything")
			}
			if len(frame) != FrameSamples {
				t.Fatalf("frame is %d bytes, want %d", len(frame), FrameSamples)
			}
		case <-time.After(5 * time.Second):
			stream.Stop()
			t.Fatal("capture delivered no frame within 5s")
		}
	}

	stopped := make(chan struct{})
	go func() {
		stream.Stop()
		close(stopped)
	}()

	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return within 5s against a delivering source")
	}

	// Stop closes the channel, and it may hold frames the consumer never took.
	// Draining it must end rather than block, or the send loop never returns.
	drained := make(chan struct{})
	go func() {
		for range stream.Frames() { //nolint:revive // draining is the point
		}
		close(drained)
	}()

	select {
	case <-drained:
	case <-time.After(5 * time.Second):
		t.Fatal("the frame channel stayed open after Stop")
	}
}

// Stop bounds its wait, so a second Stop against an already-stopped stream
// must still return at once and must not panic on the closed channel.
func TestStopIsSafeToRepeatOnADeliveringStream(t *testing.T) {
	stream := deliveringStream()
	stream.Start()

	select {
	case <-stream.Frames():
	case <-time.After(5 * time.Second):
		stream.Stop()
		t.Fatal("capture delivered no frame within 5s")
	}

	stream.Stop()
	stream.Stop()
}

// A source that gives up must end the stream by itself. If it does not, the
// consumer blocks on a channel nothing will close, and the manager goes on
// believing audio is being sent, so it never starts a replacement.
func TestStreamClosesFramesWhenTheSourceGivesUp(t *testing.T) {
	// Suppress the expected error log spam from repeated child failures.
	original := log.StandardLogger().Out
	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(original) })

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
	// Suppress the expected error log spam from repeated child failures.
	original := log.StandardLogger().Out
	log.SetOutput(io.Discard)
	t.Cleanup(func() { log.SetOutput(original) })

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
