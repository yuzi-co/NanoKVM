package ws

import (
	"testing"
	"time"

	log "github.com/sirupsen/logrus"
)

// drain reads everything currently buffered.
func drain(queue chan []byte) [][]byte {
	var got [][]byte
	for {
		select {
		case data := <-queue:
			got = append(got, data)
		default:
			return got
		}
	}
}

func TestSendQueueDeliversWhenThereIsRoom(t *testing.T) {
	queue := make(chan []byte, 2)

	if !sendQueue(queue, []byte("a")) {
		t.Fatal("expected the event to be accepted")
	}

	if got := drain(queue); len(got) != 1 || string(got[0]) != "a" {
		t.Fatalf("expected the event to be queued, got %q", got)
	}
}

func TestSendQueueNeverBlocksWhenFull(t *testing.T) {
	// A stuck HID device backs the queue up. Blocking here stalls the whole
	// websocket read loop, so heartbeats stop being read and the session dies.
	queue := make(chan []byte, 2)
	queue <- []byte("a")
	queue <- []byte("b")

	done := make(chan bool, 1)
	go func() {
		done <- sendQueue(queue, []byte("c"))
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
	queue := make(chan []byte, 2)
	queue <- []byte("a")
	queue <- []byte("b")

	sendQueue(queue, []byte("c"))

	got := drain(queue)
	if len(got) != 2 {
		t.Fatalf("expected the queue to stay at capacity, got %d entries", len(got))
	}

	if string(got[0]) != "b" || string(got[1]) != "c" {
		t.Fatalf("expected the oldest event to be dropped, got %q and %q", got[0], got[1])
	}
}

func TestSendQueueReportsAClosedQueue(t *testing.T) {
	queue := make(chan []byte, 1)
	close(queue)

	if sendQueue(queue, []byte("a")) {
		t.Fatal("expected a closed queue to be reported")
	}
}

func TestSendQueueReportsANilQueue(t *testing.T) {
	if sendQueue(nil, []byte("a")) {
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
