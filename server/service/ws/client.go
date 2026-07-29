package ws

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"NanoKVM-Server/service/controlmode"
	"NanoKVM-Server/service/hid"
	"NanoKVM-Server/service/inputcontrol"
	"NanoKVM-Server/service/picoclaw"
	"NanoKVM-Server/service/vm/jiggler"

	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"
)

const (
	Heartbeat = iota
	KeyboardEvent
	MouseEvent
)

// maxMessageSize bounds a single client frame. HID events are a handful of
// bytes, so this is generous.
const maxMessageSize = 4 * 1024

const (
	manualPreemptTimeout   = 2 * time.Second
	clientHeartbeatTimeout = 90 * time.Second
)

func NewClient(ws *websocket.Conn) *Client {
	client := &Client{
		ws:               ws,
		hid:              hid.GetHid(),
		manual:           inputcontrol.NewManualSession(controlmode.GetManager(), inputcontrol.GetCoordinator()),
		keyboard:         make(chan hid.QueuedReport, 200),
		mouse:            make(chan hid.QueuedReport, 200),
		heartbeatTimeout: clientHeartbeatTimeout,
		lastHeartbeat:    time.Time{},
	}

	client.hid.Open()

	return client
}

func (c *Client) Start() {
	c.workers.Add(2)
	go func() {
		defer c.workers.Done()
		c.hid.KeyboardReports(c.keyboard)
	}()
	go func() {
		defer c.workers.Done()
		c.hid.MouseReports(c.mouse)
	}()

	_ = c.Read()
	c.Close()
}

func (c *Client) Read() error {
	c.ws.SetReadLimit(maxMessageSize)
	if err := c.UpdateHeartbeat(); err != nil {
		return err
	}

	for {
		messageType, data, err := c.ws.ReadMessage()
		if err != nil {
			return err
		}
		if err := c.UpdateHeartbeat(); err != nil {
			return err
		}

		if len(data) == 0 {
			continue
		}

		traceMessage(messageType, data)

		switch data[0] {
		case Heartbeat:
		case KeyboardEvent:
			report := data[1:]
			if len(report) != 8 {
				log.Debugf("invalid manual keyboard report: %v", report)
				continue
			}
			c.queueManualReport(c.keyboard, inputcontrol.ManualKeyboard, report, keyboardReportHeld(report), true)
		case MouseEvent:
			report := data[1:]
			if len(report) != 4 && len(report) != 6 {
				log.Debugf("invalid manual mouse report: %v", report)
				continue
			}
			kind := inputcontrol.ManualRelativeMouse
			if len(report) == 6 {
				kind = inputcontrol.ManualAbsoluteMouse
			}
			c.queueManualReport(c.mouse, kind, report, report[0] != 0, mouseReportStartsCooldown(report))
		}
	}
}

func (c *Client) queueManualReport(queue chan hid.QueuedReport, kind inputcontrol.ManualReportKind, report []byte, held bool, startCooldown bool) {
	ctx, cancel := context.WithTimeout(context.Background(), manualPreemptTimeout)
	defer cancel()

	reservation, err := c.manual.ReserveWithCooldown(ctx, kind, held, startCooldown, func(mode controlmode.Mode) bool {
		return mode != controlmode.ModePicoclaw || !picoclaw.GetSessionLock().BlocksManualInput()
	})
	if err != nil {
		if errors.Is(err, inputcontrol.ErrManualInputBlocked) {
			log.Debug("manual HID input dropped while PicoClaw session holds control")
		} else {
			log.Errorf("manual HID input failed to acquire control: %s", err)
		}
		return
	}

	queued := hid.QueuedReport{
		Data:               append([]byte(nil), report...),
		Execute:            c.manual.Execute,
		Complete:           reservation.Complete,
		ResetKeyboard:      func() { c.manual.Reset(inputcontrol.ManualKeyboard) },
		ResetRelativeMouse: func() { c.manual.Reset(inputcontrol.ManualRelativeMouse) },
		ResetAbsoluteMouse: func() { c.manual.Reset(inputcontrol.ManualAbsoluteMouse) },
	}
	if !writeQueue(queue, queued) {
		return
	}
	jiggler.GetJiggler().Update()
}

func keyboardReportHeld(report []byte) bool {
	if len(report) != 8 {
		return false
	}
	if report[0] != 0 {
		return true
	}
	for _, key := range report[2:] {
		if key != 0 {
			return true
		}
	}
	return false
}

func mouseReportStartsCooldown(report []byte) bool {
	if len(report) != 4 && len(report) != 6 {
		return true
	}
	if report[0] != 0 {
		return true
	}
	return report[len(report)-1] != 0
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

func (c *Client) UpdateHeartbeat() error {
	now := time.Now()
	timeout := c.heartbeatTimeout
	if timeout <= 0 {
		timeout = clientHeartbeatTimeout
	}
	c.mutex.Lock()
	c.lastHeartbeat = now
	c.mutex.Unlock()

	return c.ws.SetReadDeadline(now.Add(timeout))
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
	})
	c.workers.Wait()
	if c.manual != nil {
		c.manual.Close()
	}
	log.Debug("websocket disconnected")
}

func writeQueue(queue chan hid.QueuedReport, report hid.QueuedReport) bool {
	if !sendQueue(queue, report) {
		report.Complete(false)
		log.Debug("hid event dropped because websocket queue is closed")
		return false
	}
	return true
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
// session would look dead. Reports whether the queue took the report; a false
// answer leaves completing it to the caller.
func sendQueue(queue chan hid.QueuedReport, report hid.QueuedReport) (ok bool) {
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
		case queue <- report:
			return true
		default:
		}

		// Drop the stalest report. HID reports carry absolute state rather
		// than deltas, so the newest one describes the truth on its own and
		// the backlog is worth losing. The dropped report still holds a
		// manual-control reservation, and the coordinator only gives control
		// back once every reservation is completed, so dropping one without
		// completing it would pin the session to manual for good.
		select {
		case dropped := <-queue:
			if dropped.Complete != nil {
				dropped.Complete(false)
			}
		default:
		}
	}

	// Unreachable with a single producer, but every report must end up either
	// queued or completed, so report failure rather than claim a send.
	return false
}
