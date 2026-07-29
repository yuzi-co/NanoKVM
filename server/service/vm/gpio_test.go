package vm

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestPressDurationDefaultsWhenUnset(t *testing.T) {
	if got := pressDuration(0); got != defaultPressDuration {
		t.Fatalf("pressDuration(0) = %s, want %s", got, defaultPressDuration)
	}
}

func TestPressDurationKeepsValueInRange(t *testing.T) {
	if got := pressDuration(1500); got != 1500*time.Millisecond {
		t.Fatalf("pressDuration(1500) = %s, want 1.5s", got)
	}
}

// A caller that asks for a press longer than a person could hold the button is
// asking to leave the ATX line asserted indefinitely.
func TestPressDurationIsCapped(t *testing.T) {
	if got := pressDuration(2_000_000_000); got != maxPressDuration {
		t.Fatalf("pressDuration(2e9) = %s, want %s", got, maxPressDuration)
	}
}

// Two presses of the same line must not overlap: one releasing the line while
// the other still believes it is holding it turns a reset into a no-op, or
// leaves the line asserted after both have returned.
func TestWriteGpioSerializesPressesOnTheSameDevice(t *testing.T) {
	device := filepath.Join(t.TempDir(), "power")
	if err := os.WriteFile(device, []byte("0"), 0o600); err != nil {
		t.Fatalf("failed to seed device: %s", err)
	}

	const press = 150 * time.Millisecond

	start := time.Now()

	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := writeGpio(device, press); err != nil {
				t.Errorf("writeGpio failed: %s", err)
			}
		}()
	}
	wg.Wait()

	if elapsed := time.Since(start); elapsed < 2*press {
		t.Fatalf("two presses took %s, want at least %s: they overlapped", elapsed, 2*press)
	}

	content, err := os.ReadFile(device)
	if err != nil {
		t.Fatalf("failed to read device: %s", err)
	}
	if string(content) != "0" {
		t.Fatalf("device left at %q, want %q", content, "0")
	}
}

// Different lines are independent, so pressing power must not wait on reset.
func TestWriteGpioDoesNotSerializeAcrossDevices(t *testing.T) {
	dir := t.TempDir()
	power := filepath.Join(dir, "power")
	reset := filepath.Join(dir, "reset")

	const press = 300 * time.Millisecond

	start := time.Now()

	var wg sync.WaitGroup
	for _, device := range []string{power, reset} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := writeGpio(device, press); err != nil {
				t.Errorf("writeGpio failed: %s", err)
			}
		}()
	}
	wg.Wait()

	if elapsed := time.Since(start); elapsed >= 2*press {
		t.Fatalf("two lines took %s, want less than %s: they were serialized", elapsed, 2*press)
	}
}
