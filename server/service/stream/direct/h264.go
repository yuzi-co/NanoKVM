package direct

import (
	"time"

	"NanoKVM-Server/middleware"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"
)

// The client sends nothing on this socket, so anything sizeable is abuse.
const maxReadSize = 1024

var (
	streamer = newStreamer()
	upgrader = websocket.Upgrader{
		WriteBufferSize: 256 * 1024,
		CheckOrigin:     middleware.SameOrigin,
	}
)

func Connect(c *gin.Context) {
	ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Errorf("failed to upgrade to websocket: %s", err)
		return
	}
	defer func() {
		_ = ws.Close()
		log.Debugf("h264 websocket disconnected: %s", ws.RemoteAddr())
	}()
	log.Debugf("h264 websocket connected: %s", ws.RemoteAddr())

	_ = ws.SetReadDeadline(time.Time{})
	// The client never sends anything on this socket.
	ws.SetReadLimit(maxReadSize)

	streamer.addClient(ws)
	defer streamer.removeClient(ws)

	for {
		if _, _, err := ws.NextReader(); err != nil {
			log.Debugf("failed to read message (client disconnected): %s", err)
			return
		}
	}
}
