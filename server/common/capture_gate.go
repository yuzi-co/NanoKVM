package common

import "sync"

// captureGate keeps calls into libkvm out of the way of capture teardown.
//
// libkvm gives no help here. kvmv_deinit destroys the same vi_mutex that
// kvmv_read_img locks, and calls free_all_kvmv_data() on buffers a reader may
// still be holding. Destroying a locked mutex is undefined behaviour, and so is
// locking a destroyed one, so the exclusion has to be on this side of the cgo
// boundary: nothing in the library will refuse the overlap.
//
// The calls are shared, because two streamers pulling frames must not serialise
// against each other - libkvm returns -5 when it is busy and the streamers
// already handle that. Only the stop is exclusive.
//
// The build tag files are not the right home for this: it must compile and be
// testable under `novision`, where there is no hardware to reason about.
type captureGate struct {
	mu sync.RWMutex

	// live goes from true to false once and stays there. The only caller of
	// stop is the teardown in main.go's dispose, and the process exits after
	// it, so nothing has to bring the pipeline back.
	live bool
}

func newCaptureGate() *captureGate {
	return &captureGate{live: true}
}

// withLive runs fn only while the pipeline is live, and reports whether it ran.
//
// Every call into libkvm goes through here: the frame reads, the HDMI control,
// the signal query and the encoder settings. After the stop none of them may
// reach the library at all, because kvmv_deinit has destroyed the mutex they
// would take.
//
// A live flag that a caller checks before calling would not close the hole. It
// leaves a window for the stop to run in between, which is the use-after-free
// the gate exists to prevent. Holding the read lock across fn closes it.
func (g *captureGate) withLive(fn func()) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if !g.live {
		return false
	}

	fn()

	return true
}

// stop runs deinit once, after every call already inside the boundary has left.
// A second stop does nothing: calling kvmv_deinit twice would destroy a mutex
// that is already destroyed.
func (g *captureGate) stop(deinit func()) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if !g.live {
		return
	}

	deinit()
	g.live = false
}

// There used to be a withRead method that rebuilt the pipeline before reading,
// and resume and isLive helpers beside it. All of that served
// service/vm/hdmi_idle.go, which released capture after an idle timeout and
// which the 2026-08-01 rebase dropped.
//
// Upstream has since added an idle timeout of its own in service/vm/hdmi.go,
// and it does not release the pipeline: scheduleHdmiIdleTimerLocked calls
// setHDMI(false), which reaches kvmv_hdmi_control, not kvmv_deinit. So the only
// stop that runs on a device is the one in dispose, and the process exits
// behind it. A rebuild-on-read could not fire, and the two tests that covered
// it could not fail.
//
// Restoring a rebuild means first restoring a caller that stops capture without
// ending the process. There is none today.
