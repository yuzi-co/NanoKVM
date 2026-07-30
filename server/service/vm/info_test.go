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

// useImageFile writes a `ver` file for one test and points the reader at it.
func useImageFile(t *testing.T, contents string) {
	t.Helper()

	originalPath := imageVersionFile
	t.Cleanup(func() {
		imageVersionFile = originalPath
	})

	path := filepath.Join(t.TempDir(), "ver")
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("failed to write ver file: %s", err)
	}

	imageVersionFile = path
}

// Every released image needs a map entry. Without one the UI shows the raw
// file name, which reads as a fault rather than as a version.
func TestGetImageVersionNamesEveryReleasedImage(t *testing.T) {
	releases := map[string]string{
		"2025-02-17-19-08-3649fe.img": "v1.4.0",
		"2025-04-17-14-21-98d17d.img": "v1.4.1",
		"2026-01-05-1_4_1.img":        "v1.4.2",
		"2026-06-10-1_4_3.img":        "v1.4.3",
	}

	for file, want := range releases {
		useImageFile(t, file+"\n")
		if got := getImageVersion(); got != want {
			t.Errorf("getImageVersion() = %q for %s, want %q", got, file, want)
		}
	}
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
