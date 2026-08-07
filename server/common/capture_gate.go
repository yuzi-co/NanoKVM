package common

import "sync"

// captureGate keeps frame reads and capture teardown out of each other's way.
//
// libkvm gives no help here. kvmv_deinit destroys the same vi_mutex that
// kvmv_read_img locks, and calls free_all_kvmv_data() on buffers a reader may
// still be holding. Destroying a locked mutex is undefined behaviour, and so is
// locking a destroyed one, so the exclusion has to be on this side of the cgo
// boundary: nothing in the library will refuse the overlap.
//
// Reads are shared, because two streamers pulling frames must not serialise
// against each other - libkvm returns -5 when it is busy and the streamers
// already handle that. Only a stop or a resume is exclusive.
//
// The build tag files are not the right home for this: it must compile and be
// testable under `novision`, where there is no hardware to reason about.
type captureGate struct {
	mu sync.RWMutex

	// live is false only between a stop and the resume after it. A read while
	// it is false must not reach the library at all.
	live bool
}

func newCaptureGate() *captureGate {
	return &captureGate{live: true}
}

// withRead runs read with the pipeline live, rebuilding it first if an idle stop
// released it. It reports whether read ran.
//
// Refusing the read instead would be simpler and wrong. Anything that calls this
// is a viewer: the streamers, and the loopback screenshot route that PicoClaw
// and MCP use. A refusal would break a screenshot taken after an idle stop -
// which works today only because the stop released nothing on this hardware.
// Resuming here also means no caller has to know the lifecycle exists.
//
// The cost is that the first read after a stop waits for kvmv_init, about a
// second, which is the same price hdmi_idle already pays when a viewer arrives.
func (g *captureGate) withRead(resume func(), read func()) bool {
	g.mu.RLock()
	if g.live {
		defer g.mu.RUnlock()
		read()

		return true
	}
	g.mu.RUnlock()

	// Upgrade to exclusive access to rebuild. Another reader may get there
	// first, so the state is checked again with the write lock held.
	g.mu.Lock()
	if !g.live {
		resume()
		g.live = true
	}
	g.mu.Unlock()

	g.mu.RLock()
	defer g.mu.RUnlock()

	if !g.live {
		return false
	}

	read()

	return true
}

// stop runs deinit once, after every read already inside the boundary has left.
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

// There used to be a resume method and an isLive method here. Both existed for
// service/vm/hdmi_idle.go, which the 2026-08-01 rebase dropped, and both
// outlived it with no caller but their own tests.
//
// withRead already resumes on its own, which is the path that matters: a viewer
// arriving after a stop rebuilds the pipeline without anyone asking. Restoring
// the idle timeout means restoring hdmi_idle.go from backup/pre-rebase-20260801
// and adding an explicit resume back.

// withLive runs fn only while the pipeline is live, and reports whether it ran.
// It never rebuilds.
//
// This is the guard for the calls that are not reads: the HDMI control, the
// signal query and the encoder settings. None of them is a viewer, so none of
// them should pay a second for kvmv_init or bring the pipeline back that a
// caller asked to stop. After Close they must also not reach libkvm at all,
// because kvmv_deinit has destroyed the mutex they would take.
//
// isLive cannot be used for this. Checking it and then calling leaves a window
// where stop runs in between, which is the use-after-free the guard exists to
// prevent. Holding the read lock across fn closes it.
func (g *captureGate) withLive(fn func()) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()

	if !g.live {
		return false
	}

	fn()

	return true
}

