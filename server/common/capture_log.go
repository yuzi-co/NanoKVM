package common

import (
	"sync"

	log "github.com/sirupsen/logrus"
)

// captureReadOK is the outcome every successful read collapses to.
//
// The raw result carries more than success or failure: an H.264 read returns 3
// for a keyframe and 0 otherwise, so it changes at every GOP boundary. The
// tracker below reports changes, so noting the raw value would report one per
// keyframe. Only the failures are worth telling apart.
const captureReadOK = 0

// captureReadOutcome maps a raw read result onto the value the tracker follows.
// Negative results are failures and keep their code, because which failure it
// is the only diagnostic here. Everything else is a success.
func captureReadOutcome(result int) int {
	if result < 0 {
		return result
	}

	return captureReadOK
}

// captureReadLog reports when the outcome of frame reads changes, so that a
// result which repeats is recorded once rather than once per frame.
//
// A read that fails keeps failing for as long as its cause lasts, and the cause
// is usually mundane: a target whose monitor is off returns "no image" on every
// read, for as long as a viewer is connected. Recording each one measured 213KB
// an hour into /tmp/nanokvm-server.log on an otherwise idle board. That file
// lives in the 80MB tmpfs the restart path needs 36MB of, and only S99vidiag's
// trim was holding it down - a trim its own header says nothing may depend on,
// because it stops when the reader stops.
//
// The zero value is a pipeline that is working, so a board that never fails
// reports nothing at all. service/hid reports HID writes the same way and for
// the same reason; see errRepeatedFailure there.
type captureReadLog struct {
	mutex sync.Mutex
	last  int
	run   int
}

// note records one read outcome. When the outcome differs from the one before
// it, note reports the change together with what the previous outcome was and
// how many reads it lasted. The run length is the part a per-frame line buries.
func (l *captureReadLog) note(result int) (changed bool, previous int, previousRun int) {
	l.mutex.Lock()
	defer l.mutex.Unlock()

	if result == l.last {
		l.run++
		return false, 0, 0
	}

	previous = l.last
	previousRun = l.run

	l.last = result
	l.run = 1

	return true, previous, previousRun
}

// reportCaptureRead records one read outcome and writes a line only when the
// outcome changes.
//
// The failure keeps the error level it has always had, because a capture that
// cannot read is worth an operator's attention the first time it happens. What
// changes is that it is said once. The recovery says how many reads the outage
// covered, which is the only measure of it that survives.
func reportCaptureRead(l *captureReadLog, result int) {
	outcome := captureReadOutcome(result)

	changed, previous, previousRun := l.note(outcome)
	if !changed {
		return
	}

	if outcome != captureReadOK {
		log.Errorf("failed to read kvm image: %v", outcome)
		return
	}

	if previous != captureReadOK {
		log.Infof("kvm image reads recovered after %d failed reads with result %v", previousRun, previous)
	}
}
