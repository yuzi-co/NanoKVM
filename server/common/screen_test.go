package common

import (
	"sync"
	"testing"
)

// The streamer goroutines read the screen parameters on every frame while an
// HTTP handler can write them at any moment. Run with -race.
func TestScreenIsSafeForConcurrentAccess(t *testing.T) {
	screen := GetScreen()

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			SetScreen("fps", 30)
			SetScreen("resolution", 720)
			SetScreen("quality", 60)
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			values := screen.Snapshot()
			if values.FPS <= 0 {
				t.Errorf("fps should stay positive, got %d", values.FPS)
			}
		}
	}()

	wg.Wait()
}

func TestSnapshotReflectsUpdates(t *testing.T) {
	screen := GetScreen()

	SetScreen("resolution", 1080)

	values := screen.Snapshot()
	if values.Width != 1920 || values.Height != 1080 {
		t.Fatalf("expected 1920x1080, got %dx%d", values.Width, values.Height)
	}
}

func TestCheckScreenRepairsInvalidValues(t *testing.T) {
	SetScreen("quality", 60)
	SetScreen("resolution", 720)

	CheckScreen()

	values := GetScreen().Snapshot()
	if values.Quality != 60 || values.Height != 720 {
		t.Fatalf("valid values should survive CheckScreen, got %+v", values)
	}
}
