package jiggler

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// newJiggler returns a jiggler that is enabled, points its config file
// somewhere writable, and moves nothing. The package keeps its state in a
// singleton, so tests build their own value rather than going through
// GetJiggler.
func newJiggler(t *testing.T) *Jiggler {
	t.Helper()

	restoreConfig := ConfigFile
	t.Cleanup(func() { ConfigFile = restoreConfig })
	ConfigFile = filepath.Join(t.TempDir(), "mouse-jiggler")

	j := &Jiggler{
		enabled:     true,
		mode:        "relative",
		lastUpdated: time.Now(),
		interval:    time.Millisecond,
		move:        func(string) {},
	}
	t.Cleanup(j.stop)

	return j
}

// Update runs on the websocket read loop, once per HID event, while the
// jiggler's own goroutine reads the same field to decide whether the target has
// gone idle. Nothing guarded either side, and time.Time is three words, so the
// loop could read a value that was never written.
//
// This test is only meaningful under -race.
func TestJigglerSurvivesUpdatesWhileItsLoopRuns(t *testing.T) {
	j := newJiggler(t)
	j.Run()

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 500 {
				j.Update()
			}
		}()
	}

	wg.Wait()
}

// The HTTP handlers read the mode and the enabled flag while the loop and the
// websocket are running.
func TestJigglerAccessorsAreSafeWhileItsLoopRuns(t *testing.T) {
	j := newJiggler(t)
	j.Run()

	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 500 {
				j.IsEnabled()
				j.GetMode()
				j.Update()
			}
		}()
	}

	wg.Wait()
}

// Run is called at boot and again by every Enable. It checked a flag and then
// set it without holding anything in between, so two callers could both find it
// clear and both start a loop. Two loops move the mouse twice as often as asked
// and neither can be stopped by the other.
func TestJigglerRunStartsOneLoopOnly(t *testing.T) {
	j := newJiggler(t)

	started := 0
	var mutex sync.Mutex
	var wg sync.WaitGroup

	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if j.Run() {
				mutex.Lock()
				started++
				mutex.Unlock()
			}
		}()
	}
	wg.Wait()

	if started != 1 {
		t.Fatalf("expected exactly one loop to start, got %d", started)
	}
}

// A jiggler nobody enabled must not start a loop.
func TestJigglerDoesNotRunWhenDisabled(t *testing.T) {
	j := newJiggler(t)
	j.enabled = false

	if j.Run() {
		t.Fatal("expected a disabled jiggler to start no loop")
	}
}

// Enable and Disable are HTTP handlers, so they race the loop and the websocket
// like everything else.
func TestJigglerEnableAndDisableAreSafeWhileItsLoopRuns(t *testing.T) {
	j := newJiggler(t)
	j.Run()

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 200 {
			_ = j.Enable("absolute")
			_ = j.Disable()
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 200 {
			j.Update()
			j.IsEnabled()
			j.GetMode()
		}
	}()

	wg.Wait()
}

// Disable removes the config file, and the file is what GetJiggler reads at
// startup to decide whether the jiggler was on.
func TestJigglerDisableClearsTheConfigFile(t *testing.T) {
	j := newJiggler(t)

	if err := j.Enable("absolute"); err != nil {
		t.Fatalf("failed to enable: %s", err)
	}
	if _, err := os.Stat(ConfigFile); err != nil {
		t.Fatalf("expected the config file to exist: %s", err)
	}

	if err := j.Disable(); err != nil {
		t.Fatalf("failed to disable: %s", err)
	}
	if _, err := os.Stat(ConfigFile); !os.IsNotExist(err) {
		t.Fatalf("expected the config file to be gone, got %v", err)
	}
	if j.IsEnabled() {
		t.Fatal("expected the jiggler to report itself disabled")
	}
}
