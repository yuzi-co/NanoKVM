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

func TestAReadRunsWhileCaptureIsLive(t *testing.T) {
	g := newCaptureGate()

	ran := false
	if !g.withRead(func() {}, func() { ran = true }) {
		t.Fatal("withRead refused while capture is live")
	}
	if !ran {
		t.Fatal("withRead did not call the function")
	}
}

// A read while stopped must not reach kvmv_read_img with the mutex destroyed,
// but refusing it outright is the wrong contract: anything calling read is a
// viewer, and the only other caller besides the streamers is the loopback
// screenshot route that PicoClaw and MCP use. Refusing would break screenshots
// after an idle stop, which work today only because the stop released nothing.
//
// So a read rebuilds the pipeline first, under exclusive access, and then reads.
func TestAReadWhileStoppedResumesAndThenReads(t *testing.T) {
	g := newCaptureGate()
	g.stop(func() {})

	var order []string
	ok := g.withRead(
		func() { order = append(order, "resumed") },
		func() { order = append(order, "read") },
	)

	if !ok {
		t.Fatal("withRead refused after a stop instead of resuming")
	}
	if len(order) != 2 || order[0] != "resumed" || order[1] != "read" {
		t.Fatalf("order was %v, want [resumed, read]", order)
	}
	if !g.isLive() {
		t.Fatal("capture is not live after a read resumed it")
	}
}

// The resume must happen once even if several reads arrive together, because
// kvmv_init recreates the mutex and restarts both libkvm threads.
func TestConcurrentReadsResumeOnlyOnce(t *testing.T) {
	g := newCaptureGate()
	g.stop(func() {})

	var mu sync.Mutex
	resumes := 0

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g.withRead(
				func() { mu.Lock(); resumes++; mu.Unlock() },
				func() {},
			)
		}()
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if resumes != 1 {
		t.Fatalf("resume ran %d times, want 1", resumes)
	}
}

func TestResumeLetsReadsThroughAgain(t *testing.T) {
	g := newCaptureGate()
	g.stop(func() {})
	g.resume(func() {})

	if !g.withRead(func() {}, func() {}) {
		t.Fatal("withRead still refused after a resume")
	}
}

// Both transitions have to be idempotent. The idle timer, a viewer arriving and
// an explicit request from the UI can all land on the same edge, and calling
// kvmv_deinit twice would destroy an already destroyed mutex.
func TestStopAndResumeOnlyActOnce(t *testing.T) {
	g := newCaptureGate()

	stops, resumes := 0, 0
	g.stop(func() { stops++ })
	g.stop(func() { stops++ })
	if stops != 1 {
		t.Errorf("deinit called %d times, want 1", stops)
	}

	g.resume(func() { resumes++ })
	g.resume(func() { resumes++ })
	if resumes != 1 {
		t.Errorf("init called %d times, want 1", resumes)
	}
}

// A stop must wait for readers that are already inside the boundary. If it does
// not, kvmv_deinit runs while kvmv_read_img holds vi_mutex, which is the crash
// this whole change exists to prevent.
func TestStopWaitsForAReadAlreadyInFlight(t *testing.T) {
	g := newCaptureGate()

	readEntered := make(chan struct{})
	releaseRead := make(chan struct{})
	var order []string
	var mu sync.Mutex
	note := func(s string) { mu.Lock(); order = append(order, s); mu.Unlock() }

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		g.withRead(func() {}, func() {
			close(readEntered)
			<-releaseRead
			note("read finished")
		})
	}()

	<-readEntered

	wg.Add(1)
	go func() {
		defer wg.Done()
		g.stop(func() { note("deinit ran") })
	}()

	// Give the stop a chance to run too early, which is the failure being
	// tested for. It must still be blocked when the read is released.
	time.Sleep(50 * time.Millisecond)
	close(releaseRead)
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(order) != 2 || order[0] != "read finished" || order[1] != "deinit ran" {
		t.Fatalf("order was %v, want [read finished, deinit ran]", order)
	}
}

// Reads are shared, so two streamers pulling frames must not serialise against
// each other. Only a stop is exclusive.
func TestReadsDoNotBlockEachOther(t *testing.T) {
	g := newCaptureGate()

	first := make(chan struct{})
	second := make(chan struct{})

	go g.withRead(func() {}, func() { close(first); <-second })
	<-first

	done := make(chan struct{})
	go func() { g.withRead(func() {}, func() {}); close(done) }()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("a second read blocked behind the first")
	}
	close(second)
}
