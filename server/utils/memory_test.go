package utils

import (
	"math"
	"os"
	"path/filepath"
	"runtime/debug"
	"testing"
)

const mib = 1024 * 1024

// useTempLimitFile points the limit file somewhere writable and puts the
// process memory limit back afterwards. The limit is runtime-wide, so a test
// that left it set would follow the rest of the suite around.
func useTempLimitFile(t *testing.T, contents string) {
	t.Helper()

	originalFile := GoMemLimitFile
	originalLimit := debug.SetMemoryLimit(-1)
	t.Cleanup(func() {
		GoMemLimitFile = originalFile
		debug.SetMemoryLimit(originalLimit)
	})

	GoMemLimitFile = filepath.Join(t.TempDir(), "GOMEMLIMIT")

	if contents != "" {
		if err := os.WriteFile(GoMemLimitFile, []byte(contents), 0o644); err != nil {
			t.Fatalf("setup: %s", err)
		}
	}
}

func TestInitGoMemLimitAppliesTheStoredLimit(t *testing.T) {
	// The limit was applied only to the process that set it, and nothing read
	// the file back, so it was gone after every reboot.
	useTempLimitFile(t, "128")
	debug.SetMemoryLimit(math.MaxInt64)

	InitGoMemLimit()

	if got := debug.SetMemoryLimit(-1); got != 128*mib {
		t.Fatalf("limit is %d, want %d", got, 128*mib)
	}
}

func TestInitGoMemLimitDoesNothingWithoutAFile(t *testing.T) {
	useTempLimitFile(t, "")
	debug.SetMemoryLimit(math.MaxInt64)

	InitGoMemLimit()

	if got := debug.SetMemoryLimit(-1); got != math.MaxInt64 {
		t.Fatalf("limit is %d, want it untouched at %d", got, int64(math.MaxInt64))
	}
}

func TestInitGoMemLimitAppliesTheSameFloorAsSet(t *testing.T) {
	// SetGoMemLimit clamps to the floor but writes the value it was given, so
	// a stored 10 has already been applied as 50. Restoring it unclamped would
	// hand the runtime a limit its own setter refused, and only after a reboot.
	useTempLimitFile(t, "10")
	debug.SetMemoryLimit(math.MaxInt64)

	InitGoMemLimit()

	if got := debug.SetMemoryLimit(-1); got != minGoMemLimitMB*mib {
		t.Fatalf("limit is %d, want the floor %d", got, minGoMemLimitMB*mib)
	}
}

func TestSetAndInitAgreeOnWhatWasApplied(t *testing.T) {
	// Whatever SetGoMemLimit applied now is what a restart must apply again.
	useTempLimitFile(t, "")

	if err := SetGoMemLimit(10); err != nil {
		t.Fatalf("failed to set limit: %s", err)
	}
	afterSet := debug.SetMemoryLimit(-1)

	debug.SetMemoryLimit(math.MaxInt64)
	InitGoMemLimit()
	afterRestart := debug.SetMemoryLimit(-1)

	if afterSet != afterRestart {
		t.Fatalf("set applied %d but a restart applies %d", afterSet, afterRestart)
	}
}

func TestDelGoMemLimitRemovesTheLimitRatherThanCappingAtOneGigabyte(t *testing.T) {
	// A literal 1GB is a real cap on the 1GB boards, which is the opposite of
	// turning the limit off.
	useTempLimitFile(t, "128")
	InitGoMemLimit()

	if err := DelGoMemLimit(); err != nil {
		t.Fatalf("failed to delete limit: %s", err)
	}

	if got := debug.SetMemoryLimit(-1); got != math.MaxInt64 {
		t.Fatalf("limit is %d, want no limit (%d)", got, int64(math.MaxInt64))
	}
	if IsGoMemLimitExist() {
		t.Fatal("the limit file should be gone")
	}
}
