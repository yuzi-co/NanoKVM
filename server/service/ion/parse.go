// Package ion reports the state of the SG2002 ION carveout.
//
// The carveout is a fixed 75MB region reserved before Linux starts. It is not
// CMA, so none of it comes back when the capture path is idle. libkvm does not
// check the result of an allocation, which makes an exhausted carveout a
// segfault rather than an error, and only a reboot clears it.
package ion

import (
	"bufio"
	"errors"
	"strconv"
	"strings"
)

// Verdicts. The thresholds are explained in Verdict.
const (
	VerdictOK          = "ok"
	VerdictWarn        = "warn"
	VerdictCritical    = "critical"
	VerdictUnavailable = "unavailable"
)

// generationBuffer is allocated once when the capture path initialises. A
// second entry therefore means a second initialisation, and because process
// death frees nothing here, it means a process died holding memory.
const generationBuffer = "ISP_SHARED_BUFFER_0"

// detailsColumns is the field count of one row in the Details table:
// heap_id, alloc_buf_size, phy_addr, kmap_cnt, buffer name.
const detailsColumns = 5

// Buffer is one row of the Details table in the carveout summary.
type Buffer struct {
	HeapID  int
	Size    uint64
	PhyAddr string
	Name    string
}

// ParseCounter reads one of the plain integer files: total_mem, alloc_mem or peak.
func ParseCounter(s string) (uint64, error) {
	t := strings.TrimSpace(s)
	if t == "" {
		return 0, errors.New("ion: empty counter")
	}
	return strconv.ParseUint(t, 10, 64)
}

// ParseSummary reads the Details table out of the carveout summary. Text that
// is not a summary yields no buffers and no error: a diagnostic must not become
// a fault, and an unfamiliar firmware layout is not this server's problem.
func ParseSummary(s string) ([]Buffer, error) {
	var out []Buffer

	scanner := bufio.NewScanner(strings.NewReader(s))
	inDetails := false

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "Details:") {
			inDetails = true
			continue
		}
		if !inDetails {
			continue
		}
		// The table ends here. The lines after it describe the free regions and
		// have the same shape as a buffer row, so stopping is not optional.
		if strings.HasPrefix(line, "minimum ion allocate unit") {
			break
		}

		fields := strings.Fields(line)
		if len(fields) != detailsColumns {
			continue // the column header has six fields
		}
		heapID, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		size, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}

		out = append(out, Buffer{
			HeapID:  heapID,
			Size:    size,
			PhyAddr: fields[2],
			Name:    fields[4],
		})
	}

	return out, scanner.Err()
}

// CountGenerations reports how many capture initialisations the carveout is
// holding. One is healthy. More than one means that many server processes have
// died without releasing their buffers.
func CountGenerations(buffers []Buffer) int {
	n := 0
	for _, b := range buffers {
		if b.Name == generationBuffer {
			n++
		}
	}
	return n
}

// Verdict grades the free carveout against what one capture session costs.
//
// There is only ever one capture session, so the warn threshold is not room for
// a second one. A restart orphans the whole working set and allocates a fresh
// copy, so the cost of a restart is a full generation. warn therefore means one
// restart away from critical.
//
// critical on its own is too late to act on: the board looks healthy until
// someone opens the stream, and opening the stream is what kills the server.
func Verdict(free, reserve uint64) string {
	if reserve == 0 {
		return VerdictUnavailable
	}
	if free < reserve {
		return VerdictCritical
	}
	if free < 2*reserve {
		return VerdictWarn
	}
	return VerdictOK
}
