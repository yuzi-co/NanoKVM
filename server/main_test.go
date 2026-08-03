package main

import (
	"sync/atomic"
	"testing"
	"time"
)

// The signal handler used to call dispose() and then os.Exit(0). dispose()
// tears the capture pipeline down through cgo, and kvmv_deinit joins libkvm's
// threads, so a thread that does not return leaves the process alive after a
// SIGTERM. The init script that sent the signal does not wait, so the next
// server starts while this one still owns the VI pipeline, and its channel
// enable fails with ENOMEM.
//
// A process on its way out does not need a clean teardown. Bound it.
func TestDisposeWithinReportsATeardownThatFinished(t *testing.T) {
	var ran atomic.Bool

	finished := disposeWithin(5*time.Second, func() { ran.Store(true) })

	if !finished {
		t.Fatal("expected a teardown that returns to be reported as finished")
	}
	if !ran.Load() {
		t.Fatal("expected the teardown to have run")
	}
}

func TestDisposeWithinGivesUpOnATeardownThatBlocks(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	start := time.Now()
	finished := disposeWithin(50*time.Millisecond, func() { <-release })
	waited := time.Since(start)

	if finished {
		t.Fatal("expected a teardown that never returns to be reported as unfinished")
	}
	if waited > 2*time.Second {
		t.Fatalf("expected the wait to be bounded by the timeout, waited %s", waited)
	}
}
