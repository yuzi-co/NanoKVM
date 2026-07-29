package webrtc

import (
	"testing"

	"github.com/pion/rtp"
)

const (
	keyFrame   = true
	deltaFrame = false
)

func frame(label string) []*rtp.Packet {
	return []*rtp.Packet{{Payload: []byte(label)}}
}

func takeLabel(t *testing.T, c *Client) string {
	t.Helper()

	packets, ok := c.slot.Take()
	if !ok {
		t.Fatal("slot was closed")
	}

	return string(packets[0].Payload)
}

func newTestClient() *Client {
	c := NewClient(nil, nil)
	c.track, _ = newTestTrack(5)

	return c
}

func TestEnqueueAcceptsFrameWhenClientIsKeepingUp(t *testing.T) {
	c := newTestClient()

	c.enqueue(frame("frame"), deltaFrame)

	if !c.slot.Pending() {
		t.Fatal("the frame should be queued for the writer")
	}
}

// The capture loop must never wait on a viewer: one client on a slow link
// would otherwise hold up the frame for everyone else.
func TestEnqueueDropsFrameWhenPreviousOneIsStillPending(t *testing.T) {
	c := newTestClient()
	c.enqueue(frame("first"), deltaFrame)

	c.enqueue(frame("second"), deltaFrame)

	if c.slot.Dropped() != 1 {
		t.Fatalf("expected the second frame to be dropped, dropped=%d", c.slot.Dropped())
	}

	if got := takeLabel(t, c); got != "first" {
		t.Fatalf("the pending frame should be untouched, got %q", got)
	}
}

func TestEnqueueSkipsDeltaFramesUntilNextKeyFrame(t *testing.T) {
	c := newTestClient()
	c.enqueue(frame("first"), deltaFrame)
	c.enqueue(frame("dropped"), deltaFrame)
	takeLabel(t, c) // the writer catches up

	c.enqueue(frame("delta"), deltaFrame)

	if c.slot.Pending() {
		t.Fatal("a delta frame must not be sent while waiting for a keyframe")
	}

	c.enqueue(frame("key"), keyFrame)

	if !c.slot.Pending() {
		t.Fatal("a keyframe should resume the stream")
	}
	if got := takeLabel(t, c); got != "key" {
		t.Fatalf("expected the keyframe, got %q", got)
	}
}
