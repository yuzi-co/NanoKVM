package direct

import (
	"time"

	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"

	"NanoKVM-Server/service/stream"
)

// writeTimeout caps how long a single write may take before the client is
// considered gone.
const writeTimeout = 5 * time.Second

// client owns the only goroutine that writes to its websocket. The capture
// loop hands frames over through the slot and never blocks on the socket.
type client struct {
	conn *websocket.Conn
	slot *stream.FrameSlot[[]byte]
	done chan struct{}

	// waitingForKeyFrame is read and written only by the capture goroutine.
	waitingForKeyFrame bool
}

func newClient(conn *websocket.Conn) *client {
	return &client{
		conn: conn,
		slot: stream.NewFrameSlot[[]byte](),
		done: make(chan struct{}),
	}
}

// enqueue offers a frame to the client.
//
// A client that has not drained the previous frame is behind, and an H.264
// stream with a hole in it stays broken until the next keyframe, so the frame
// is dropped and everything after it is skipped until one arrives.
func (c *client) enqueue(message []byte, isKeyFrame bool) {
	if c.waitingForKeyFrame && !isKeyFrame {
		return
	}

	if !c.slot.TryPut(message) {
		c.waitingForKeyFrame = true
		return
	}

	c.waitingForKeyFrame = false
}

// write drains the slot until it is closed or the socket fails.
func (c *client) write() {
	defer close(c.done)

	for {
		message, ok := c.slot.Take()
		if !ok {
			return
		}

		_ = c.conn.SetWriteDeadline(time.Now().Add(writeTimeout))

		if err := c.conn.WriteMessage(websocket.BinaryMessage, message); err != nil {
			log.Debugf("h264 write to %s failed: %s", c.conn.RemoteAddr(), err)

			// Unblock the reader so the handler tears the client down.
			_ = c.conn.Close()

			return
		}
	}
}

// stop releases the writer and waits for it to let go of the socket.
func (c *client) stop() {
	c.slot.Close()
	<-c.done

	if dropped := c.slot.Dropped(); dropped > 0 {
		log.Debugf("h264 client dropped %d frames", dropped)
	}
}
