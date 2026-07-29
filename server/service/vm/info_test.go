package vm

import (
	"os"
	"path/filepath"
	"testing"

	buildversion "NanoKVM-Server/common/version"
)

// useVersionFile writes a version file for one test and points the reader at
// it. Passing an empty string leaves the path missing, which is the branch that
// falls back to the compiled-in default.
func useVersionFile(t *testing.T, contents string) {
	t.Helper()

	originalPath := applicationVersionFile
	t.Cleanup(func() {
		applicationVersionFile = originalPath
	})

	path := filepath.Join(t.TempDir(), "version")
	if contents != "" {
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatalf("failed to write version file: %s", err)
		}
	}

	applicationVersionFile = path
}

func useBuildStamp(t *testing.T, stamp string) {
	t.Helper()

	original := buildversion.Build
	t.Cleanup(func() {
		buildversion.Build = original
	})

	buildversion.Build = stamp
}

// The application version is the only thing that tells an operator which server
// binary is running, so the reported value has to carry the build stamp rather
// than the raw contents of the version file.
func TestGetApplicationVersionStampsTheVersionOnDisk(t *testing.T) {
	useVersionFile(t, "2.4.3\n")
	useBuildStamp(t, "test.stamp")

	want := "2.4.3+test.stamp"
	if got := getApplicationVersion(); got != want {
		t.Fatalf("getApplicationVersion() = %q, want %q", got, want)
	}
}

func TestGetApplicationVersionStampsTheFallbackVersion(t *testing.T) {
	useVersionFile(t, "")
	useBuildStamp(t, "test.stamp")

	want := "1.0.0+test.stamp"
	if got := getApplicationVersion(); got != want {
		t.Fatalf("getApplicationVersion() = %q, want %q", got, want)
	}
}

// A release build carries no stamp, and the reported version has to stay
// exactly what the updater wrote.
func TestGetApplicationVersionIsUnstampedInAReleaseBuild(t *testing.T) {
	useVersionFile(t, "2.4.3\n")
	useBuildStamp(t, "")

	if got := getApplicationVersion(); got != "2.4.3" {
		t.Fatalf("getApplicationVersion() = %q, want %q", got, "2.4.3")
	}
}
