package hid

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"NanoKVM-Server/proto"

	log "github.com/sirupsen/logrus"
)

type Hid struct {
	g0         *os.File
	g1         *os.File
	g2         *os.File
	kbMutex    sync.Mutex
	mouseMutex sync.Mutex

	// One health record per endpoint. They are separate because the failure
	// this reports is per-endpoint: a target can fetch keyboard and relative
	// mouse reports while ignoring the absolute mouse entirely.
	kbHealth  endpointHealth
	relHealth endpointHealth
	absHealth endpointHealth
}

const (
	HID0 = "/dev/hidg0" // Keyboard
	HID1 = "/dev/hidg1" // Mouse (Relative Mode)
	HID2 = "/dev/hidg2" // Touchpad (Absolute Mode)
)

const (
	hidWriteTimeout     = 50 * time.Millisecond
	hidWriteRetryDelay  = time.Millisecond
	hidReopenTimeout    = 2 * time.Second
	hidReopenRetryDelay = 100 * time.Millisecond
)

type hidWriter interface {
	Write([]byte) (int, error)
}

// hidDevice points at one of the Hid handles rather than closing over it. A
// device is built for every HID report, and a pair of closures would be two
// heap allocations on the hottest path in the server.
type hidDevice struct {
	path   string
	name   string
	mu     *sync.Mutex
	file   **os.File
	health *endpointHealth
}

func (d hidDevice) get() *os.File {
	return *d.file
}

func (d hidDevice) set(file *os.File) {
	*d.file = file
}

var (
	hid     *Hid
	hidOnce sync.Once
)

func GetHid() *Hid {
	hidOnce.Do(func() {
		hid = &Hid{}
	})
	return hid
}

func (h *Hid) Lock() {
	h.kbMutex.Lock()
	h.mouseMutex.Lock()
}

func (h *Hid) Unlock() {
	h.kbMutex.Unlock()
	h.mouseMutex.Unlock()
}

// The names are codes, not prose: the web UI translates them, and it needs a
// stable key to translate.
const (
	NameKeyboard      = "keyboard"
	NameRelativeMouse = "mouse-relative"
	NameAbsoluteMouse = "mouse-absolute"
)

func (h *Hid) keyboardDevice(path string) hidDevice {
	return hidDevice{path: path, name: NameKeyboard, mu: &h.kbMutex, file: &h.g0, health: &h.kbHealth}
}

func (h *Hid) relativeMouseDevice(path string) hidDevice {
	return hidDevice{path: path, name: NameRelativeMouse, mu: &h.mouseMutex, file: &h.g1, health: &h.relHealth}
}

func (h *Hid) absoluteMouseDevice(path string) hidDevice {
	return hidDevice{path: path, name: NameAbsoluteMouse, mu: &h.mouseMutex, file: &h.g2, health: &h.absHealth}
}

// Status reports what the target is doing with each endpoint. It takes none of
// the HID locks, so a caller cannot delay a keystroke by asking.
func (h *Hid) Status() []proto.HidDeviceStatus {
	now := time.Now()

	devices := h.devices()
	statuses := make([]proto.HidDeviceStatus, 0, len(devices))
	for _, device := range devices {
		status := device.health.snapshot(now)
		status.Name = device.name
		status.Path = device.path
		statuses = append(statuses, status)
	}

	return statuses
}

func (h *Hid) devices() []hidDevice {
	return []hidDevice{
		h.keyboardDevice(HID0),
		h.relativeMouseDevice(HID1),
		h.absoluteMouseDevice(HID2),
	}
}

func (h *Hid) OpenNoLock() error {
	h.CloseNoLock()

	var errs []error
	for _, device := range h.devices() {
		if err := h.openDeviceNoLock(device); err != nil {
			log.Errorf("open %s failed: %s", device.path, err)
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

func (h *Hid) OpenNoLockWithRetry(timeout, delay time.Duration) error {
	return openNoLockWithRetry(h.OpenNoLock, timeout, delay)
}

func openNoLockWithRetry(open func() error, timeout, delay time.Duration) error {
	if timeout <= 0 {
		return open()
	}
	if delay <= 0 {
		delay = hidReopenRetryDelay
	}

	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		if err := open(); err != nil {
			lastErr = err
		} else {
			return nil
		}

		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		if remaining > delay {
			remaining = delay
		}
		time.Sleep(remaining)
	}

	return fmt.Errorf("open HID devices within %s: %w", timeout, lastErr)
}

func (h *Hid) CloseNoLock() {
	for _, device := range h.devices() {
		h.closeDeviceNoLock(device)
	}
}

func (h *Hid) openDeviceNoLock(device hidDevice) error {
	if device.get() != nil {
		return nil
	}

	file, err := os.OpenFile(device.path, os.O_WRONLY|syscall.O_NONBLOCK, 0o666)
	if err != nil {
		return fmt.Errorf("%s: %w", device.path, err)
	}

	device.set(file)
	return nil
}

func (h *Hid) closeDeviceNoLock(device hidDevice) {
	file := device.get()
	if file == nil {
		return
	}

	device.set(nil)
	if err := file.Close(); err != nil {
		log.Debugf("close %s failed: %s", device.path, err)
	}
}

// writeWithTimeout bounds how long callers hold HID locks when writing to a
// nonblocking descriptor. EAGAIN means the host is not accepting HID reports
// yet, so retry until the caller's deadline expires.
func writeWithTimeout(writer hidWriter, data []byte, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	for {
		n, err := writer.Write(data)
		if err == nil {
			if n != len(data) {
				return io.ErrShortWrite
			}
			return nil
		}

		if n != 0 {
			return io.ErrShortWrite
		}
		if !isRetryableWriteError(err) {
			return err
		}

		remaining := time.Until(deadline)
		if timeout <= 0 || remaining <= 0 {
			return os.ErrDeadlineExceeded
		}
		if remaining > hidWriteRetryDelay {
			remaining = hidWriteRetryDelay
		}
		time.Sleep(remaining)
	}
}

func isRetryableWriteError(err error) bool {
	return errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK)
}

// hidFileWasDeleted reports whether the device node behind an open handle has
// been removed.
//
// Rebuilding the USB gadget - mounting an image, switching HID mode - deletes
// /dev/hidg*. A handle opened before that keeps accepting writes that reach
// nothing, so keyboard and mouse stop working with no error to say why. The
// kernel marks the link in /proc/self/fd for exactly this case.
func hidFileWasDeleted(file *os.File) bool {
	target, err := os.Readlink(fmt.Sprintf("/proc/self/fd/%d", file.Fd()))
	if err != nil {
		return false
	}

	return strings.HasSuffix(target, " (deleted)")
}

func (h *Hid) Open() {
	h.kbMutex.Lock()
	defer h.kbMutex.Unlock()
	h.mouseMutex.Lock()
	defer h.mouseMutex.Unlock()

	h.OpenNoLock()
}

func (h *Hid) Close() {
	h.kbMutex.Lock()
	defer h.kbMutex.Unlock()
	h.mouseMutex.Lock()
	defer h.mouseMutex.Unlock()

	h.CloseNoLock()
}

func (h *Hid) WriteHid0(data []byte) {
	if err := h.WriteKeyboardReport(data); err != nil {
		reportWriteFailure("keyboard HID write failed", err)
	}
}

func (h *Hid) WriteHid1(data []byte) {
	if err := h.WriteRelativeMouseReport(data); err != nil {
		reportWriteFailure("relative mouse HID write failed", err)
	}
}

func (h *Hid) WriteHid2(data []byte) {
	if err := h.WriteAbsoluteMouseReport(data); err != nil {
		reportWriteFailure("absolute mouse HID write failed", err)
	}
}

func (h *Hid) WriteKeyboardReport(data []byte) error {
	if len(data) != 8 {
		return fmt.Errorf("invalid keyboard report length: %d", len(data))
	}
	return h.writeHID(h.keyboardDevice(HID0), data)
}

func (h *Hid) WriteRelativeMouseReport(data []byte) error {
	if len(data) != 4 {
		return fmt.Errorf("invalid relative mouse report length: %d", len(data))
	}
	return h.writeHID(h.relativeMouseDevice(HID1), data)
}

func (h *Hid) WriteAbsoluteMouseReport(data []byte) error {
	if len(data) != 6 {
		return fmt.Errorf("invalid absolute mouse report length: %d", len(data))
	}
	return h.writeHID(h.absoluteMouseDevice(HID2), data)
}

func (h *Hid) writeHIDReport(device hidDevice, data []byte) bool {
	if err := h.writeHID(device, data); err != nil {
		reportWriteFailure("write to "+device.path+" failed", err)
		return false
	}
	return true
}

// errRepeatedFailure marks a write failure that repeats one already reported for
// the same endpoint. A stalled endpoint fails every report - about twenty a
// second while the mouse moves - and each call site would otherwise print the
// same line every time. Call sites pass their failures through
// reportWriteFailure, which drops the repeats and keeps the first.
var errRepeatedFailure = errors.New("repeated failure")

// reportWriteFailure logs a failed HID operation unless the same endpoint has
// already reported this failure. The context describes the operation, because
// which write failed is worth knowing and the error alone does not say.
func reportWriteFailure(context string, err error) {
	if errors.Is(err, errRepeatedFailure) {
		return
	}

	log.Errorf("%s: %s", context, err)
}

func (h *Hid) writeHID(device hidDevice, data []byte) error {
	device.mu.Lock()
	defer device.mu.Unlock()

	return device.note(h.writeHIDLocked(device, data))
}

// note records what one write did to the endpoint and decides whether the
// failure is worth reporting. It logs the two transitions no call site can see:
// an endpoint that has stopped accepting reports, and one that has started
// again.
func (d hidDevice) note(err error) error {
	transition := d.health.record(err, time.Now())

	if err == nil {
		if transition.Changed && transition.From != hidStateUnknown {
			log.Infof("%s is accepting reports again", d.path)
		}
		return nil
	}

	if !transition.Changed {
		return fmt.Errorf("%w: %w", errRepeatedFailure, err)
	}

	if transition.To == hidStateStalled {
		// The failure that looks like nothing. The device node is present, the
		// gadget is bound, and the target is simply not fetching from this
		// endpoint - so every report to it is dropped while the others work.
		//
		// The message names the endpoint and stops there. Why a target stops
		// collecting from one interface is not knowable from this side, and a
		// guess printed as a fact would send the operator the wrong way.
		log.Errorf("%s: the target is not fetching reports from this endpoint, so reports to it are lost. "+
			"The other HID endpoints are unaffected. A USB reset, or a mouse mode that uses a different "+
			"endpoint, may recover it", d.path)
	}

	return err
}

// writeReport puts one report on the wire. It is a variable so tests can drive
// the outcomes writeHID has to react to: an endpoint the target is not fetching
// from cannot be built out of an ordinary file, and a pipe stands in for it only
// as long as the runtime keeps the descriptor pollable, which it does not
// promise.
var writeReport = func(path string, file *os.File, data []byte, timeout time.Duration) error {
	// Two mechanisms for one deadline, because only one of them is available
	// at a time. A pollable descriptor honours the deadline directly; one the
	// runtime declined to poll returns EAGAIN instead, and writeWithTimeout
	// bounds the retries.
	if err := file.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
		log.Debugf("set write deadline for %s failed: %s", path, err)
	}

	return writeWithTimeout(file, data, timeout)
}

func (h *Hid) writeHIDLocked(device hidDevice, data []byte) error {
	// Drop a handle whose device node is gone before writing into it.
	if file := device.get(); file != nil && hidFileWasDeleted(file) {
		log.Debugf("%s was rebuilt underneath us, reopening", device.path)
		h.closeDeviceNoLock(device)
	}

	if err := h.openDeviceNoLock(device); err != nil {
		return err
	}

	file := device.get()
	if file == nil {
		return fmt.Errorf("%s: hid handle is nil", device.path)
	}

	if err := writeReport(device.path, file, data, hidWriteTimeout); err != nil {
		h.closeDeviceNoLock(device)
		switch {
		case errors.Is(err, os.ErrClosed):
			return fmt.Errorf("hid already closed: %w", err)
		case errors.Is(err, os.ErrDeadlineExceeded):
			return fmt.Errorf("timeout after %s: %w", hidWriteTimeout, err)
		default:
			return err
		}
	}

	traceHIDWrite(device.path, data)
	return nil
}

// traceHIDWrite logs a report only when someone is listening. logrus skips the
// formatting on its own, but the variadic call still allocates a slice and
// boxes both arguments, once per report.
func traceHIDWrite(path string, data []byte) {
	if !log.IsLevelEnabled(log.DebugLevel) {
		return
	}

	log.Debugf("write to %s: %v", path, data)
}
