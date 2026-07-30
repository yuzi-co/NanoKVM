package hid

import (
	"errors"
	"os"
	"testing"
	"time"
)

func at(seconds int) time.Time {
	return time.Unix(1700000000, 0).Add(time.Duration(seconds) * time.Second)
}

func TestHealthStartsUnknown(t *testing.T) {
	var h endpointHealth

	got := h.snapshot(at(0))
	if got.State != hidStateUnknown {
		t.Fatalf("state = %q, want %q", got.State, hidStateUnknown)
	}
	if got.Detail != "" {
		t.Fatalf("detail = %q, want empty", got.Detail)
	}
}

func TestHealthFirstSuccessIsAChange(t *testing.T) {
	var h endpointHealth

	if changed := h.record(nil, at(0)).Changed; !changed {
		t.Fatal("first successful write should report a state change")
	}
	if got := h.snapshot(at(0)).State; got != hidStateAccepting {
		t.Fatalf("state = %q, want %q", got, hidStateAccepting)
	}
}

func TestHealthRepeatedSuccessIsNotAChange(t *testing.T) {
	var h endpointHealth

	h.record(nil, at(0))
	if changed := h.record(nil, at(1)).Changed; changed {
		t.Fatal("a second successful write should not report a change")
	}
}

// The distinction that matters: a write that times out means the target is not
// fetching from that endpoint, which is a different fault from the device node
// being gone. Only the first one warrants "switch to relative mouse".
func TestHealthTimeoutStalls(t *testing.T) {
	var h endpointHealth

	h.record(nil, at(0))
	if changed := h.record(os.ErrDeadlineExceeded, at(1)).Changed; !changed {
		t.Fatal("the first timeout should report a state change")
	}

	got := h.snapshot(at(1))
	if got.State != hidStateStalled {
		t.Fatalf("state = %q, want %q", got.State, hidStateStalled)
	}
}

func TestHealthOtherErrorsAreNotStalls(t *testing.T) {
	var h endpointHealth

	h.record(nil, at(0))
	h.record(errors.New("no such device"), at(1))

	got := h.snapshot(at(1))
	if got.State != hidStateError {
		t.Fatalf("state = %q, want %q", got.State, hidStateError)
	}
	if got.Detail != "no such device" {
		t.Fatalf("detail = %q, want the error text", got.Detail)
	}
}

// This is the whole point of the type: 20 identical failures per second became
// 20 log lines per second, and the operator learned nothing after the first.
func TestHealthRepeatedTimeoutsAreNotChanges(t *testing.T) {
	var h endpointHealth

	h.record(os.ErrDeadlineExceeded, at(0))
	for i := 1; i < 100; i++ {
		if changed := h.record(os.ErrDeadlineExceeded, at(i)).Changed; changed {
			t.Fatalf("timeout %d reported a change; only the first should", i)
		}
	}
}

// A stall that has lasted ten minutes has to read as ten minutes, so the clock
// starts when the state began and not when it was last confirmed.
func TestHealthStalledForMeasuresFromTheFirstFailure(t *testing.T) {
	var h endpointHealth

	h.record(os.ErrDeadlineExceeded, at(10))
	h.record(os.ErrDeadlineExceeded, at(20))
	h.record(os.ErrDeadlineExceeded, at(30))

	got := h.snapshot(at(70))
	if want := int64(60000); got.StateForMs != want {
		t.Fatalf("stateForMs = %d, want %d", got.StateForMs, want)
	}
}

// Once the mouse mode is switched away, nothing writes to the dead endpoint any
// more, so its state stops being refreshed. A consumer has to be able to tell a
// live observation from a stale one rather than reporting a fault that may have
// cured itself.
func TestHealthReportsHowOldTheObservationIs(t *testing.T) {
	var h endpointHealth

	h.record(os.ErrDeadlineExceeded, at(10))

	got := h.snapshot(at(25))
	if want := int64(15000); got.ObservedMsAgo != want {
		t.Fatalf("observedMsAgo = %d, want %d", got.ObservedMsAgo, want)
	}
}

func TestHealthRecoveryIsAChange(t *testing.T) {
	var h endpointHealth

	h.record(os.ErrDeadlineExceeded, at(0))
	h.record(os.ErrDeadlineExceeded, at(1))

	if changed := h.record(nil, at(2)).Changed; !changed {
		t.Fatal("recovering should report a state change")
	}
	if got := h.snapshot(at(2)).State; got != hidStateAccepting {
		t.Fatalf("state = %q, want %q", got, hidStateAccepting)
	}
}

// A first successful write is a change, but it is not a recovery, and calling
// it one at boot would report a fault that never happened.
func TestHealthFirstSuccessComesFromUnknown(t *testing.T) {
	var h endpointHealth

	got := h.record(nil, at(0))
	if got.From != hidStateUnknown {
		t.Fatalf("from = %q, want %q", got.From, hidStateUnknown)
	}

	recovery := func() hidTransition {
		h.record(os.ErrDeadlineExceeded, at(1))
		return h.record(nil, at(2))
	}()
	if recovery.From != hidStateStalled {
		t.Fatalf("from = %q, want %q", recovery.From, hidStateStalled)
	}
}

func TestHealthMovingBetweenFaultsIsAChange(t *testing.T) {
	var h endpointHealth

	h.record(os.ErrDeadlineExceeded, at(0))
	if changed := h.record(errors.New("no such device"), at(1)).Changed; !changed {
		t.Fatal("moving from a stall to a different fault should report a change")
	}
	if changed := h.record(os.ErrDeadlineExceeded, at(2)).Changed; !changed {
		t.Fatal("moving back to a stall should report a change")
	}
}

// A wrapped timeout still has to read as a timeout: writeHID returns the error
// with context attached.
func TestHealthUnwrapsTimeouts(t *testing.T) {
	var h endpointHealth

	h.record(wrapped{os.ErrDeadlineExceeded}, at(0))

	if got := h.snapshot(at(0)).State; got != hidStateStalled {
		t.Fatalf("state = %q, want %q", got, hidStateStalled)
	}
}

type wrapped struct{ err error }

func (w wrapped) Error() string { return "timeout after 50ms: " + w.err.Error() }
func (w wrapped) Unwrap() error { return w.err }
