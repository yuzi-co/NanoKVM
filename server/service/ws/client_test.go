package ws

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"NanoKVM-Server/service/controlmode"
	"NanoKVM-Server/service/hid"
	"NanoKVM-Server/service/inputcontrol"

	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"
)

func TestHeartbeatTimeoutReleasesManualLease(t *testing.T) {
	control := controlmode.NewManager(filepath.Join(t.TempDir(), "mode"), controlmode.ModeMCP)
	coordinator := &inputcontrol.Coordinator{}
	manual := inputcontrol.NewManualSession(control, coordinator)
	defer manual.Close()

	reservation, err := manual.Reserve(context.Background(), inputcontrol.ManualKeyboard, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	reservation.Complete(true)

	connected := make(chan struct{})
	serverDone := make(chan struct{})
	upgrade := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrade.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		client := &Client{
			ws:               conn,
			hid:              hid.GetHid(),
			manual:           manual,
			keyboard:         make(chan hid.QueuedReport, 1),
			mouse:            make(chan hid.QueuedReport, 1),
			heartbeatTimeout: 30 * time.Millisecond,
		}
		close(connected)
		client.Start()
		close(serverDone)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	select {
	case <-connected:
	case <-time.After(time.Second):
		t.Fatal("websocket client did not connect")
	}

	switchDone := make(chan error, 1)
	go func() {
		switchDone <- control.SwitchToPicoclaw(nil)
	}()

	select {
	case err := <-switchDone:
		if err != nil {
			t.Fatalf("switch after heartbeat timeout failed: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("heartbeat timeout did not release the manual control lease")
	}
	if got := control.Current(); got != controlmode.ModePicoclaw {
		t.Fatalf("mode = %q, want picoclaw", got)
	}

	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("websocket client did not close after heartbeat timeout")
	}
}

func TestMouseReportStartsCooldown(t *testing.T) {
	tests := []struct {
		name   string
		report []byte
		want   bool
	}{
		{name: "relative move", report: []byte{0, 10, 0, 0}, want: false},
		{name: "relative wheel", report: []byte{0, 0, 0, 1}, want: true},
		{name: "relative button", report: []byte{1, 0, 0, 0}, want: true},
		{name: "absolute move", report: []byte{0, 1, 0, 1, 0, 0}, want: false},
		{name: "absolute wheel", report: []byte{0, 1, 0, 1, 0, 0xff}, want: true},
		{name: "absolute button", report: []byte{1, 1, 0, 1, 0, 0}, want: true},
		{name: "invalid", report: []byte{0, 1}, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mouseReportStartsCooldown(tt.report); got != tt.want {
				t.Fatalf("mouseReportStartsCooldown(%v) = %v, want %v", tt.report, got, tt.want)
			}
		})
	}
}

// --- queue policy -----------------------------------------------------------

// report builds a queued report that records how it was completed.
func report(name string, completions *[]string, mu *sync.Mutex) hid.QueuedReport {
	return hid.QueuedReport{
		Data: []byte(name),
		Complete: func(success bool) {
			mu.Lock()
			defer mu.Unlock()
			*completions = append(*completions, fmt.Sprintf("%s=%v", name, success))
		},
	}
}

// drain reads everything currently buffered.
func drain(queue chan hid.QueuedReport) []string {
	var got []string
	for {
		select {
		case r := <-queue:
			got = append(got, string(r.Data))
		default:
			return got
		}
	}
}

func TestSendQueueDeliversWhenThereIsRoom(t *testing.T) {
	var mu sync.Mutex
	var completions []string
	queue := make(chan hid.QueuedReport, 2)

	if !sendQueue(queue, report("a", &completions, &mu)) {
		t.Fatal("expected the event to be accepted")
	}

	if got := drain(queue); len(got) != 1 || got[0] != "a" {
		t.Fatalf("expected the event to be queued, got %q", got)
	}
}

func TestSendQueueNeverBlocksWhenFull(t *testing.T) {
	// A stuck HID device backs the queue up. Blocking here stalls the whole
	// websocket read loop, so heartbeats stop being read and the session dies.
	var mu sync.Mutex
	var completions []string
	queue := make(chan hid.QueuedReport, 2)
	queue <- report("a", &completions, &mu)
	queue <- report("b", &completions, &mu)

	done := make(chan bool, 1)
	go func() {
		done <- sendQueue(queue, report("c", &completions, &mu))
	}()

	select {
	case ok := <-done:
		if !ok {
			t.Fatal("expected the newest event to be accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("sendQueue blocked on a full queue")
	}
}

func TestSendQueueDropsTheOldestEventWhenFull(t *testing.T) {
	// HID reports carry absolute state, not deltas, so the newest report
	// describes the truth. Stale reports are the ones worth losing.
	var mu sync.Mutex
	var completions []string
	queue := make(chan hid.QueuedReport, 2)
	queue <- report("a", &completions, &mu)
	queue <- report("b", &completions, &mu)

	sendQueue(queue, report("c", &completions, &mu))

	got := drain(queue)
	if len(got) != 2 {
		t.Fatalf("expected the queue to stay at capacity, got %d entries", len(got))
	}
	if got[0] != "b" || got[1] != "c" {
		t.Fatalf("expected the oldest event to be dropped, got %q and %q", got[0], got[1])
	}
}

func TestSendQueueCompletesTheReportsItDrops(t *testing.T) {
	// Every queued report holds a manual-control reservation. The coordinator
	// only releases control once every reservation is completed, so a report
	// that is dropped without being completed leaves the session pinned to
	// manual forever and PicoClaw can never take over.
	var mu sync.Mutex
	var completions []string
	queue := make(chan hid.QueuedReport, 2)
	queue <- report("a", &completions, &mu)
	queue <- report("b", &completions, &mu)

	sendQueue(queue, report("c", &completions, &mu))

	mu.Lock()
	defer mu.Unlock()
	if len(completions) != 1 || completions[0] != "a=false" {
		t.Fatalf("expected the dropped report to be completed as failed, got %v", completions)
	}
}

func TestSendQueueReportsAClosedQueue(t *testing.T) {
	var mu sync.Mutex
	var completions []string
	queue := make(chan hid.QueuedReport, 1)
	close(queue)

	if sendQueue(queue, report("a", &completions, &mu)) {
		t.Fatal("expected a closed queue to be reported")
	}
}

func TestSendQueueReportsANilQueue(t *testing.T) {
	var mu sync.Mutex
	var completions []string

	if sendQueue(nil, report("a", &completions, &mu)) {
		t.Fatal("expected a nil queue to be reported")
	}
}

func TestTracingAMessageCostsNothingWhenDebugIsOff(t *testing.T) {
	previous := log.GetLevel()
	log.SetLevel(log.ErrorLevel)
	t.Cleanup(func() { log.SetLevel(previous) })

	event := []byte{MouseEvent, 0, 1, 2}

	allocs := testing.AllocsPerRun(200, func() {
		traceMessage(2, event)
	})

	if allocs != 0 {
		t.Fatalf("expected no allocations while debug logging is off, got %v", allocs)
	}
}

func TestAnOversizedFrameEndsTheReadLoop(t *testing.T) {
	// Without a read limit a client can hand the server an arbitrarily large
	// frame and the server will buffer all of it, on a board with 158MB of RAM.
	control := controlmode.NewManager(filepath.Join(t.TempDir(), "mode"), controlmode.ModeMCP)
	manual := inputcontrol.NewManualSession(control, &inputcontrol.Coordinator{})
	defer manual.Close()

	connected := make(chan struct{})
	serverDone := make(chan struct{})
	upgrade := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrade.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		client := &Client{
			ws:       conn,
			hid:      hid.GetHid(),
			manual:   manual,
			keyboard: make(chan hid.QueuedReport, 1),
			mouse:    make(chan hid.QueuedReport, 1),
			// Generous, so only the read limit can end the loop quickly.
			heartbeatTimeout: 30 * time.Second,
		}
		close(connected)
		client.Start()
		close(serverDone)
	}))
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	select {
	case <-connected:
	case <-time.After(time.Second):
		t.Fatal("websocket client did not connect")
	}

	oversized := make([]byte, maxMessageSize+1)
	oversized[0] = MouseEvent
	if err := conn.WriteMessage(websocket.BinaryMessage, oversized); err != nil {
		t.Fatal(err)
	}

	select {
	case <-serverDone:
	case <-time.After(2 * time.Second):
		t.Fatal("an oversized frame did not end the read loop")
	}
}
