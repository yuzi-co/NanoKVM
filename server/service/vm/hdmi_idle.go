package vm

import (
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"NanoKVM-Server/common"
	"NanoKVM-Server/utils"
)

// An idle NanoKVM captures and encodes video that nobody is watching, for as
// long as it is plugged in. Switching capture off while there are no viewers
// and bringing it back when one arrives costs a second of startup on the
// first connection and nothing else.
//
// These are variables so the state machine can be tested without hardware.
var (
	setCapture      = func(on bool) { common.GetKvmVision().SetHDMI(on) }
	idleTimeout     = func() time.Duration { return time.Duration(utils.GetHDMIIdleTimeout()) * time.Minute }
	captureDisabled = utils.IsHdmiDisabled
)

var (
	idleMutex sync.Mutex

	// viewers is counted per stream because a client switching between mjpeg,
	// h264 and webrtc is briefly present on two of them.
	viewers = map[string]int{}

	idleTimer *time.Timer

	// stoppedForIdle separates capture we switched off from capture the user
	// switched off. Only the former may be brought back automatically.
	stoppedForIdle bool

	// generation invalidates a timer that has already fired or is about to.
	generation uint64

	// resuming tracks resets still in flight, so a caller can wait for the
	// package to go quiet.
	resuming sync.WaitGroup
)

// SetViewerCount records how many clients a stream currently has.
//
// Callers must not hold their own lock: stopping and resuming capture is slow,
// and a stream's lock is on the path that delivers frames to everyone else.
func SetViewerCount(source string, count int) {
	if count < 0 {
		count = 0
	}

	idleMutex.Lock()
	defer idleMutex.Unlock()

	viewers[source] = count
	generation++
	stopTimerLocked()

	if totalViewersLocked() == 0 {
		scheduleTimerLocked()
		return
	}

	if stoppedForIdle && !captureDisabled() {
		stoppedForIdle = false
		resumeLocked()
	}
}

// EnableHdmiCapture switches capture on and starts watching for idleness.
func EnableHdmiCapture() {
	idleMutex.Lock()
	defer idleMutex.Unlock()

	setCapture(true)
	stoppedForIdle = false
	generation++
	stopTimerLocked()
	scheduleTimerLocked()
}

// DisableHdmiCapture switches capture off at the user's request, which the
// idle machinery must not undo.
func DisableHdmiCapture() {
	idleMutex.Lock()
	defer idleMutex.Unlock()

	setCapture(false)
	stoppedForIdle = false
	generation++
	stopTimerLocked()
}

// ApplyHdmiIdleTimeout takes up a changed timeout straight away rather than
// waiting for the next viewer to come or go.
func ApplyHdmiIdleTimeout() {
	idleMutex.Lock()
	defer idleMutex.Unlock()

	generation++
	stopTimerLocked()

	if stoppedForIdle && !captureDisabled() {
		stoppedForIdle = false
		resumeLocked()
		return
	}

	scheduleTimerLocked()
}

func totalViewersLocked() int {
	total := 0
	for _, count := range viewers {
		total += count
	}

	return total
}

func stopTimerLocked() {
	if idleTimer != nil {
		idleTimer.Stop()
		idleTimer = nil
	}
}

func scheduleTimerLocked() {
	if stoppedForIdle || captureDisabled() || totalViewersLocked() != 0 {
		return
	}

	timeout := idleTimeout()
	if timeout <= 0 {
		return
	}

	scheduled := generation
	idleTimer = time.AfterFunc(timeout, func() {
		idleMutex.Lock()
		defer idleMutex.Unlock()

		if scheduled != generation || totalViewersLocked() != 0 || captureDisabled() {
			return
		}

		idleTimer = nil
		stoppedForIdle = true
		setCapture(false)

		log.Debugf("stopped hdmi capture after %s without viewers", timeout)
	})
}

// resumeLocked restarts capture on its own goroutine. The reset takes about a
// second, and the caller is accepting a stream client while it happens.
func resumeLocked() {
	resuming.Add(1)

	go func() {
		defer resuming.Done()

		setCapture(false)
		time.Sleep(1 * time.Second)

		idleMutex.Lock()
		defer idleMutex.Unlock()

		// The viewer may have given up, or capture may have been switched off
		// by hand, while the reset was in progress.
		if totalViewersLocked() == 0 || captureDisabled() {
			scheduleTimerLocked()
			return
		}

		setCapture(true)
		log.Debug("resumed hdmi capture for a new viewer")
	}()
}

// resetIdleState returns the package to its initial state, for tests. The wait
// is outside the lock because a reset in flight needs it to finish.
func resetIdleState() {
	resuming.Wait()

	idleMutex.Lock()
	defer idleMutex.Unlock()

	stopTimerLocked()
	viewers = map[string]int{}
	stoppedForIdle = false
	generation++
}
