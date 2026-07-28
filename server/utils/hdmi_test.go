package utils

import (
	"os"
	"path/filepath"
	"testing"
)

func signalFile(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "state")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("setup: %s", err)
	}

	return path
}

func TestReadHDMISignalSeesAnActivePort(t *testing.T) {
	// The capture library writes 1 the moment it starts getting frames.
	if !readHDMISignal(signalFile(t, "1\n")) {
		t.Fatal("expected an active signal to be reported")
	}
}

func TestReadHDMISignalSeesASleepingHost(t *testing.T) {
	if readHDMISignal(signalFile(t, "0\n")) {
		t.Fatal("expected no signal to be reported")
	}
}

func TestReadHDMISignalReportsNoSignalWhenTheFileIsMissing(t *testing.T) {
	// Nothing has captured yet, or this build never writes the file. Callers
	// are polling to find out whether a machine is awake, so the safe answer
	// is that it is not.
	if readHDMISignal(filepath.Join(t.TempDir(), "absent")) {
		t.Fatal("expected a missing file to read as no signal")
	}
}

func TestReadHDMISignalIgnoresContentItDoesNotUnderstand(t *testing.T) {
	for _, body := range []string{"", "\n", "yes", "2"} {
		if readHDMISignal(signalFile(t, body)) {
			t.Fatalf("expected %q to read as no signal", body)
		}
	}
}
