package hid

import (
	"errors"
	"os"
	"sync"
	"time"

	"NanoKVM-Server/proto"
)

// A HID endpoint can fail in a way that produces no error the operator can act
// on. The gadget driver only completes a write once the target fetches the
// report, so an interface the target is not polling swallows every write until
// the deadline expires. Nothing is broken on this side: the device node is
// present, the gadget is bound, and the USB link is configured.
//
// It happens per endpoint. On one observed target the absolute mouse stopped
// being fetched for forty minutes while the keyboard and the relative mouse kept
// working, so the pointer did not move and nothing anywhere said why. A USB
// re-enumeration cleared it; relative mouse mode also worked throughout. Which
// of the two an operator should reach for depends on the target, and the server
// cannot tell - but it can say which endpoint stopped, which is what neither the
// log nor the web UI did before.
//
// Do not read a stall as a diagnosis of the target. It says one thing only: the
// reports are not being collected.
const (
	hidStateUnknown   = "unknown"   // nothing has been written yet
	hidStateAccepting = "accepting" // the target is fetching reports
	hidStateStalled   = "stalled"   // writes time out: the target is not fetching
	hidStateError     = "error"     // the write failed some other way
)

// endpointHealth remembers the last outcome of a write to one HID endpoint.
// The zero value is usable and means "nothing written yet".
type endpointHealth struct {
	mu       sync.Mutex
	state    string
	detail   string
	since    time.Time // when the current state began
	observed time.Time // when the current state was last confirmed
}

// hidTransition describes what one write did to an endpoint's state. Callers
// log on a change and stay quiet otherwise, so From matters as much as To: the
// first successful write is a change, and it is not a recovery.
type hidTransition struct {
	Changed bool
	From    string
	To      string
}

// record takes the outcome of one write and reports the transition it caused.
func (h *endpointHealth) record(err error, now time.Time) hidTransition {
	state, detail := classifyWriteResult(err)

	h.mu.Lock()
	defer h.mu.Unlock()

	from := h.state
	if from == "" {
		from = hidStateUnknown
	}

	h.observed = now
	if h.state == state && h.detail == detail {
		return hidTransition{Changed: false, From: from, To: state}
	}

	h.state = state
	h.detail = detail
	h.since = now
	return hidTransition{Changed: true, From: from, To: state}
}

func classifyWriteResult(err error) (state string, detail string) {
	switch {
	case err == nil:
		return hidStateAccepting, ""
	case errors.Is(err, os.ErrDeadlineExceeded):
		return hidStateStalled, ""
	default:
		return hidStateError, err.Error()
	}
}

func (h *endpointHealth) snapshot(now time.Time) proto.HidDeviceStatus {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.state == "" {
		return proto.HidDeviceStatus{State: hidStateUnknown}
	}

	return proto.HidDeviceStatus{
		State:         h.state,
		Detail:        h.detail,
		StateForMs:    millisBetween(h.since, now),
		ObservedMsAgo: millisBetween(h.observed, now),
	}
}

func millisBetween(from, to time.Time) int64 {
	elapsed := to.Sub(from)
	if elapsed < 0 {
		return 0
	}
	return elapsed.Milliseconds()
}
