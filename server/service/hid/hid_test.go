package hid

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	log "github.com/sirupsen/logrus"
)

func TestDeviceHandlesAreSharedWithHid(t *testing.T) {
	h := &Hid{}

	file, err := os.Create(filepath.Join(t.TempDir(), "hidg0"))
	if err != nil {
		t.Fatalf("failed to create the file: %s", err)
	}
	defer file.Close()

	device := h.keyboardDevice(HID0)
	device.set(file)

	if h.g0 != file {
		t.Fatal("expected the device to write through to the Hid handle")
	}

	if device.get() != file {
		t.Fatal("expected the device to read back the Hid handle")
	}
}

func TestEachDeviceTargetsItsOwnHandle(t *testing.T) {
	h := &Hid{}

	file, err := os.Create(filepath.Join(t.TempDir(), "hidg1"))
	if err != nil {
		t.Fatalf("failed to create the file: %s", err)
	}
	defer file.Close()

	h.relativeMouseDevice(HID1).set(file)

	if h.g1 != file {
		t.Fatal("expected the relative mouse to target g1")
	}
	if h.g0 != nil || h.g2 != nil {
		t.Fatal("expected the other handles to be untouched")
	}

	h.absoluteMouseDevice(HID2).set(file)
	if h.g2 != file {
		t.Fatal("expected the absolute mouse to target g2")
	}
}

// deviceSink forces the device to escape, the way it does when it is handed to
// a real, non-inlined write. Measuring an inlined call would prove nothing.
var deviceSink hidDevice

func TestBuildingADeviceDoesNotAllocate(t *testing.T) {
	// One of these is built for every HID report. A moving mouse produces
	// hundreds a second, so this sits on the hottest path in the server.
	h := &Hid{}

	allocs := testing.AllocsPerRun(200, func() {
		deviceSink = h.relativeMouseDevice(HID1)
	})

	if deviceSink.path != HID1 {
		t.Fatalf("expected the device to be built, got path %q", deviceSink.path)
	}

	if allocs != 0 {
		t.Fatalf("expected building a device to allocate nothing, got %v allocations", allocs)
	}
}

func TestTracingAWriteCostsNothingWhenDebugIsOff(t *testing.T) {
	// logrus skips the formatting when the level is off, but the variadic call
	// still builds an []interface{} and boxes every argument. That is garbage
	// produced once per HID report on a device with 256MB of RAM.
	previous := log.GetLevel()
	log.SetLevel(log.ErrorLevel)
	t.Cleanup(func() { log.SetLevel(previous) })

	report := []byte{0, 0, 0, 4, 0, 0, 0, 0}

	allocs := testing.AllocsPerRun(200, func() {
		traceHIDWrite(HID0, report)
	})

	if allocs != 0 {
		t.Fatalf("expected no allocations while debug logging is off, got %v", allocs)
	}
}

func TestReportLengthValidation(t *testing.T) {
	h := &Hid{}
	if err := h.WriteKeyboardReport(make([]byte, 7)); err == nil {
		t.Fatal("expected keyboard length error")
	}
	if err := h.WriteRelativeMouseReport(make([]byte, 5)); err == nil {
		t.Fatal("expected relative mouse length error")
	}
	if err := h.WriteAbsoluteMouseReport(make([]byte, 7)); err == nil {
		t.Fatal("expected absolute mouse length error")
	}
}

func TestPasteDurationLeavesModeSwitchMargin(t *testing.T) {
	if maxPasteDuration >= 30*time.Second {
		t.Fatalf("maxPasteDuration = %s, want below 30s mode switch wait budget", maxPasteDuration)
	}
	if got := time.Duration(maxPasteContentRunes) * defaultPasteDelay; got > maxPasteDuration {
		t.Fatalf("max paste content duration = %s, want <= %s", got, maxPasteDuration)
	}
}
