package stream

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPublishFPSWritesTheFirstValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "now_fps")
	f := &FrameRateCounter{}

	if !f.publishFPS(path, 25) {
		t.Fatal("expected the first value to be written")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected the file to exist: %s", err)
	}

	if string(data) != "25" {
		t.Fatalf("expected 25, got %q", data)
	}
}

func TestPublishFPSSkipsTheWriteWhenNothingChanged(t *testing.T) {
	// The counter ticks every 3 seconds forever. Rewriting an unchanged value
	// is ~28k pointless writes a day to the SD card the device boots from.
	path := filepath.Join(t.TempDir(), "now_fps")
	f := &FrameRateCounter{}

	f.publishFPS(path, 0)

	if err := os.Remove(path); err != nil {
		t.Fatalf("failed to remove the file: %s", err)
	}

	if f.publishFPS(path, 0) {
		t.Fatal("expected an unchanged value to skip the write")
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("expected no write for an unchanged value")
	}
}

func TestPublishFPSWritesWhenTheValueChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "now_fps")
	f := &FrameRateCounter{}

	f.publishFPS(path, 25)

	if !f.publishFPS(path, 30) {
		t.Fatal("expected a changed value to be written")
	}

	data, _ := os.ReadFile(path)
	if string(data) != "30" {
		t.Fatalf("expected 30, got %q", data)
	}
}

func TestPublishFPSRetriesAfterAFailedWrite(t *testing.T) {
	// A failed write must not be remembered as published, otherwise a
	// transient error silences the file until the value happens to change.
	dir := t.TempDir()
	path := filepath.Join(dir, "missing", "now_fps")
	f := &FrameRateCounter{}

	if f.publishFPS(path, 25) {
		t.Fatal("expected the write to fail")
	}

	if err := os.Mkdir(filepath.Join(dir, "missing"), 0o755); err != nil {
		t.Fatalf("failed to create the directory: %s", err)
	}

	if !f.publishFPS(path, 25) {
		t.Fatal("expected the same value to be retried after a failed write")
	}
}
