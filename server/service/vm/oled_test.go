package vm

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteSleepSettingStoresAValueTheFirmwareCanActOn(t *testing.T) {
	// The file is what kvm_system reads, so the clamp has to survive the
	// round trip -- not just live in a helper nobody calls.
	path := filepath.Join(t.TempDir(), "oled_sleep")

	if err := writeSleepSetting(path, 5); err != nil {
		t.Fatalf("expected the setting to be written: %s", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected the setting to be readable: %s", err)
	}

	if string(data) != "10" {
		t.Fatalf("stored %q, want \"10\"", data)
	}
}

func TestClampSleepSecondsRaisesDurationsTheFirmwareIgnores(t *testing.T) {
	// kvm_system treats anything below OLED_SLEEP_DELAY_MIN as "never sleep",
	// so asking for 5 seconds used to turn the screen saver off entirely --
	// the opposite of what was requested.
	for _, seconds := range []int{1, 5, 9} {
		if got := clampSleepSeconds(seconds); got != minSleepSeconds {
			t.Fatalf("clampSleepSeconds(%d) = %d, want %d", seconds, got, minSleepSeconds)
		}
	}
}

func TestClampSleepSecondsKeepsNeverAsNever(t *testing.T) {
	// Zero is the one deliberate way to say "keep the screen on".
	if got := clampSleepSeconds(0); got != 0 {
		t.Fatalf("clampSleepSeconds(0) = %d, want 0", got)
	}
}

func TestClampSleepSecondsKeepsSupportedDurations(t *testing.T) {
	// Every duration the web UI offers, plus the boundary itself.
	for _, seconds := range []int{10, 15, 30, 60, 180, 300, 600, 1800, 3600} {
		if got := clampSleepSeconds(seconds); got != seconds {
			t.Fatalf("clampSleepSeconds(%d) = %d, want it unchanged", seconds, got)
		}
	}
}

func TestClampSleepSecondsLowersDurationsTheFirmwareCannotHold(t *testing.T) {
	// The firmware parses the file into a uint16_t, so 65536 wraps to 0 and
	// silently means "never" again.
	for _, seconds := range []int{65536, 100000} {
		if got := clampSleepSeconds(seconds); got != maxSleepSeconds {
			t.Fatalf("clampSleepSeconds(%d) = %d, want %d", seconds, got, maxSleepSeconds)
		}
	}
}

func TestClampSleepSecondsTreatsNegativeAsNever(t *testing.T) {
	// A negative delay has no sensible reading; keep the screen on rather
	// than inventing a duration.
	if got := clampSleepSeconds(-5); got != 0 {
		t.Fatalf("clampSleepSeconds(-5) = %d, want 0", got)
	}
}
