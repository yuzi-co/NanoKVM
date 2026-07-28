//go:build linux

package hid

import (
	"os"
	"path/filepath"
	"testing"
)

func openThenRemove(t *testing.T) *os.File {
	t.Helper()

	path := filepath.Join(t.TempDir(), "hidg0")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("setup: %s", err)
	}
	t.Cleanup(func() { _ = file.Close() })

	if err := os.Remove(path); err != nil {
		t.Fatalf("setup: %s", err)
	}

	return file
}

func TestADeletedDeviceNodeIsNoticed(t *testing.T) {
	// Rebuilding the USB gadget - mounting an image, switching HID mode -
	// removes /dev/hidg*. The old handle keeps accepting writes and they go
	// nowhere, so keyboard and mouse die silently with nothing in the log.
	if !hidFileWasDeleted(openThenRemove(t)) {
		t.Fatal("expected a deleted device node to be detected")
	}
}

func TestALiveDeviceNodeIsLeftAlone(t *testing.T) {
	file, err := os.Create(filepath.Join(t.TempDir(), "hidg0"))
	if err != nil {
		t.Fatalf("setup: %s", err)
	}
	defer file.Close()

	if hidFileWasDeleted(file) {
		t.Fatal("a live handle must not be treated as stale")
	}
}

func TestWritingToADeletedNodeReopensIt(t *testing.T) {
	// The point of noticing: the report still has to arrive.
	h := &Hid{}
	path := filepath.Join(t.TempDir(), "hidg0")

	// The gadget rebuild removes the node and creates a new one at the same
	// path; the handle we are holding still points at the old inode.
	stale, err := os.Create(path)
	if err != nil {
		t.Fatalf("setup: %s", err)
	}
	defer stale.Close()

	if err := os.Remove(path); err != nil {
		t.Fatalf("setup: %s", err)
	}

	fresh, err := os.Create(path)
	if err != nil {
		t.Fatalf("setup: %s", err)
	}
	_ = fresh.Close()

	h.g0 = stale

	device := h.keyboardDevice(path)
	if err := h.writeHID(device, []byte{1, 2, 3}); err != nil {
		t.Fatalf("expected the write to succeed: %s", err)
	}

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected the device to be recreated and written: %s", err)
	}

	if string(written) != string([]byte{1, 2, 3}) {
		t.Fatalf("device holds %v, want the report", written)
	}
}
