package ws

import (
	"encoding/json"
	"time"

	"NanoKVM-Server/service/hid"
	"NanoKVM-Server/service/picoclaw"
	"NanoKVM-Server/service/vm/jiggler"

	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"
)

// maxMessageSize bounds a single client frame. HID events are a handful of
// bytes, so this is generous.
const maxMessageSize = 4 * 1024

const (
	Heartbeat = iota
	KeyboardEvent
	MouseEvent
)

func NewClient(ws *websocket.Conn) *Client {
	client := &Client{
		ws:            ws,
		hid:           hid.GetHid(),
		keyboard:      make(chan []byte, 200),
		mouse:         make(chan []byte, 200),
		lastHeartbeat: time.Time{},
	}

	client.hid.Open()

	return client
}

func (c *Client) Start() {
	defer c.Close()

	go c.hid.Keyboard(c.keyboard)
	go c.hid.Mouse(c.mouse)

	_ = c.Read()
}

func (c *Client) Read() error {
	var zeroTime time.Time
	_ = c.ws.SetReadDeadline(zeroTime)
	c.ws.SetReadLimit(maxMessageSize)

	for {
		messageType, data, err := c.ws.ReadMessage()
		if err != nil {
			return err
		}

		if len(data) == 0 {
			continue
		}

		traceMessage(messageType, data)

		switch data[0] {
		case Heartbeat:
			c.UpdateHeartbeat()
		case KeyboardEvent:
			if picoclaw.GetSessionLock().BlocksManualInput() {
				log.Debug("manual keyboard input dropped while AI session holds control")
				continue
			}
			writeQueue(c.keyboard, data[1:])
		case MouseEvent:
			if picoclaw.GetSessionLock().BlocksManualInput() {
				log.Debug("manual mouse input dropped while AI session holds control")
				continue
			}
			writeQueue(c.mouse, data[1:])
		}
	}
}

func (c *Client) Write(event string, data string) error {
	message := &Message{
		Type: event,
		Data: data,
	}

	messageByte, err := json.Marshal(message)
	if err != nil {
		log.Errorf("failed to marshal message: %s", err)
		return err
	}

	c.mutex.Lock()
	defer c.mutex.Unlock()

	_ = c.ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return c.ws.WriteMessage(websocket.TextMessage, messageByte)
}

func (c *Client) UpdateHeartbeat() {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.lastHeartbeat = time.Now()
}

func (c *Client) Close() {
	c.closeOnce.Do(func() {
		if c.ws != nil {
			_ = c.ws.Close()
		}

		if c.keyboard != nil {
			close(c.keyboard)
		}
		if c.mouse != nil {
			close(c.mouse)
		}

		log.Debug("websocket disconnected")
	})
}

func writeQueue(queue chan []byte, data []byte) {
	if !sendQueue(queue, data) {
		log.Debug("hid event dropped because websocket queue is closed")
		return
	}

	jiggler.GetJiggler().Update()
}

// traceMessage logs an inbound event only when someone is listening. This runs
// once per keystroke and per mouse move, and the variadic call allocates even
// when the level is off.
func traceMessage(messageType int, data []byte) {
	if !log.IsLevelEnabled(log.DebugLevel) {
		return
	}

	log.Debugf("received message %d: %v", messageType, data)
}

// sendQueue hands an event to a HID writer without ever blocking. A host that
// has stopped accepting HID reports backs the queue up, and blocking here would
// stall the websocket read loop, so heartbeats would stop being read and the
// session would look dead. Reports whether the queue is still usable.
func sendQueue(queue chan []byte, data []byte) (ok bool) {
	if queue == nil {
		return false
	}

	defer func() {
		if r := recover(); r != nil {
			ok = false
		}
	}()

	// Only the read loop produces here, so evicting once is always enough to
	// make room. The bound is a guard, not an expectation.
	for range cap(queue) + 1 {
		select {
		case queue <- data:
			return true
		default:
		}

		// Drop the stalest report. HID reports carry absolute state rather
		// than deltas, so the newest one describes the truth on its own and
		// the backlog is worth losing.
		select {
		case <-queue:
		default:
		}
	}

	return true
}
