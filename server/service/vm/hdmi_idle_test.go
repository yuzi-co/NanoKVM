package vm

import (
	"sync"
	"testing"
	"time"
)

// captureRecorder stands in for the capture hardware and reports every change.
type captureRecorder struct {
	mutex   sync.Mutex
	changes []bool
	updated chan struct{}
}

func newCaptureRecorder() *captureRecorder {
	return &captureRecorder{updated: make(chan struct{}, 32)}
}

func (r *captureRecorder) set(on bool) {
	r.mutex.Lock()
	r.changes = append(r.changes, on)
	r.mutex.Unlock()

	select {
	case r.updated <- struct{}{}:
	default:
	}
}

// awaitState waits for capture to reach on, and reports whether it did.
func (r *captureRecorder) awaitState(t *testing.T, on bool) bool {
	t.Helper()

	deadline := time.After(2 * time.Second)
	for {
		r.mutex.Lock()
		last := len(r.changes) > 0 && r.changes[len(r.changes)-1] == on
		r.mutex.Unlock()

		if last {
			return true
		}

		select {
		case <-r.updated:
		case <-deadline:
			return false
		}
	}
}

func (r *captureRecorder) changed() bool {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	return len(r.changes) > 0
}

// withIdleCapture installs a recorder and a short timeout for one test.
func withIdleCapture(t *testing.T, timeout time.Duration, disabled bool) *captureRecorder {
	t.Helper()

	recorder := newCaptureRecorder()

	originalSet, originalTimeout, originalDisabled := setCapture, idleTimeout, captureDisabled
	setCapture = recorder.set
	idleTimeout = func() time.Duration { return timeout }
	captureDisabled = func() bool { return disabled }

	t.Cleanup(func() {
		// Drain first: a reset still in flight is holding these.
		resetIdleState()
		setCapture, idleTimeout, captureDisabled = originalSet, originalTimeout, originalDisabled
	})

	resetIdleState()

	return recorder
}

func TestCaptureStopsOnceNobodyIsWatching(t *testing.T) {
	// An idle NanoKVM encodes video into nothing, which costs power and heat
	// for as long as it is plugged in.
	recorder := withIdleCapture(t, 20*time.Millisecond, false)

	SetViewerCount("mjpeg", 1)
	SetViewerCount("mjpeg", 0)

	if !recorder.awaitState(t, false) {
		t.Fatal("expected capture to stop after the idle timeout")
	}
}

func TestAViewerArrivingInTimeKeepsCaptureRunning(t *testing.T) {
	recorder := withIdleCapture(t, 200*time.Millisecond, false)

	SetViewerCount("mjpeg", 1)
	SetViewerCount("mjpeg", 0)
	SetViewerCount("mjpeg", 1)

	time.Sleep(400 * time.Millisecond)

	if recorder.changed() {
		t.Fatal("capture must not be touched while someone is watching")
	}
}

func TestCaptureComesBackForANewViewer(t *testing.T) {
	recorder := withIdleCapture(t, 20*time.Millisecond, false)

	SetViewerCount("mjpeg", 1)
	SetViewerCount("mjpeg", 0)

	if !recorder.awaitState(t, false) {
		t.Fatal("setup: expected capture to stop first")
	}

	SetViewerCount("mjpeg", 1)

	if !recorder.awaitState(t, true) {
		t.Fatal("expected capture to resume for a new viewer")
	}
}

func TestResumingDoesNotBlockTheCaller(t *testing.T) {
	// This runs on the path that accepts a stream client. The capture reset
	// takes about a second, and holding the caller there would stall every
	// other viewer behind it.
	recorder := withIdleCapture(t, 20*time.Millisecond, false)

	SetViewerCount("mjpeg", 1)
	SetViewerCount("mjpeg", 0)

	if !recorder.awaitState(t, false) {
		t.Fatal("setup: expected capture to stop first")
	}

	done := make(chan struct{})
	go func() {
		SetViewerCount("mjpeg", 1)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("adding a viewer blocked on the capture reset")
	}
}

func TestZeroTimeoutLeavesCaptureAlone(t *testing.T) {
	// Zero is the default and means never stop.
	recorder := withIdleCapture(t, 0, false)

	SetViewerCount("mjpeg", 1)
	SetViewerCount("mjpeg", 0)

	time.Sleep(100 * time.Millisecond)

	if recorder.changed() {
		t.Fatal("capture must not be stopped when the feature is off")
	}
}

func TestOneStreamGoingIdleDoesNotStopAnother(t *testing.T) {
	// A viewer switching from mjpeg to webrtc briefly leaves both counted.
	recorder := withIdleCapture(t, 20*time.Millisecond, false)

	SetViewerCount("mjpeg", 1)
	SetViewerCount("webrtc", 1)
	SetViewerCount("mjpeg", 0)

	time.Sleep(100 * time.Millisecond)

	if recorder.changed() {
		t.Fatal("capture must keep running while another stream has viewers")
	}
}

func TestCaptureSwitchedOffByHandIsNotResumed(t *testing.T) {
	// The user turned capture off. A viewer connecting must not override that.
	recorder := withIdleCapture(t, 20*time.Millisecond, true)

	SetViewerCount("mjpeg", 1)

	time.Sleep(100 * time.Millisecond)

	if recorder.changed() {
		t.Fatal("a disabled port must stay disabled")
	}
}
