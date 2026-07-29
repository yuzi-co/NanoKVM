package application

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

	originalPath := versionFile
	t.Cleanup(func() {
		versionFile = originalPath
	})

	path := filepath.Join(t.TempDir(), "version")
	if contents != "" {
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatalf("failed to write version file: %s", err)
		}
	}

	versionFile = path
}

func useBuildStamp(t *testing.T, stamp string) {
	t.Helper()

	original := buildversion.Build
	t.Cleanup(func() {
		buildversion.Build = original
	})

	buildversion.Build = stamp
}

// The update page shows this value, so it is where an operator looks to confirm
// which binary is installed.
func TestCurrentVersionStampsTheVersionOnDisk(t *testing.T) {
	useVersionFile(t, "2.4.3\n")
	useBuildStamp(t, "test.stamp")

	want := "2.4.3+test.stamp"
	if got := currentVersion(); got != want {
		t.Fatalf("currentVersion() = %q, want %q", got, want)
	}
}

func TestCurrentVersionStampsTheFallbackVersion(t *testing.T) {
	useVersionFile(t, "")
	useBuildStamp(t, "test.stamp")

	want := "1.0.0+test.stamp"
	if got := currentVersion(); got != want {
		t.Fatalf("currentVersion() = %q, want %q", got, want)
	}
}

// A release build carries no stamp, and the reported version has to stay
// exactly what the updater wrote — the update page compares it against the
// release feed with semver.
func TestCurrentVersionIsUnstampedInAReleaseBuild(t *testing.T) {
	useVersionFile(t, "2.4.3\n")
	useBuildStamp(t, "")

	if got := currentVersion(); got != "2.4.3" {
		t.Fatalf("currentVersion() = %q, want %q", got, "2.4.3")
	}
}
