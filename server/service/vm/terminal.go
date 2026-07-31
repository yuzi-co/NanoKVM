package vm

import (
	"encoding/json"
	"os"
	"os/exec"
	"time"

	"NanoKVM-Server/middleware"

	"github.com/creack/pty"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"
)

const (
	messageWait    = 10 * time.Second
	maxMessageSize = 1024
	// maxReadSize bounds one client frame; large enough for a paste, small
	// enough that a hostile client cannot exhaust memory.
	maxReadSize = 64 * 1024
)

type WinSize struct {
	Rows uint16 `json:"rows"`
	Cols uint16 `json:"cols"`
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  maxMessageSize,
	WriteBufferSize: maxMessageSize,
	CheckOrigin:     middleware.SameOrigin,
}

func (s *Service) Terminal(c *gin.Context) {
	ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Errorf("failed to init websocket: %s", err)
		return
	}
	defer func() {
		_ = ws.Close()
	}()

	cmd := exec.Command("/bin/sh")
	ptmx, err := pty.Start(cmd)
	if err != nil {
		log.Errorf("failed to start pty: %s", err)
		return
	}
	defer func() {
		_ = ptmx.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	runTerminal(ws, ptmx)
}

// runTerminal pumps the session in both directions and returns once either
// direction has ended, which lets the caller run its teardown.
//
// Close the socket when the writer stops. wsRead sets no read deadline, so it
// returns only when the socket fails, and a client that has stopped reading
// keeps its connection open while it sends nothing. The writer gives up on its
// own deadline, the reader waits for a message that is never coming, and the
// pty and the /bin/sh behind it outlive the session that owned them. Closing
// here is what makes the reader return. The h264 writer closes for the same
// reason; see service/stream/direct/client.go.
func runTerminal(ws *websocket.Conn, ptmx *os.File) {
	go func() {
		wsWrite(ws, ptmx)
		_ = ws.Close()
	}()

	wsRead(ws, ptmx)
}

// pty to ws
func wsWrite(ws *websocket.Conn, ptmx *os.File) {
	data := make([]byte, maxMessageSize)

	for {
		n, err := ptmx.Read(data)
		if err != nil {
			return
		}

		if n > 0 {
			_ = ws.SetWriteDeadline(time.Now().Add(messageWait))

			err = ws.WriteMessage(websocket.BinaryMessage, data[:n])
			if err != nil {
				log.Errorf("write ws message failed: %s", err)
				return
			}
		}
	}
}

// ws to pty
func wsRead(ws *websocket.Conn, ptmx *os.File) {
	var zeroTime time.Time
	_ = ws.SetReadDeadline(zeroTime)
	ws.SetReadLimit(maxReadSize)

	for {
		msgType, p, err := ws.ReadMessage()
		if err != nil {
			return
		}

		// resize message
		if msgType == websocket.BinaryMessage {
			var winSize WinSize
			if err := json.Unmarshal(p, &winSize); err == nil {
				_ = pty.Setsize(ptmx, &pty.Winsize{
					Rows: winSize.Rows,
					Cols: winSize.Cols,
				})
			}
			continue
		}

		_, err = ptmx.Write(p)
		if err != nil {
			log.Errorf("failed to write to pty: %s", err)
			return
		}
	}
}
