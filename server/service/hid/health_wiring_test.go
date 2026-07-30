package hid

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"NanoKVM-Server/proto"

	log "github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
)

// stallWrites makes every report fail the way a HID endpoint the target is not
// fetching from does: the descriptor is open and healthy, and the write runs out
// of time. An ordinary file cannot be made to do that, and a pipe only does it
// while the runtime keeps the descriptor pollable, which it does not promise.
func stallWrites(t *testing.T) {
	t.Helper()

	restore := writeReport
	t.Cleanup(func() { writeReport = restore })

	writeReport = func(_ string, _ *os.File, _ []byte, timeout time.Duration) error {
		return fmt.Errorf("write timed out: %w", os.ErrDeadlineExceeded)
	}
}

// openDevice hands the device a real descriptor so writeHID gets past its own
// checks and reaches the write.
func openDevice(t *testing.T, device hidDevice) {
	t.Helper()

	file, err := os.Create(filepath.Join(t.TempDir(), filepath.Base(device.path)))
	if err != nil {
		t.Fatalf("failed to create the stand-in device node: %s", err)
	}
	t.Cleanup(func() { _ = file.Close() })

	device.set(file)
}

func statusFor(t *testing.T, h *Hid, name string) proto.HidDeviceStatus {
	t.Helper()

	for _, device := range h.Status() {
		if device.Name == name {
			return device
		}
	}

	t.Fatalf("no status reported for %q", name)
	return proto.HidDeviceStatus{}
}

func TestStatusNamesEveryEndpoint(t *testing.T) {
	h := &Hid{}

	got := h.Status()
	if len(got) != 3 {
		t.Fatalf("reported %d endpoints, want 3", len(got))
	}

	want := map[string]string{
		NameKeyboard:      HID0,
		NameRelativeMouse: HID1,
		NameAbsoluteMouse: HID2,
	}
	for _, device := range got {
		path, ok := want[device.Name]
		if !ok {
			t.Fatalf("unexpected endpoint %q", device.Name)
		}
		if device.Path != path {
			t.Fatalf("%s reported path %q, want %q", device.Name, device.Path, path)
		}
		if device.State != hidStateUnknown {
			t.Fatalf("%s starts as %q, want %q", device.Name, device.State, hidStateUnknown)
		}
	}
}

func TestAStalledWriteIsVisibleInStatus(t *testing.T) {
	stallWrites(t)

	h := &Hid{}
	device := h.absoluteMouseDevice(HID2)
	openDevice(t, device)

	if err := h.writeHID(device, make([]byte, 6)); err == nil {
		t.Fatal("expected the write to a stalled endpoint to fail")
	}

	if got := statusFor(t, h, NameAbsoluteMouse).State; got != hidStateStalled {
		t.Fatalf("state = %q, want %q", got, hidStateStalled)
	}

	// The endpoint that was never written to must not be tarred with it: the
	// whole value of the report is telling the operator which one still works.
	if got := statusFor(t, h, NameRelativeMouse).State; got != hidStateUnknown {
		t.Fatalf("the relative mouse reported %q, want %q", got, hidStateUnknown)
	}
}

// The reason this exists. A moving mouse produced two log lines per report and
// about twenty a second, all identical after the first.
func TestAStalledEndpointIsLoggedOncePerStallNotPerReport(t *testing.T) {
	stallWrites(t)
	hook := test.NewGlobal()
	defer hook.Reset()

	h := &Hid{}
	device := h.absoluteMouseDevice(HID2)

	// writeHID drops the handle on failure, so each pass needs a fresh one -
	// the same way the real path reopens the device node.
	for i := 0; i < 20; i++ {
		openDevice(t, device)
		if err := h.writeHID(device, make([]byte, 6)); err == nil {
			t.Fatalf("write %d unexpectedly succeeded", i)
		}
	}

	if got := countEntries(hook, log.ErrorLevel, HID2); got != 1 {
		t.Fatalf("logged %d errors for %s across 20 failed writes, want 1", got, HID2)
	}
}

// The message has to name the endpoint and say what the operator can do, because
// nothing else in the system will: the device node is present and the gadget is
// bound, so every other signal says healthy.
func TestTheStallMessageNamesTheEndpoint(t *testing.T) {
	stallWrites(t)
	hook := test.NewGlobal()
	defer hook.Reset()

	h := &Hid{}
	device := h.absoluteMouseDevice(HID2)
	openDevice(t, device)
	_ = h.writeHID(device, make([]byte, 6))

	entry := hook.LastEntry()
	if entry == nil {
		t.Fatal("a stalled endpoint logged nothing")
	}
	if !strings.Contains(entry.Message, HID2) {
		t.Fatalf("the message does not name the endpoint: %q", entry.Message)
	}
	if !strings.Contains(entry.Message, "not fetching") {
		t.Fatalf("the message does not say the target is not fetching: %q", entry.Message)
	}
}

func TestRecoveryIsLoggedOnce(t *testing.T) {
	stallWrites(t)
	hook := test.NewGlobal()
	defer hook.Reset()

	h := &Hid{}
	device := h.absoluteMouseDevice(HID2)

	openDevice(t, device)
	if err := h.writeHID(device, make([]byte, 6)); err == nil {
		t.Fatal("expected the first write to fail")
	}

	writeReport = func(_ string, _ *os.File, _ []byte, _ time.Duration) error { return nil }

	for i := 0; i < 3; i++ {
		openDevice(t, device)
		if err := h.writeHID(device, make([]byte, 6)); err != nil {
			t.Fatalf("write %d failed: %s", i, err)
		}
	}

	if got := countEntries(hook, log.InfoLevel, HID2); got != 1 {
		t.Fatalf("logged %d recoveries for %s across 3 good writes, want 1", got, HID2)
	}
	if got := statusFor(t, h, NameAbsoluteMouse).State; got != hidStateAccepting {
		t.Fatalf("state = %q, want %q", got, hidStateAccepting)
	}
}

// Three endpoints that have always worked must not announce themselves at boot.
func TestAFirstSuccessfulWriteIsNotLoggedAsARecovery(t *testing.T) {
	restore := writeReport
	t.Cleanup(func() { writeReport = restore })
	writeReport = func(_ string, _ *os.File, _ []byte, _ time.Duration) error { return nil }

	hook := test.NewGlobal()
	defer hook.Reset()

	h := &Hid{}
	device := h.keyboardDevice(HID0)
	openDevice(t, device)

	if err := h.writeHID(device, make([]byte, 8)); err != nil {
		t.Fatalf("write failed: %s", err)
	}

	if got := len(hook.AllEntries()); got != 0 {
		t.Fatalf("a healthy first write logged %d entries, want 0", got)
	}
}

// The call sites keep their own messages - which write failed is worth knowing -
// so the repeats have to be marked on the error rather than silenced centrally.
func TestRepeatedFailuresAreMarked(t *testing.T) {
	stallWrites(t)

	h := &Hid{}
	device := h.absoluteMouseDevice(HID2)

	openDevice(t, device)
	first := h.writeHID(device, make([]byte, 6))
	if first == nil {
		t.Fatal("expected the first write to fail")
	}
	if errors.Is(first, errRepeatedFailure) {
		t.Fatal("the first failure must not be marked as a repeat; nothing has reported it yet")
	}

	openDevice(t, device)
	second := h.writeHID(device, make([]byte, 6))
	if second == nil {
		t.Fatal("expected the second write to fail")
	}
	if !errors.Is(second, errRepeatedFailure) {
		t.Fatal("the second identical failure should be marked as a repeat")
	}

	// Marking must not hide what went wrong from anyone reading the error.
	if !errors.Is(second, os.ErrDeadlineExceeded) {
		t.Fatalf("the marked error lost its cause: %s", second)
	}
}

func TestReportWriteFailureDropsMarkedRepeats(t *testing.T) {
	hook := test.NewGlobal()
	defer hook.Reset()

	reportWriteFailure("first", errors.New("boom"))
	reportWriteFailure("repeat", fmt.Errorf("%w: %w", errRepeatedFailure, errors.New("boom")))

	if got := len(hook.AllEntries()); got != 1 {
		t.Fatalf("logged %d entries, want 1", got)
	}
	if got := hook.LastEntry().Message; !strings.Contains(got, "first") {
		t.Fatalf("logged %q, want the unmarked failure", got)
	}
}

func countEntries(hook *test.Hook, level log.Level, substring string) int {
	count := 0
	for _, entry := range hook.AllEntries() {
		if entry.Level == level && strings.Contains(entry.Message, substring) {
			count++
		}
	}
	return count
}
