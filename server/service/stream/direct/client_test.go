package direct

import "testing"

const (
	keyFrame    = true
	deltaFrame  = false
	someMessage = "frame"
)

func TestEnqueueAcceptsFrameWhenClientIsKeepingUp(t *testing.T) {
	c := newClient(nil)

	c.enqueue([]byte(someMessage), deltaFrame)

	if !c.slot.Pending() {
		t.Fatal("the frame should be queued for the writer")
	}
}

func TestEnqueueDropsFrameWhenPreviousOneIsStillPending(t *testing.T) {
	c := newClient(nil)
	c.enqueue([]byte("first"), deltaFrame)

	c.enqueue([]byte("second"), deltaFrame)

	if c.slot.Dropped() != 1 {
		t.Fatalf("expected the second frame to be dropped, dropped=%d", c.slot.Dropped())
	}

	frame, _ := c.slot.Take()
	if string(frame) != "first" {
		t.Fatalf("the pending frame should be untouched, got %q", frame)
	}
}

func TestEnqueueSkipsDeltaFramesUntilNextKeyFrame(t *testing.T) {
	// A gap in an H.264 stream cannot be repaired by the next delta frame, so
	// once a client falls behind it gets nothing until a keyframe arrives.
	c := newClient(nil)
	c.enqueue([]byte("first"), deltaFrame)
	c.enqueue([]byte("dropped"), deltaFrame)
	c.slot.Take() // the writer catches up

	c.enqueue([]byte("delta"), deltaFrame)

	if c.slot.Pending() {
		t.Fatal("a delta frame must not be sent while waiting for a keyframe")
	}
}

func TestEnqueueResumesOnKeyFrame(t *testing.T) {
	c := newClient(nil)
	c.enqueue([]byte("first"), deltaFrame)
	c.enqueue([]byte("dropped"), deltaFrame)
	c.slot.Take()

	c.enqueue([]byte("key"), keyFrame)

	frame, ok := c.slot.Take()
	if !ok || string(frame) != "key" {
		t.Fatalf("a keyframe should restart the stream, got %q (ok=%v)", frame, ok)
	}

	c.enqueue([]byte("delta"), deltaFrame)
	if !c.slot.Pending() {
		t.Fatal("delta frames should flow again once the stream resumed")
	}
}

func TestEnqueueStaysWaitingWhenKeyFrameArrivesWhileStillBehind(t *testing.T) {
	c := newClient(nil)
	c.enqueue([]byte("first"), deltaFrame)
	c.enqueue([]byte("dropped"), deltaFrame)

	// The writer has not caught up, so even a keyframe cannot be queued.
	c.enqueue([]byte("key"), keyFrame)
	c.slot.Take()

	c.enqueue([]byte("delta"), deltaFrame)
	if c.slot.Pending() {
		t.Fatal("the client should still be waiting for a keyframe")
	}
}
