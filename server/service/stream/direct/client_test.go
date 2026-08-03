package direct

import (
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// legacyPayload is what the previous newOutboundFrame built: a fresh buffer
// with the header spliced in front of the encoder's frame. The wire format is
// not changing, so it is the reference the new path has to match byte for byte.
func legacyPayload(isKeyFrame bool, timestamp int64, data []byte) []byte {
	payload := make([]byte, 9+len(data))
	if isKeyFrame {
		payload[0] = 1
	}
	binary.LittleEndian.PutUint64(payload[1:9], uint64(timestamp))
	copy(payload[9:], data)

	return payload
}

func TestFrameHeaderMatchesTheFormatOnTheWire(t *testing.T) {
	for _, test := range []struct {
		name      string
		key       bool
		timestamp int64
	}{
		{"keyframe", true, 1234567},
		{"delta frame", false, 1234567},
		{"zero timestamp", false, 0},
		{"large timestamp", true, 1 << 40},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := []byte{0xde, 0xad, 0xbe, 0xef}
			frame := newOutboundFrame(test.key, test.timestamp, data)

			header := frame.header()
			got := append(header[:], frame.data...)

			if want := legacyPayload(test.key, test.timestamp, data); string(got) != string(want) {
				t.Fatalf("wire bytes are %v, want %v", got, want)
			}
		})
	}
}

func TestNewOutboundFrameDoesNotCopyTheEncoderBuffer(t *testing.T) {
	// The whole point: the encoder's frame is referenced, not duplicated. A
	// copy here is a second full-frame allocation for every frame captured.
	data := make([]byte, 64*1024)
	data[0] = 0xaa

	frame := newOutboundFrame(false, 1, data)

	if &frame.data[0] != &data[0] {
		t.Fatal("the frame should reference the encoder's buffer, not a copy of it")
	}

	allocs := testing.AllocsPerRun(100, func() {
		f := newOutboundFrame(false, 1, data)
		_ = f.header()
	})
	// One allocation for the outboundFrame itself. The old code added a second
	// one the size of the frame.
	if allocs > 1 {
		t.Fatalf("newOutboundFrame plus header allocates %.0f times, want at most 1", allocs)
	}
}

func TestFrameSizeCountsTheHeaderSoTheQueueBudgetIsUnchanged(t *testing.T) {
	// queuedBytes is compared against maxBytes, which is a wire budget. Dropping
	// the header from the count would silently raise the queue's real ceiling.
	frame := newOutboundFrame(true, 7, make([]byte, 1000))

	if got, want := frame.size(), len(legacyPayload(true, 7, make([]byte, 1000))); got != want {
		t.Fatalf("size is %d, want %d", got, want)
	}
}

func TestQueueAccountingReturnsToZeroAfterEveryFrameIsPopped(t *testing.T) {
	q := newFrameQueue(8, 1024*1024)

	q.offer(newOutboundFrame(true, 1, make([]byte, 100)))
	q.offer(newOutboundFrame(false, 2, make([]byte, 200)))

	if q.queuedBytes != 2*frameHeaderSize+300 {
		t.Fatalf("queuedBytes is %d, want %d", q.queuedBytes, 2*frameHeaderSize+300)
	}

	for q.popForWrite() != nil {
	}

	if q.queuedBytes != 0 {
		t.Fatalf("queuedBytes is %d after draining, want 0", q.queuedBytes)
	}
}

func TestQueueRefusesAFrameThatWouldExceedTheByteBudget(t *testing.T) {
	// The budget must be enforced on the wire size, header included.
	q := newFrameQueue(8, 128)

	if !q.offer(newOutboundFrame(true, 1, make([]byte, 100))) {
		t.Fatal("the first keyframe should be accepted")
	}
	// 109 queued; another 100-byte frame is 109 more and does not fit in 128.
	if q.offer(newOutboundFrame(false, 2, make([]byte, 100))) {
		t.Fatal("a frame past the byte budget should be refused")
	}
}

// writeFrame has to produce one binary message holding the header followed by
// the frame, which is what the browser decoder reads.
func TestWriteFrameSendsOneMessageWithTheHeaderInFront(t *testing.T) {
	data := []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	frame := newOutboundFrame(true, 42, data)

	received := make(chan []byte, 1)
	upgrader := websocket.Upgrader{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()

		if err := writeFrame(conn, frame); err != nil {
			t.Errorf("failed to write frame: %s", err)
			return
		}
		<-received
	}))
	defer server.Close()

	client, _, err := websocket.DefaultDialer.Dial("ws"+server.URL[len("http"):], nil)
	if err != nil {
		t.Fatalf("failed to dial: %s", err)
	}
	defer func() { _ = client.Close() }()

	_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))
	messageType, payload, err := client.ReadMessage()
	if err != nil {
		t.Fatalf("failed to read: %s", err)
	}
	received <- payload

	if messageType != websocket.BinaryMessage {
		t.Fatalf("message type is %d, want binary", messageType)
	}
	if want := legacyPayload(true, 42, data); string(payload) != string(want) {
		t.Fatalf("received %v, want %v", payload, want)
	}
}
