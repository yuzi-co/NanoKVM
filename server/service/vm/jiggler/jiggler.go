package jiggler

import (
	"context"
	"os"
	"strings"
	"sync"
	"time"

	"NanoKVM-Server/service/controlmode"
	"NanoKVM-Server/service/hid"
	"NanoKVM-Server/service/inputcontrol"
)

// ConfigFile records whether the jiggler is on, and its contents are the mode.
// A variable so tests can point it somewhere writable.
var ConfigFile = "/etc/kvm/mouse-jiggler"

// DefaultInterval is how long the target may sit idle before the jiggler moves
// the mouse.
const DefaultInterval = 15 * time.Second

var (
	jiggler Jiggler
	once    sync.Once
)

type Jiggler struct {
	mutex       sync.Mutex
	enabled     bool
	running     bool
	mode        string
	lastUpdated time.Time

	// configMutex serialises the config file against itself, so the file and
	// the fields above cannot disagree after two calls race. It is deliberately
	// not mutex: the websocket read loop takes that one on every HID event, and
	// a write to the boot SD card can take tens of milliseconds. Input must not
	// wait behind it.
	configMutex sync.Mutex

	// interval and move belong to the instance rather than the package so a
	// test can drive the loop quickly and without a HID gadget behind it. Both
	// are set when the value is built and never written again, so the loop
	// reads them without the lock.
	interval time.Duration
	move     func(string)
}

func GetJiggler() *Jiggler {
	once.Do(func() {
		jiggler = Jiggler{
			mutex:       sync.Mutex{},
			enabled:     false,
			running:     false,
			mode:        "relative",
			lastUpdated: time.Now(),
			interval:    DefaultInterval,
			move:        move,
		}

		content, err := os.ReadFile(ConfigFile)
		if err != nil {
			return
		}

		mode := strings.ReplaceAll(string(content), "\n", "")
		if mode != "" {
			jiggler.mode = mode
		}

		jiggler.enabled = true
	})

	return &jiggler
}

// Enable turns the jiggler on and remembers the choice across a restart. The
// file is written first, so a jiggler that reports itself on is one the next
// boot also finds on.
//
// configMutex spans the write and the fields it describes. Without it two calls
// can write the file in one order and update the fields in the other, leaving
// the running jiggler in a mode the next boot does not agree with.
func (j *Jiggler) Enable(mode string) error {
	j.configMutex.Lock()

	if err := os.WriteFile(ConfigFile, []byte(mode), 0644); err != nil {
		j.configMutex.Unlock()
		return err
	}

	j.mutex.Lock()
	j.enabled = true
	j.mode = mode
	j.mutex.Unlock()

	j.configMutex.Unlock()

	// Outside both locks, because Run takes mutex.
	j.Run()

	return nil
}

func (j *Jiggler) Disable() error {
	j.configMutex.Lock()
	defer j.configMutex.Unlock()

	if err := os.Remove(ConfigFile); err != nil {
		return err
	}

	j.mutex.Lock()
	defer j.mutex.Unlock()

	j.enabled = false
	j.mode = "relative"

	return nil
}

// Run starts the loop and reports whether this call is the one that started it.
// Boot calls it once and every Enable calls it again, so it has to be safe to
// call at any time from anywhere.
//
// The check and the claim happen under one lock. Reading the flag and then
// setting it as two steps let two callers both find it clear, and two loops
// move the mouse twice as often as asked.
func (j *Jiggler) Run() bool {
	j.mutex.Lock()
	if !j.enabled || j.running {
		j.mutex.Unlock()
		return false
	}

	j.running = true
	j.lastUpdated = time.Now()
	j.mutex.Unlock()

	go j.loop()

	return true
}

// loop moves the mouse whenever the target has been idle for the interval.
func (j *Jiggler) loop() {
	ticker := time.NewTicker(j.interval)
	defer ticker.Stop()

	for range ticker.C {
		mode, due, running := j.tick()
		if !running {
			return
		}

		if due {
			j.move(mode)
		}
	}
}

// tick decides what the loop does next: the mode to move in, whether a move is
// due, and whether the loop should keep running at all.
//
// The decision and the state it reads live together under the lock, because the
// websocket read loop writes lastUpdated once per HID event. A move counts as
// activity, so tick records it here rather than making the loop reacquire the
// lock to say so.
func (j *Jiggler) tick() (mode string, due bool, running bool) {
	j.mutex.Lock()
	defer j.mutex.Unlock()

	if !j.enabled {
		j.running = false
		return "", false, false
	}

	if time.Since(j.lastUpdated) <= j.interval {
		return "", false, true
	}

	j.lastUpdated = time.Now()

	return j.mode, true, true
}

// Update records that the target saw real input, which holds the jiggler off.
// It runs once per HID event on the websocket read loop.
func (j *Jiggler) Update() {
	j.mutex.Lock()
	defer j.mutex.Unlock()

	if j.running {
		j.lastUpdated = time.Now()
	}
}

func (j *Jiggler) IsEnabled() bool {
	j.mutex.Lock()
	defer j.mutex.Unlock()

	return j.enabled
}

func (j *Jiggler) GetMode() string {
	j.mutex.Lock()
	defer j.mutex.Unlock()

	return j.mode
}

func move(mode string) {
	_, releaseMode, err := controlmode.GetManager().AcquireStable()
	if err != nil {
		return
	}
	defer releaseMode()

	ctx, release, err := inputcontrol.GetCoordinator().BeginBackground(context.Background())
	if err != nil {
		return
	}
	defer release()

	h := hid.GetHid()

	if mode == "absolute" {
		if err := h.WriteAbsoluteMouseReport([]byte{0x00, 0x00, 0x3f, 0x00, 0x3f, 0x00}); err != nil {
			return
		}
		defer func() {
			_ = h.WriteAbsoluteMouseReport([]byte{0x00, 0xff, 0x3f, 0xff, 0x3f, 0x00})
		}()
		_ = waitMove(ctx, 100*time.Millisecond)
	} else {
		if err := h.WriteRelativeMouseReport([]byte{0x00, 0xa, 0xa, 0x00}); err != nil {
			return
		}
		defer func() {
			_ = h.WriteRelativeMouseReport([]byte{0x00, 0xf6, 0xf6, 0x00})
		}()
		_ = waitMove(ctx, 100*time.Millisecond)
	}
}

func waitMove(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
