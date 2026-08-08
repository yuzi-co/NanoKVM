package common

import (
	"sync"
	"testing"
	"time"
)

// The gate exists because libkvm's kvmv_deinit destroys the mutex that
// kvmv_read_img locks, and frees the buffers a reader may still hold. Nothing
// in the C library stops those overlapping, so the exclusion has to happen on
// this side of the boundary.

func TestACallRunsWhileCaptureIsLive(t *testing.T) {
	g := newCaptureGate()

	ran := false
	if !g.withLive(func() { ran = true }) {
		t.Fatal("withLive refused while capture is live")
	}
	if !ran {
		t.Fatal("withLive did not call the function")
	}
}

// After the stop, no call may reach libkvm: kvmv_deinit has destroyed the mutex
// each of them would take. The refusal has to be reported, because the frame
// reads turn it into IMG_NOT_EXIST for the streamers.
func TestACallAfterTheStopIsRefused(t *testing.T) {
	g := newCaptureGate()
	g.stop(func() {})

	ran := false
	if g.withLive(func() { ran = true }) {
		t.Fatal("withLive reported that it ran after the stop")
	}
	if ran {
		t.Fatal("withLive called the function after the stop")
	}

	// The refusal is permanent. Nothing brings the pipeline back, so a second
	// call must not find it live again.
	if g.withLive(func() {}) {
		t.Fatal("withLive ran on a second attempt after the stop")
	}
}

// The stop has to be idempotent. dispose runs it, and a second call must not
// reach kvmv_deinit again: that would destroy an already destroyed mutex.
func TestStopOnlyActsOnce(t *testing.T) {
	g := newCaptureGate()

	stops := 0
	g.stop(func() { stops++ })
	g.stop(func() { stops++ })
	if stops != 1 {
		t.Errorf("deinit called %d times, want 1", stops)
	}
}

// A stop must wait for calls that are already inside the boundary. If it does
// not, kvmv_deinit runs while kvmv_read_img holds vi_mutex, which is the crash
// this whole change exists to prevent.
func TestStopWaitsForACallAlreadyInFlight(t *testing.T) {
	g := newCaptureGate()

	callEntered := make(chan struct{})
	releaseCall := make(chan struct{})
	var order []string
	var mu sync.Mutex
	note := func(s string) { mu.Lock(); order = append(order, s); mu.Unlock() }

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		g.withLive(func() {
			close(callEntered)
			<-releaseCall
			note("call finished")
		})
	}()

	<-callEntered

	wg.Add(1)
	go func() {
		defer wg.Done()
		g.stop(func() { note("deinit ran") })
	}()

	// Give the stop a chance to run too early, which is the failure being
	// tested for. It must still be blocked when the call is released.
	time.Sleep(50 * time.Millisecond)
	close(releaseCall)
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(order) != 2 || order[0] != "call finished" || order[1] != "deinit ran" {
		t.Fatalf("order was %v, want [call finished, deinit ran]", order)
	}
}

// The calls are shared, so two streamers pulling frames must not serialise
// against each other. Only the stop is exclusive.
func TestCallsDoNotBlockEachOther(t *testing.T) {
	g := newCaptureGate()

	first := make(chan struct{})
	second := make(chan struct{})

	go g.withLive(func() { close(first); <-second })
	<-first

	done := make(chan struct{})
	go func() { g.withLive(func() {}); close(done) }()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("a second call blocked behind the first")
	}
	close(second)
}
