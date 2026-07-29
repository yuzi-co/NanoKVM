package mjpeg

import (
	"sync"
	"testing"
	"time"
)

// recordFrameDetect swaps the hardware call for a recorder and returns the
// values it was handed, newest last.
func recordFrameDetect(t *testing.T) func() []uint8 {
	t.Helper()

	var (
		mutex  sync.Mutex
		values []uint8
	)

	original := setFrameDetect
	setFrameDetect = func(frames uint8) {
		mutex.Lock()
		defer mutex.Unlock()
		values = append(values, frames)
	}

	t.Cleanup(func() {
		setFrameDetect = original
		resetFrameDetectPause()
	})

	resetFrameDetectPause()

	return func() []uint8 {
		mutex.Lock()
		defer mutex.Unlock()

		return append([]uint8(nil), values...)
	}
}

func TestPauseDurationDefaultsWhenUnset(t *testing.T) {
	if got := pauseDuration(0); got != defaultPauseDuration {
		t.Fatalf("pauseDuration(0) = %s, want %s", got, defaultPauseDuration)
	}
}

func TestPauseDurationDefaultsWhenNegative(t *testing.T) {
	if got := pauseDuration(-5); got != defaultPauseDuration {
		t.Fatalf("pauseDuration(-5) = %s, want %s", got, defaultPauseDuration)
	}
}

// Detection is what notices the screen has changed. A caller must not be able
// to switch it off for a week.
func TestPauseDurationIsCapped(t *testing.T) {
	if got := pauseDuration(1_000_000_000); got != maxPauseDuration {
		t.Fatalf("pauseDuration(1e9) = %s, want %s", got, maxPauseDuration)
	}
}

func TestPauseFrameDetectStopsThenResumes(t *testing.T) {
	values := recordFrameDetect(t)

	pauseFrameDetect(80 * time.Millisecond)

	if got := values(); len(got) != 1 || got[0] != 0 {
		t.Fatalf("after pause, calls = %v, want [0]", got)
	}

	time.Sleep(200 * time.Millisecond)

	got := values()
	if len(got) != 2 || got[1] != FrameDetectInterval {
		t.Fatalf("after the pause elapsed, calls = %v, want [0 %d]", got, FrameDetectInterval)
	}
}

// A second, shorter request must not cut short a pause someone else is still
// relying on.
func TestShorterPauseDoesNotCutLongerOneShort(t *testing.T) {
	values := recordFrameDetect(t)

	pauseFrameDetect(400 * time.Millisecond)
	pauseFrameDetect(50 * time.Millisecond)

	time.Sleep(200 * time.Millisecond)

	if got := values(); len(got) != 1 {
		t.Fatalf("detection resumed early: calls = %v, want just [0]", got)
	}

	time.Sleep(400 * time.Millisecond)

	got := values()
	if len(got) != 2 || got[1] != FrameDetectInterval {
		t.Fatalf("detection never resumed: calls = %v", got)
	}
}
