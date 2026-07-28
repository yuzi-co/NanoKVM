package direct

import (
	"encoding/binary"
	"time"

	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"

	"NanoKVM-Server/service/stream"
)

// writeTimeout caps how long a single write may take before the client is
// considered gone.
const writeTimeout = 5 * time.Second

// headerSize is the keyframe flag plus the little-endian microsecond timestamp
// that precede every payload on the wire.
const headerSize = 1 + 8

// h264Frame keeps the header and the encoded payload apart. Joining them would
// mean allocating and copying a whole frame per client tick; the writer emits
// both into a single websocket message instead.
type h264Frame struct {
	header [headerSize]byte
	data   []byte
}

func newH264Frame(isKeyFrame bool, timestamp int64, data []byte) h264Frame {
	frame := h264Frame{data: data}

	if isKeyFrame {
		frame.header[0] = 1
	}
	binary.LittleEndian.PutUint64(frame.header[1:], uint64(timestamp))

	return frame
}

// client owns the only goroutine that writes to its websocket. The capture
// loop hands frames over through the slot and never blocks on the socket.
type client struct {
	conn *websocket.Conn
	slot *stream.FrameSlot[h264Frame]
	done chan struct{}

	// waitingForKeyFrame is read and written only by the capture goroutine.
	waitingForKeyFrame bool
}

func newClient(conn *websocket.Conn) *client {
	return &client{
		conn: conn,
		slot: stream.NewFrameSlot[h264Frame](),
		done: make(chan struct{}),
	}
}

// enqueue offers a frame to the client.
//
// A client that has not drained the previous frame is behind, and an H.264
// stream with a hole in it stays broken until the next keyframe, so the frame
// is dropped and everything after it is skipped until one arrives.
func (c *client) enqueue(frame h264Frame, isKeyFrame bool) {
	if c.waitingForKeyFrame && !isKeyFrame {
		return
	}

	if !c.slot.TryPut(frame) {
		c.waitingForKeyFrame = true
		return
	}

	c.waitingForKeyFrame = false
}

// write drains the slot until it is closed or the socket fails.
func (c *client) write() {
	defer close(c.done)

	for {
		frame, ok := c.slot.Take()
		if !ok {
			return
		}

		_ = c.conn.SetWriteDeadline(time.Now().Add(writeTimeout))

		if err := writeFrame(c.conn, frame); err != nil {
			log.Debugf("h264 write to %s failed: %s", c.conn.RemoteAddr(), err)

			// Unblock the reader so the handler tears the client down.
			_ = c.conn.Close()

			return
		}
	}
}

// writeFrame emits the header and the payload as one binary message without
// joining them into a new buffer first.
func writeFrame(conn *websocket.Conn, frame h264Frame) error {
	writer, err := conn.NextWriter(websocket.BinaryMessage)
	if err != nil {
		return err
	}

	if _, err := writer.Write(frame.header[:]); err != nil {
		_ = writer.Close()
		return err
	}

	if _, err := writer.Write(frame.data); err != nil {
		_ = writer.Close()
		return err
	}

	return writer.Close()
}

// stop releases the writer and waits for it to let go of the socket.
func (c *client) stop() {
	c.slot.Close()
	<-c.done

	if dropped := c.slot.Dropped(); dropped > 0 {
		log.Debugf("h264 client dropped %d frames", dropped)
	}
}
