package mjpeg

import (
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"NanoKVM-Server/service/stream"
)

// writeTimeout caps how long a single write may take before the client is
// considered gone.
const writeTimeout = 5 * time.Second

var crlf = []byte("\r\n")

// client owns the only goroutine that writes to its response. The capture loop
// hands frames over through the slot and never blocks on the socket.
type client struct {
	ctx  *gin.Context
	slot *stream.FrameSlot[[]byte]

	// done is closed once the writer has stopped touching the response, and
	// failed once it has given up, which wakes the handler.
	done     chan struct{}
	failed   chan struct{}
	failOnce sync.Once
}

func newClient(c *gin.Context) *client {
	return &client{
		ctx:    c,
		slot:   stream.NewFrameSlot[[]byte](),
		done:   make(chan struct{}),
		failed: make(chan struct{}),
	}
}

// enqueue offers a frame to the client. MJPEG frames are independent, so a
// client that is behind is given the newest frame rather than a backlog.
func (c *client) enqueue(frame []byte) {
	c.slot.Replace(frame)
}

// write drains the slot until it is closed or the response fails.
func (c *client) write() {
	defer close(c.done)

	for {
		frame, ok := c.slot.Take()
		if !ok {
			return
		}

		if err := c.writeFrame(frame); err != nil {
			log.Debugf("mjpeg write to %s failed: %s", c.ctx.Request.RemoteAddr, err)
			c.fail()

			return
		}
	}
}

func (c *client) writeFrame(data []byte) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = c.ctx.Request.Context().Err()
			if err == nil {
				err = fmt.Errorf("panic recovered in writeFrame: %v", r)
			}
		}
	}()

	// Without a deadline a client that stops reading blocks this goroutine
	// forever. Not every writer supports deadlines.
	_ = http.NewResponseController(c.ctx.Writer).SetWriteDeadline(time.Now().Add(writeTimeout))

	header := "--frame\r\nContent-Type: image/jpeg\r\nContent-Length: " + strconv.Itoa(len(data)) + "\r\n\r\n"
	if _, err = c.ctx.Writer.WriteString(header); err != nil {
		return err
	}

	if _, err = c.ctx.Writer.Write(data); err != nil {
		return err
	}

	if _, err = c.ctx.Writer.Write(crlf); err != nil {
		return err
	}

	c.ctx.Writer.Flush()

	return nil
}

// fail wakes the handler so it can tear this client down.
func (c *client) fail() {
	c.failOnce.Do(func() {
		close(c.failed)
	})
}

// stop releases the writer and waits for it to let go of the response, which
// the handler must do before returning.
func (c *client) stop() {
	c.slot.Close()
	<-c.done

	if dropped := c.slot.Dropped(); dropped > 0 {
		log.Debugf("mjpeg client dropped %d frames", dropped)
	}
}
