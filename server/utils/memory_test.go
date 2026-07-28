package utils

import (
	"math"
	"os"
	"path/filepath"
	"runtime/debug"
	"testing"
)

// currentMemoryLimit reads the live limit without changing it.
func currentMemoryLimit() int64 {
	return debug.SetMemoryLimit(-1)
}

func withTempLimitFile(t *testing.T) {
	t.Helper()

	original := GoMemLimitFile
	originalLimit := currentMemoryLimit()

	GoMemLimitFile = filepath.Join(t.TempDir(), "GOMEMLIMIT")

	t.Cleanup(func() {
		GoMemLimitFile = original
		debug.SetMemoryLimit(originalLimit)
	})
}

func TestInitGoMemLimitAppliesTheSavedLimit(t *testing.T) {
	withTempLimitFile(t)

	if err := os.WriteFile(GoMemLimitFile, []byte("80"), 0o644); err != nil {
		t.Fatalf("failed to seed the limit file: %s", err)
	}

	InitGoMemLimit()

	want := int64(80 * 1024 * 1024)
	if got := currentMemoryLimit(); got != want {
		t.Fatalf("expected the saved limit to be applied at startup, got %d want %d", got, want)
	}
}

func TestInitGoMemLimitLeavesTheLimitAloneWithoutAFile(t *testing.T) {
	withTempLimitFile(t)

	before := currentMemoryLimit()

	InitGoMemLimit()

	if got := currentMemoryLimit(); got != before {
		t.Fatalf("expected the limit to be untouched, got %d want %d", got, before)
	}
}

func TestDelGoMemLimitRemovesTheCapEntirely(t *testing.T) {
	withTempLimitFile(t)

	if err := SetGoMemLimit(80); err != nil {
		t.Fatalf("failed to set the limit: %s", err)
	}

	if err := DelGoMemLimit(); err != nil {
		t.Fatalf("failed to delete the limit: %s", err)
	}

	// A 1GB "off" value is a real cap on the 1GB boards.
	if got := currentMemoryLimit(); got != math.MaxInt64 {
		t.Fatalf("expected the cap to be removed, got %d", got)
	}
}
