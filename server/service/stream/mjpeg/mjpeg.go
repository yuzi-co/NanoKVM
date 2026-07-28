package mjpeg

import (
	"time"

	"github.com/gin-gonic/gin"
)

var streamer = NewStreamer()

type LatestFrame struct {
	Data       []byte
	Width      uint16
	Height     uint16
	CapturedAt time.Time
}

func Connect(c *gin.Context) {
	c.Header("Content-Type", "multipart/x-mixed-replace; boundary=frame")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Pragma", "no-cache")
	c.Header("X-Server-Date", time.Now().Format(time.RFC1123))

	client := streamer.AddClient(c)

	// Wait for the client to go away, or for its writer to give up on it.
	select {
	case <-c.Request.Context().Done():
	case <-client.failed:
	}

	// RemoveClient waits for the writer to stop, which has to happen before
	// this handler returns and the response writer is recycled.
	streamer.RemoveClient(c)
}

func GetLatestFrame() (LatestFrame, bool) {
	return streamer.getLatestFrame()
}

func EnableLatestFrameCache() {
	streamer.enableLatestFrameCache()
}

func DisableLatestFrameCache() {
	streamer.disableLatestFrameCache()
}
