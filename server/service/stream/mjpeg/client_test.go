package mjpeg

import "testing"

func TestEnqueueQueuesFrameForTheWriter(t *testing.T) {
	c := newClient(nil)

	c.enqueue([]byte("frame"))

	frame, ok := c.slot.Take()
	if !ok || string(frame) != "frame" {
		t.Fatalf("expected the frame to be queued, got %q (ok=%v)", frame, ok)
	}
}

func TestEnqueueKeepsTheNewestFrameForASlowClient(t *testing.T) {
	// Every MJPEG frame stands alone, so a client that fell behind should get
	// the current picture rather than work through a backlog.
	c := newClient(nil)

	c.enqueue([]byte("old"))
	c.enqueue([]byte("new"))

	frame, _ := c.slot.Take()
	if string(frame) != "new" {
		t.Fatalf("expected the newest frame, got %q", frame)
	}

	if c.slot.Dropped() != 1 {
		t.Fatalf("expected 1 dropped frame, got %d", c.slot.Dropped())
	}
}

func TestFailedIsClosedWhenTheWriterGivesUp(t *testing.T) {
	c := newClient(nil)

	c.fail()

	select {
	case <-c.failed:
	default:
		t.Fatal("the handler must be woken when the writer gives up")
	}
}

func TestFailIsIdempotent(t *testing.T) {
	c := newClient(nil)

	c.fail()
	c.fail()
}
