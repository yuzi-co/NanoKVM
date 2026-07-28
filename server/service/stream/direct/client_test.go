package direct

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

const (
	keyFrame   = true
	deltaFrame = false
)

// payload builds a frame carrying s, with a header nothing in these tests reads.
func payload(s string) h264Frame {
	return newH264Frame(deltaFrame, 0, []byte(s))
}

func takePayload(t *testing.T, c *client) (string, bool) {
	t.Helper()

	frame, ok := c.slot.Take()

	return string(frame.data), ok
}

func TestEnqueueAcceptsFrameWhenClientIsKeepingUp(t *testing.T) {
	c := newClient(nil)

	c.enqueue(payload("frame"), deltaFrame)

	if !c.slot.Pending() {
		t.Fatal("the frame should be queued for the writer")
	}
}

func TestEnqueueDropsFrameWhenPreviousOneIsStillPending(t *testing.T) {
	c := newClient(nil)
	c.enqueue(payload("first"), deltaFrame)

	c.enqueue(payload("second"), deltaFrame)

	if c.slot.Dropped() != 1 {
		t.Fatalf("expected the second frame to be dropped, dropped=%d", c.slot.Dropped())
	}

	got, _ := takePayload(t, c)
	if got != "first" {
		t.Fatalf("the pending frame should be untouched, got %q", got)
	}
}

func TestEnqueueSkipsDeltaFramesUntilNextKeyFrame(t *testing.T) {
	// A gap in an H.264 stream cannot be repaired by the next delta frame, so
	// once a client falls behind it gets nothing until a keyframe arrives.
	c := newClient(nil)
	c.enqueue(payload("first"), deltaFrame)
	c.enqueue(payload("dropped"), deltaFrame)
	c.slot.Take() // the writer catches up

	c.enqueue(payload("delta"), deltaFrame)

	if c.slot.Pending() {
		t.Fatal("a delta frame must not be sent while waiting for a keyframe")
	}
}

func TestEnqueueResumesOnKeyFrame(t *testing.T) {
	c := newClient(nil)
	c.enqueue(payload("first"), deltaFrame)
	c.enqueue(payload("dropped"), deltaFrame)
	c.slot.Take()

	c.enqueue(payload("key"), keyFrame)

	got, ok := takePayload(t, c)
	if !ok || got != "key" {
		t.Fatalf("a keyframe should restart the stream, got %q (ok=%v)", got, ok)
	}

	c.enqueue(payload("delta"), deltaFrame)
	if !c.slot.Pending() {
		t.Fatal("delta frames should flow again once the stream resumed")
	}
}

func TestEnqueueStaysWaitingWhenKeyFrameArrivesWhileStillBehind(t *testing.T) {
	c := newClient(nil)
	c.enqueue(payload("first"), deltaFrame)
	c.enqueue(payload("dropped"), deltaFrame)

	// The writer has not caught up, so even a keyframe cannot be queued.
	c.enqueue(payload("key"), keyFrame)
	c.slot.Take()

	c.enqueue(payload("delta"), deltaFrame)
	if c.slot.Pending() {
		t.Fatal("the client should still be waiting for a keyframe")
	}
}

func TestFrameHeaderCarriesTheKeyFrameFlagAndTimestamp(t *testing.T) {
	frame := newH264Frame(keyFrame, 0x0102030405060708, []byte("payload"))

	want := []byte{1, 0x08, 0x07, 0x06, 0x05, 0x04, 0x03, 0x02, 0x01}
	if !bytes.Equal(frame.header[:], want) {
		t.Fatalf("expected header %v, got %v", want, frame.header)
	}

	delta := newH264Frame(deltaFrame, 0, nil)
	if delta.header[0] != 0 {
		t.Fatalf("expected a delta frame to be flagged 0, got %d", delta.header[0])
	}
}

func TestBuildingAFrameDoesNotCopyThePayload(t *testing.T) {
	// Joining the header and the payload into one buffer means allocating and
	// memcpying a whole encoded frame, 30 times a second, on a 1GHz core.
	data := make([]byte, 128*1024)

	allocs := testing.AllocsPerRun(50, func() {
		frameSink = newH264Frame(keyFrame, 1, data)
	})

	if &frameSink.data[0] != &data[0] {
		t.Fatal("expected the payload to be referenced, not copied")
	}

	if allocs != 0 {
		t.Fatalf("expected building a frame to allocate nothing, got %v allocations", allocs)
	}
}

var frameSink h264Frame

// websocketPair returns a connected server and client websocket.
func websocketPair(t *testing.T) (*websocket.Conn, *websocket.Conn) {
	t.Helper()

	upgrader := websocket.Upgrader{}
	conns := make(chan *websocket.Conn, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("failed to upgrade: %s", err)
			return
		}
		conns <- conn
	}))
	t.Cleanup(server.Close)

	client, _, err := websocket.DefaultDialer.Dial(strings.Replace(server.URL, "http", "ws", 1), nil)
	if err != nil {
		t.Fatalf("failed to dial: %s", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	select {
	case conn := <-conns:
		t.Cleanup(func() { _ = conn.Close() })
		return conn, client
	case <-time.After(5 * time.Second):
		t.Fatal("the server never accepted the connection")
		return nil, nil
	}
}

func TestWriterSendsTheHeaderAndPayloadAsOneMessage(t *testing.T) {
	server, client := websocketPair(t)

	c := newClient(server)
	go c.write()

	c.enqueue(newH264Frame(keyFrame, 0x0102030405060708, []byte{0xAA, 0xBB, 0xCC}), keyFrame)

	_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))
	messageType, data, err := client.ReadMessage()
	if err != nil {
		t.Fatalf("failed to read the message: %s", err)
	}

	if messageType != websocket.BinaryMessage {
		t.Fatalf("expected a binary message, got %d", messageType)
	}

	want := []byte{1, 0x08, 0x07, 0x06, 0x05, 0x04, 0x03, 0x02, 0x01, 0xAA, 0xBB, 0xCC}
	if !bytes.Equal(data, want) {
		t.Fatalf("expected %v on the wire, got %v", want, data)
	}

	c.stop()
}
