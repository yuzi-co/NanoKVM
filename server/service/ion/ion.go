package ion

import (
	"os"
	"path/filepath"
	"sync"
)

// Root is the carveout debugfs directory. It is a variable so that tests can
// point it at a fixture directory.
//
// Do not add /proc/cvitek/vb to anything in this package. Reading that file
// blocks the reader forever in uninterruptible sleep, and the reader cannot be
// killed.
var Root = "/sys/kernel/debug/ion/cvi_carveout_heap_dump"

// Status is one reading of the carveout.
type Status struct {
	Total       uint64
	Used        uint64
	Free        uint64
	UsageRate   int
	Generations int
	Reserve     uint64
	// Measured is false when Reserve is the configured floor rather than a
	// value this process observed.
	Measured bool
	Verdict  string
}

var (
	mu           sync.Mutex
	allocAtStart uint64
	baselineOK   bool
	peakReset    bool
	reserveFloor uint64
)

// Init records the allocation level at startup and resets the peak watermark,
// so that a later reading measures what this process needed rather than what
// the board happened to hold.
//
// The working set is cumulative over delivery paths: a board that only serves
// screenshots needs less than one that also streams H264. A fixed constant is
// therefore wrong in both directions, and the floor exists only to cover the
// window before this process has captured anything.
//
// Every failure here is survivable. A board whose peak cannot be reset falls
// back to the floor and still reports a graded verdict.
func Init(floor uint64) {
	mu.Lock()
	defer mu.Unlock()

	reserveFloor = floor
	allocAtStart = 0
	baselineOK = false
	peakReset = false

	alloc, err := readCounter("alloc_mem")
	if err != nil {
		return
	}
	allocAtStart = alloc
	baselineOK = true

	if err := os.WriteFile(filepath.Join(Root, "peak"), []byte("0"), 0o644); err == nil {
		peakReset = true
	}
}

// Read takes one reading. It never returns an error: a carveout it cannot read
// is reported as unavailable, and the UI shows nothing.
func Read() Status {
	mu.Lock()
	base, haveBase, reset, floor := allocAtStart, baselineOK, peakReset, reserveFloor
	mu.Unlock()

	total, errTotal := readCounter("total_mem")
	used, errUsed := readCounter("alloc_mem")
	if errTotal != nil || errUsed != nil || total == 0 {
		return Status{Verdict: VerdictUnavailable}
	}

	status := Status{Total: total, Used: used}
	if used <= total {
		status.Free = total - used
	}
	status.UsageRate = int(used * 100 / total)

	if body, err := os.ReadFile(filepath.Join(Root, "summary")); err == nil {
		if buffers, err := ParseSummary(string(body)); err == nil {
			status.Generations = CountGenerations(buffers)
		}
	}

	// The floor stands until this process has been observed needing more. Once
	// a real measurement exists it replaces the floor outright, even when the
	// observed growth is smaller than the floor: the floor is a pessimistic
	// stand-in for the unmeasured case, not a minimum on the measured one.
	status.Reserve = floor
	if haveBase && reset {
		if peak, err := readCounter("peak"); err == nil && peak > base {
			status.Reserve = peak - base
			status.Measured = true
		}
	}

	status.Verdict = Verdict(status.Free, status.Reserve)
	return status
}

func readCounter(name string) (uint64, error) {
	body, err := os.ReadFile(filepath.Join(Root, name))
	if err != nil {
		return 0, err
	}
	return ParseCounter(string(body))
}
