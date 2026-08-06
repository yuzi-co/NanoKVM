# ION Carveout Visibility Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Report the ION carveout state through the API and the web UI, so an operator sees the board approach the allocation failure that kills the server, instead of discovering it as a crash.

**Architecture:** A pure parser turns the cvitek debugfs text into a struct. A thin reader records the allocation level at startup, resets the writable `peak` counter, and reads the counters on demand — so the server measures its own capture requirement instead of trusting a constant. `GET /api/vm/ion` publishes it. `S98supervise` writes one line at each restart, because a restart is what erodes the carveout and the supervisor is the only component that outlives the server.

**Tech Stack:** Go 1.24 (gin, viper), React + TypeScript (jotai, antd, tailwind), POSIX shell for busybox `ash`.

**Spec:** `docs/superpowers/specs/2026-08-07-ion-visibility-design.md`. Read it for the measurements every threshold rests on.

## Global Constraints

- Do not write runtime state under `/kvmapp`. That is the boot SD card.
- `S98supervise` is fork tooling. It installs to `/etc/init.d` only, never to `/kvmapp/system/init.d`.
- Init scripts and shell tests keep LF line endings. `core.autocrlf` is `true` on the workstation, so `git diff` lies — check with `git cat-file -p <rev>:<path>`.
- **Never read `/proc/cvitek/vb`.** It blocks the reader forever in uninterruptible sleep and the reader cannot be killed. Any code that reads carveout state must be reviewed against this.
- `go vet -tags novision ./...` and `go test -tags novision ./...` must pass.
- `CGO_ENABLED=0 GOOS=linux GOARCH=riscv64 go build -tags novision ./...` must pass.
- Every new UI string goes into `web/src/i18n/locales/en.ts` at minimum.
- Docs follow ASD-STE100. `server/README.md` changes must be mirrored in `server/README_ZH.md` and `server/README_JA.md`. `tools/README.md` has no translated variants.
- A diagnostic must never become a fault. Every read path degrades to "unavailable" rather than erroring.

**Running the Go tests.** There is no local Go toolchain. Use the plain `golang` image, not the MaixCDK builder:

```shell
docker run --rm -v "$PWD/server:/src" -v nanokvm-gomod:/go/pkg/mod -w /src \
  -e CGO_ENABLED=0 golang:1.25 go test -tags novision ./service/ion/...
```

On Windows in Git Bash, prefix with `MSYS_NO_PATHCONV=1` and use `$(pwd -W)` in place of `$PWD`.

**Measured values used in this plan.** Do not "tidy" these into round numbers.

| state | `alloc_mem` |
| --- | --- |
| fresh boot, capture never started | 19,050,496 |
| after screenshots only | 31,600,640 |
| after a browser stream as well | 42,942,464 |
| after one server restart, two generations | 49,459,200 |

Carveout size is 78,643,200. The reserve floor is 25,165,824 (24MB).

---

### Task 1: The pure ION parser

Text in, struct out. No filesystem access in this file, so every case runs off-device.

**Files:**
- Create: `server/service/ion/parse.go`
- Create: `server/service/ion/parse_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `type Buffer struct { HeapID int; Size uint64; PhyAddr string; Name string }`,
  `ParseCounter(string) (uint64, error)`, `ParseSummary(string) ([]Buffer, error)`,
  `CountGenerations([]Buffer) int`, `Verdict(free, reserve uint64) string`, and the constants
  `VerdictOK`, `VerdictWarn`, `VerdictCritical`, `VerdictUnavailable`.

- [ ] **Step 1: Write the failing test**

Create `server/service/ion/parse_test.go`. The fixtures are real captures from the device on 2026-08-06.

```go
package ion

import "testing"

// summaryIdle is a fresh boot with capture never started. It keeps the trailing
// "free memory regions" block, which proves the parser stops at the end of the
// Details table instead of reading rows out of it.
const summaryIdle = `Summary:
[0] carveout heap size:78643200 bytes, used:19050496 bytes
usage rate:25%, memory usage peak 19050496 bytes

Details:
         heap_id   alloc_buf_size         phy_addr         kmap_cnt      buffer name
               0         12533760         8b300000                1          VbPool0
               0          6221824         8bf3c000                1          VbPool1
               0           294912         8bef4000                1 ISP_SHARED_BUFFER_0


minimum ion allocate unit = 4096
free memory regions:
         heap_id            start              end           length
               0         8c52b000         8fe00000         59592704
`

// summaryTwoGenerations was produced deliberately by restarting the server. The
// two ISP_SHARED_BUFFER_0 entries at different phy_addr are the whole point:
// one belongs to the live process and one to the process that died holding it.
const summaryTwoGenerations = `Summary:
[0] carveout heap size:78643200 bytes, used:49459200 bytes
usage rate:62%, memory usage peak 49459200 bytes

Details:
         heap_id   alloc_buf_size         phy_addr         kmap_cnt      buffer name
               0          9437184         8cffc000                1         jpeg_ion
               0          6221824         8dc3c000                1          VbPool4
               0          3112960         8d8fc000                1          VbPool3
               0         12533760         8b300000                1          VbPool0
               0          3133440         8c6ff000                1          VbPool2
               0            81920         8c6eb000                1 VENC_1_H264_WorkBuffer
               0          6221824         8bf3c000                1          VbPool1
               0           786432         8c52b000                1 VCODEC_H264_FW_Buffer
               0           294912         8dbf4000                1 ISP_SHARED_BUFFER_0
               0          3145728         8ccfc000                1 VENC_1_ReconFrameBuf
               0          3145728         8c9fc000                1 VENC_1_ReconFrameBuf
               0          1048576         8c5eb000                1 VENC_1_BitStreamBuffer
               0           294912         8bef4000                1 ISP_SHARED_BUFFER_0


minimum ion allocate unit = 4096
`

func TestParseCounter(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want uint64
		bad  bool
	}{
		{"plain", "19050496", 19050496, false},
		{"trailing newline", "78643200\n", 78643200, false},
		{"zero", "0", 0, false},
		{"empty", "", 0, true},
		{"blank", "   \n", 0, true},
		{"not a number", "nineteen", 0, true},
		{"negative", "-1", 0, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseCounter(c.in)
			if c.bad {
				if err == nil {
					t.Fatalf("ParseCounter(%q) = %d, want an error", c.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseCounter(%q): %s", c.in, err)
			}
			if got != c.want {
				t.Fatalf("ParseCounter(%q) = %d, want %d", c.in, got, c.want)
			}
		})
	}
}

func TestParseSummaryReadsOnlyTheDetailsTable(t *testing.T) {
	bufs, err := ParseSummary(summaryIdle)
	if err != nil {
		t.Fatalf("ParseSummary: %s", err)
	}
	if len(bufs) != 3 {
		t.Fatalf("got %d buffers, want 3: %+v", len(bufs), bufs)
	}

	want := []Buffer{
		{HeapID: 0, Size: 12533760, PhyAddr: "8b300000", Name: "VbPool0"},
		{HeapID: 0, Size: 6221824, PhyAddr: "8bf3c000", Name: "VbPool1"},
		{HeapID: 0, Size: 294912, PhyAddr: "8bef4000", Name: "ISP_SHARED_BUFFER_0"},
	}
	for i := range want {
		if bufs[i] != want[i] {
			t.Fatalf("buffer %d = %+v, want %+v", i, bufs[i], want[i])
		}
	}
}

func TestParseSummaryTotalsAgreeWithTheHeader(t *testing.T) {
	bufs, err := ParseSummary(summaryIdle)
	if err != nil {
		t.Fatalf("ParseSummary: %s", err)
	}

	var sum uint64
	for _, b := range bufs {
		sum += b.Size
	}
	if sum != 19050496 {
		t.Fatalf("buffer sizes total %d, want 19050496 to match the header", sum)
	}
}

func TestCountGenerations(t *testing.T) {
	one, err := ParseSummary(summaryIdle)
	if err != nil {
		t.Fatalf("ParseSummary: %s", err)
	}
	if got := CountGenerations(one); got != 1 {
		t.Fatalf("idle board: CountGenerations = %d, want 1", got)
	}

	two, err := ParseSummary(summaryTwoGenerations)
	if err != nil {
		t.Fatalf("ParseSummary: %s", err)
	}
	if got := CountGenerations(two); got != 2 {
		t.Fatalf("after one restart: CountGenerations = %d, want 2", got)
	}
}

func TestParseSummaryOnRubbish(t *testing.T) {
	for _, in := range []string{"", "Summary:\n", "Details:\n", "not a summary at all\n"} {
		bufs, err := ParseSummary(in)
		if err != nil {
			t.Fatalf("ParseSummary(%q) returned an error: %s", in, err)
		}
		if len(bufs) != 0 {
			t.Fatalf("ParseSummary(%q) = %+v, want no buffers", in, bufs)
		}
	}
}

func TestVerdict(t *testing.T) {
	const reserve = 12550144

	cases := []struct {
		name    string
		free    uint64
		reserve uint64
		want    string
	}{
		{"plenty", reserve * 4, reserve, VerdictOK},
		{"exactly twice the reserve is ok", reserve * 2, reserve, VerdictOK},
		{"one byte under twice is warn", reserve*2 - 1, reserve, VerdictWarn},
		{"exactly the reserve is warn", reserve, reserve, VerdictWarn},
		{"one byte under the reserve is critical", reserve - 1, reserve, VerdictCritical},
		{"nothing free is critical", 0, reserve, VerdictCritical},
		{"no reserve is unavailable", reserve * 4, 0, VerdictUnavailable},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Verdict(c.free, c.reserve); got != c.want {
				t.Fatalf("Verdict(%d, %d) = %q, want %q", c.free, c.reserve, got, c.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run:

```shell
docker run --rm -v "$PWD/server:/src" -v nanokvm-gomod:/go/pkg/mod -w /src \
  -e CGO_ENABLED=0 golang:1.25 go test -tags novision ./service/ion/...
```

Expected: FAIL — the package does not build, because `Buffer`, `ParseCounter`, `ParseSummary`, `CountGenerations` and `Verdict` are undefined.

- [ ] **Step 3: Write the implementation**

Create `server/service/ion/parse.go`:

```go
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
```

- [ ] **Step 4: Run the test to verify it passes**

Run:

```shell
docker run --rm -v "$PWD/server:/src" -v nanokvm-gomod:/go/pkg/mod -w /src \
  -e CGO_ENABLED=0 golang:1.25 go test -tags novision ./service/ion/... -v
```

Expected: PASS, every subtest.

- [ ] **Step 5: Commit**

```bash
git add server/service/ion/parse.go server/service/ion/parse_test.go
git commit -m "Read the carveout summary without trusting its shape"
```

---

### Task 2: The reader, the peak reset, and degradation

The only file in the package that touches the filesystem. It measures the capture requirement instead of assuming it.

**Files:**
- Create: `server/service/ion/ion.go`
- Create: `server/service/ion/ion_test.go`

**Interfaces:**
- Consumes: everything Task 1 produced.
- Produces: `var Root string`, `type Status struct { Total, Used, Free uint64; UsageRate int; Generations int; Reserve uint64; Measured bool; Verdict string }`, `Init(reserveFloor uint64)`, `Read() Status`.

- [ ] **Step 1: Write the failing test**

Create `server/service/ion/ion_test.go`:

```go
package ion

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeCarveout builds a directory with the same file names as the real debugfs
// entry and points Root at it for the duration of the test.
func fakeCarveout(t *testing.T, total, alloc, peak uint64, summary string) string {
	t.Helper()

	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %s", name, err)
		}
	}

	write("total_mem", itoa(total))
	write("alloc_mem", itoa(alloc))
	write("peak", itoa(peak))
	if summary != "" {
		write("summary", summary)
	}

	original := Root
	Root = dir
	t.Cleanup(func() { Root = original })

	return dir
}

func itoa(v uint64) string {
	s := ""
	if v == 0 {
		return "0"
	}
	for v > 0 {
		s = string(rune('0'+v%10)) + s
		v /= 10
	}
	return s
}

func TestInitResetsPeakSoTheRequirementCanBeMeasured(t *testing.T) {
	dir := fakeCarveout(t, 78643200, 19050496, 19050496, summaryIdle)

	Init(25165824)

	body, err := os.ReadFile(filepath.Join(dir, "peak"))
	if err != nil {
		t.Fatalf("read peak: %s", err)
	}
	if got := string(body); got != "0" {
		t.Fatalf("peak = %q after Init, want %q", got, "0")
	}
}

func TestReadMeasuresTheReserveFromPeakGrowth(t *testing.T) {
	dir := fakeCarveout(t, 78643200, 19050496, 19050496, summaryIdle)
	Init(25165824)

	// The board captures: alloc and peak both reach the measured value.
	writeCounter(t, dir, "alloc_mem", 42942464)
	writeCounter(t, dir, "peak", 42942464)

	got := Read()

	// 42942464 - 19050496 = 23891968, the measured cost of one session.
	if got.Reserve != 23891968 {
		t.Fatalf("Reserve = %d, want 23891968", got.Reserve)
	}
	if !got.Measured {
		t.Fatal("Measured = false, want true once the growth exceeds the floor")
	}
}

func TestReadFallsBackToTheFloorBeforeAnythingHasBeenCaptured(t *testing.T) {
	fakeCarveout(t, 78643200, 19050496, 19050496, summaryIdle)
	Init(25165824)

	got := Read()

	if got.Reserve != 25165824 {
		t.Fatalf("Reserve = %d, want the floor 25165824", got.Reserve)
	}
	if got.Measured {
		t.Fatal("Measured = true, want false while the floor is in use")
	}
}

func TestReadReportsTheDerivedFields(t *testing.T) {
	fakeCarveout(t, 78643200, 19050496, 19050496, summaryIdle)
	Init(25165824)

	got := Read()

	if got.Total != 78643200 || got.Used != 19050496 {
		t.Fatalf("Total/Used = %d/%d, want 78643200/19050496", got.Total, got.Used)
	}
	if got.Free != 59592704 {
		t.Fatalf("Free = %d, want 59592704", got.Free)
	}
	if got.UsageRate != 24 {
		t.Fatalf("UsageRate = %d, want 24", got.UsageRate)
	}
	if got.Generations != 1 {
		t.Fatalf("Generations = %d, want 1", got.Generations)
	}
	if got.Verdict != VerdictOK {
		t.Fatalf("Verdict = %q, want %q", got.Verdict, VerdictOK)
	}
}

func TestReadCountsTheOrphanedGeneration(t *testing.T) {
	fakeCarveout(t, 78643200, 49459200, 49459200, summaryTwoGenerations)
	Init(25165824)

	if got := Read().Generations; got != 2 {
		t.Fatalf("Generations = %d, want 2", got)
	}
}

func TestMissingCountersAreUnavailableAndNotAnError(t *testing.T) {
	original := Root
	Root = filepath.Join(t.TempDir(), "not-here")
	t.Cleanup(func() { Root = original })

	Init(25165824)
	got := Read()

	if got.Verdict != VerdictUnavailable {
		t.Fatalf("Verdict = %q, want %q", got.Verdict, VerdictUnavailable)
	}
	if got.Total != 0 || got.Used != 0 {
		t.Fatalf("Total/Used = %d/%d, want 0/0", got.Total, got.Used)
	}
}

func TestAMissingSummaryStillReportsTheCounters(t *testing.T) {
	fakeCarveout(t, 78643200, 19050496, 19050496, "")
	Init(25165824)

	got := Read()

	if got.Generations != 0 {
		t.Fatalf("Generations = %d, want 0 when the summary cannot be read", got.Generations)
	}
	if got.Verdict == VerdictUnavailable {
		t.Fatal("Verdict is unavailable, want the counters to still be graded")
	}
}

func TestAReadOnlyPeakDoesNotBreakTheEndpoint(t *testing.T) {
	dir := fakeCarveout(t, 78643200, 19050496, 19050496, summaryIdle)
	if err := os.Chmod(filepath.Join(dir, "peak"), 0o444); err != nil {
		t.Fatalf("chmod: %s", err)
	}

	Init(25165824)
	got := Read()

	if got.Verdict == VerdictUnavailable {
		t.Fatal("Verdict is unavailable, want a graded reading from the floor")
	}
	if got.Measured {
		t.Fatal("Measured = true, want false when peak could not be reset")
	}
}

func writeCounter(t *testing.T, dir, name string, v uint64) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(itoa(v)), 0o644); err != nil {
		t.Fatalf("write %s: %s", name, err)
	}
}
```

Note on `TestAReadOnlyPeakDoesNotBreakTheEndpoint`: the container runs as root, and root ignores the write bit, so this case may not exercise what its name says. If the reset succeeds despite the `chmod`, change the test to point `Root` at a directory whose `peak` entry is a directory rather than a file — a write to that fails for root as well. Do not delete the case.

- [ ] **Step 2: Run the test to verify it fails**

Run:

```shell
docker run --rm -v "$PWD/server:/src" -v nanokvm-gomod:/go/pkg/mod -w /src \
  -e CGO_ENABLED=0 golang:1.25 go test -tags novision ./service/ion/...
```

Expected: FAIL — `Root`, `Status`, `Init` and `Read` are undefined.

- [ ] **Step 3: Write the implementation**

Create `server/service/ion/ion.go`:

```go
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

	// The floor stands until this process has been observed needing more.
	status.Reserve = floor
	if haveBase && reset {
		if peak, err := readCounter("peak"); err == nil && peak > base {
			if growth := peak - base; growth > floor {
				status.Reserve = growth
				status.Measured = true
			}
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
```

- [ ] **Step 4: Run the tests to verify they pass**

Run:

```shell
docker run --rm -v "$PWD/server:/src" -v nanokvm-gomod:/go/pkg/mod -w /src \
  -e CGO_ENABLED=0 golang:1.25 go test -tags novision ./service/ion/... -v
```

Expected: PASS, every case in both files.

- [ ] **Step 5: Commit**

```bash
git add server/service/ion/ion.go server/service/ion/ion_test.go
git commit -m "Measure what a capture session costs instead of assuming it"
```

---

### Task 3: Configuration, the endpoint, and the wiring

**Files:**
- Modify: `server/config/types.go`
- Modify: `server/config/default.go`
- Modify: `server/proto/vm.go`
- Create: `server/service/vm/ion.go`
- Modify: `server/router/vm.go`
- Modify: `server/main.go:82-92`
- Modify: `server/README.md`, `server/README_ZH.md`, `server/README_JA.md`

**Interfaces:**
- Consumes: `ion.Init(uint64)` and `ion.Read() ion.Status` from Task 2.
- Produces: `GET /api/vm/ion` returning `proto.GetIonRsp`, and `config.Ion{ReserveFloor uint64}`.

- [ ] **Step 1: Write the failing test**

Add to `server/config/default.go`'s existing test neighbour by creating `server/config/ion_test.go`:

```go
package config

import "testing"

// A server.yaml written before this feature existed has no ion block, so viper
// leaves the floor at zero. Zero would make every verdict unavailable, which is
// a silent loss of the whole feature on every existing device.
func TestAMissingIonBlockGetsTheDefaultFloor(t *testing.T) {
	original := instance
	t.Cleanup(func() { instance = original })

	instance = &Config{}
	checkDefaultValue()

	if instance.Ion.ReserveFloor != 25165824 {
		t.Fatalf("ReserveFloor = %d, want 25165824", instance.Ion.ReserveFloor)
	}
}

func TestAConfiguredFloorIsKept(t *testing.T) {
	original := instance
	t.Cleanup(func() { instance = original })

	instance = &Config{Ion: Ion{ReserveFloor: 12345678}}
	checkDefaultValue()

	if instance.Ion.ReserveFloor != 12345678 {
		t.Fatalf("ReserveFloor = %d, want the configured 12345678", instance.Ion.ReserveFloor)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run:

```shell
docker run --rm -v "$PWD/server:/src" -v nanokvm-gomod:/go/pkg/mod -w /src \
  -e CGO_ENABLED=0 golang:1.25 go test -tags novision ./config/...
```

Expected: FAIL — `Ion` is not a field of `Config`.

- [ ] **Step 3: Add the configuration**

In `server/config/types.go`, add the field to `Config` after `Security`:

```go
	Security       Security `yaml:"security"`
	Ion            Ion      `yaml:"ion"`
```

and the type beside the others:

```go
// Ion configures how the carveout is graded.
type Ion struct {
	// ReserveFloor is the assumed cost of one capture session, in bytes, used
	// until this process has been observed needing more. 24MB: one measured
	// session allocated 23,891,968 bytes on hardware.
	ReserveFloor uint64 `yaml:"reserveFloor"`
}
```

In `server/config/default.go`, add to `defaultConfig`:

```go
	Ion: Ion{
		ReserveFloor: 25165824,
	},
```

and inside `checkDefaultValue()`:

```go
	// A server.yaml written before this feature existed has no ion block, and a
	// zero floor would report every board as unavailable.
	if instance.Ion.ReserveFloor == 0 {
		instance.Ion.ReserveFloor = 25165824
	}
```

- [ ] **Step 4: Run the config test to verify it passes**

Run:

```shell
docker run --rm -v "$PWD/server:/src" -v nanokvm-gomod:/go/pkg/mod -w /src \
  -e CGO_ENABLED=0 golang:1.25 go test -tags novision ./config/... -v
```

Expected: PASS.

- [ ] **Step 5: Add the response struct**

In `server/proto/vm.go`, beside `GetMemoryLimitRsp`:

```go
type GetIonRsp struct {
	Total       uint64 `json:"total"`
	Used        uint64 `json:"used"`
	Free        uint64 `json:"free"`
	UsageRate   int    `json:"usageRate"`
	Generations int    `json:"generations"`
	Reserve     uint64 `json:"reserve"`
	Measured    bool   `json:"measured"`
	Verdict     string `json:"verdict"`
}
```

- [ ] **Step 6: Add the handler**

Create `server/service/vm/ion.go`:

```go
package vm

import (
	"NanoKVM-Server/proto"
	"NanoKVM-Server/service/ion"

	"github.com/gin-gonic/gin"
)

// GetIon reports the ION carveout state. It always succeeds: a carveout it
// cannot read comes back with the verdict "unavailable", because a diagnostic
// that fails the request would be worse than no diagnostic.
func (s *Service) GetIon(c *gin.Context) {
	var rsp proto.Response

	status := ion.Read()

	rsp.OkRspWithData(c, &proto.GetIonRsp{
		Total:       status.Total,
		Used:        status.Used,
		Free:        status.Free,
		UsageRate:   status.UsageRate,
		Generations: status.Generations,
		Reserve:     status.Reserve,
		Measured:    status.Measured,
		Verdict:     status.Verdict,
	})
}
```

- [ ] **Step 7: Register the route**

In `server/router/vm.go`, add beside the other read-only `vm` routes, before the reboot route:

```go
	api.GET("/vm/ion", service.GetIon) // get ION carveout state
```

- [ ] **Step 8: Initialise at startup**

In `server/main.go`, inside `run()`, immediately after `conf := config.GetInstance()`:

```go
	conf := config.GetInstance()

	// Record the carveout baseline and reset the peak watermark before anything
	// captures, so that later readings measure what this process needed.
	ion.Init(conf.Ion.ReserveFloor)
```

Add `"NanoKVM-Server/service/ion"` to the imports.

- [ ] **Step 9: Verify the whole tree builds and passes**

Run:

```shell
docker run --rm -v "$PWD/server:/src" -v nanokvm-gomod:/go/pkg/mod -w /src \
  -e CGO_ENABLED=0 golang:1.25 sh -c \
  'go vet -tags novision ./... && go test -tags novision ./... && \
   GOOS=linux GOARCH=riscv64 go build -tags novision ./...'
```

Expected: PASS, with no vet findings and a clean riscv64 build.

- [ ] **Step 10: Document the config block**

In `server/README.md`, add `ion` to the annotated `server.yaml` schema, next to the other optional blocks:

```yaml
ion:
    # The assumed cost of one capture session, in bytes. The server measures
    # the true cost while it runs and uses this value only before it has
    # captured anything. The default is 24MB.
    reserveFloor: 25165824
```

Write the surrounding prose to ASD-STE100: one instruction per sentence, active voice, stated subject. Mirror the same addition into `server/README_ZH.md` and `server/README_JA.md`.

- [ ] **Step 11: Commit**

```bash
git add server/config server/proto/vm.go server/service/vm/ion.go server/router/vm.go \
        server/main.go server/README.md server/README_ZH.md server/README_JA.md
git commit -m "Publish the carveout state on /api/vm/ion"
```

---

### Task 4: The supervisor records the carveout at each restart

A restart is what erodes the carveout, and the supervisor is the only component that outlives the server.

**Files:**
- Modify: `tools/service/S98supervise` (new marker block, and one call inside `full_restart`)
- Modify: `tools/service/test-supervise.sh`
- Modify: `tools/service/test-supervise-mutation.sh`
- Modify: `tools/README.md`

**Interfaces:**
- Consumes: the existing `log()` helper and the `# --- act ---` marker block convention.
- Produces: `ion_line()`, called from `full_restart()`.

- [ ] **Step 1: Write the failing test**

In `tools/service/test-supervise.sh`, follow the existing pattern: extract the block by marker, source it, stub what it calls. Add a new extraction beside the others, and these cases:

```sh
# --- ion line ---
sed -n '/^# --- ion ---$/,/^# --- end ion ---$/p' "$SCRIPT" > "$WORK/ion.sh"

ion_case() {   # $1 = name, $2 = fixture dir, $3 = expected line, or "" for none
    : > "$WORK/ion.log"
    (
        # ION_DIR is read by the block through its ${ION_DIR:-...} default, so it
        # is set inside the subshell that sources the block and nowhere else.
        ION_DIR=$2
        log() { echo "$*" >> "$WORK/ion.log"; }
        . "$WORK/ion.sh"
        ion_line
    )
    got=$(cat "$WORK/ion.log")
    if [ "$got" = "$3" ]; then
        printf '  %-60s OK\n' "$1"
    else
        printf '  %-60s FAIL (got %s, want %s)\n' "$1" "$got" "$3"
        FAILED=1
    fi
}

mkfixture() {   # $1 = dir, $2 = alloc, $3 = total, $4 = generations
    mkdir -p "$1"
    echo "$2" > "$1/alloc_mem"
    echo "$3" > "$1/total_mem"
    {
        echo "Details:"
        i=0
        while [ "$i" -lt "$4" ]; do
            echo "               0           294912         8bef4000                1 ISP_SHARED_BUFFER_0"
            i=$(( i + 1 ))
        done
        echo "minimum ion allocate unit = 4096"
    } > "$1/summary"
}

mkfixture "$WORK/ion-clean"  19050496 78643200 1
mkfixture "$WORK/ion-orphan" 49459200 78643200 2
mkfixture "$WORK/ion-zero"   19050496 0        1

ion_case "a healthy board reports one generation" \
    "$WORK/ion-clean"  "ion 19050496/78643200 24% gen=1"
ion_case "an orphaned generation is counted" \
    "$WORK/ion-orphan" "ion 49459200/78643200 62% gen=2"
# ion-absent is deliberately never created by mkfixture. A board without the
# debugfs entry must write no line at all.
ion_case "a missing carveout writes nothing" \
    "$WORK/ion-absent" ""
ion_case "a zero total writes nothing rather than dividing by it" \
    "$WORK/ion-zero"   ""

# A summary that cannot be read must not lose the counters.
rm -f "$WORK/ion-clean/summary"
ion_case "a missing summary still reports the counters" \
    "$WORK/ion-clean"  "ion 19050496/78643200 24% gen=0"
```

Then the wiring check, anchored to the call site and not to the name — a function that exists and is never called passed every case in this suite once already:

```sh
check "full_restart actually calls ion_line" \
    "$(sed -n '/^full_restart()/,/^}/p' "$SCRIPT" | grep -c '^[[:space:]]*ion_line$')" "1"
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `sh tools/service/test-supervise.sh`

Expected: FAIL — `ion.sh` is empty because the marker block does not exist, so `ion_line` is undefined and the wiring count is 0.

- [ ] **Step 3: Write the implementation**

In `tools/service/S98supervise`, add this block inside the `act` section, after `capture_bounded`:

```sh
# --- ion ---
# The carveout erodes with server restarts, not with uptime and not with capture
# cycles. A dead process keeps its whole ION working set - the buffers belong to
# the soph_* drivers, not to the process file descriptors - so every restart
# orphans a generation and allocates a second copy. Nothing in userspace frees
# it. Only a reboot does.
#
# The supervisor is the only component that outlives the server, so this is the
# only place the state at a restart can be recorded.
#
# Never read /proc/cvitek/vb here. It blocks forever in uninterruptible sleep and
# the reader cannot be killed. The files below are integers and a text table.
ION_DIR=${ION_DIR:-/sys/kernel/debug/ion/cvi_carveout_heap_dump}

ion_line() {
    [ -r "$ION_DIR/alloc_mem" ] || return 0

    used=$(cat "$ION_DIR/alloc_mem" 2>/dev/null)
    total=$(cat "$ION_DIR/total_mem" 2>/dev/null)

    case "$used"  in ''|*[!0-9]*) return 0 ;; esac
    case "$total" in ''|*[!0-9]*) return 0 ;; esac
    [ "$total" -gt 0 ] || return 0

    gen=$(grep -c 'ISP_SHARED_BUFFER_0' "$ION_DIR/summary" 2>/dev/null)
    case "$gen" in ''|*[!0-9]*) gen=0 ;; esac

    log "ion $used/$total $(( used * 100 / total ))% gen=$gen"
    return 0
}
# --- end ion ---
```

Then call it as the first statement of `full_restart()`, so the record describes the state that caused the restart:

```sh
full_restart() {
    ion_line
    ...
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `sh tools/service/test-supervise.sh`

Expected: PASS, all cases including the wiring check.

- [ ] **Step 5: Add the mutations**

In `tools/service/test-supervise-mutation.sh`, add these. Every one must be caught.

```sh
mutate "the readable guard is dropped" \
    's|\[ -r "\$ION_DIR/alloc_mem" \] || return 0|:|'
mutate "a non-numeric total is accepted" \
    's|case "\$total" in ..|*\[!0-9\]\*) return 0 ;; esac|:|'
mutate "a zero total is divided by" \
    's|\[ "\$total" -gt 0 \] || return 0|:|'
mutate "the generation buffer name is wrong" \
    's|ISP_SHARED_BUFFER_0|ISP_SHARED_BUFFER_1|'
mutate "the percentage is computed from the wrong operand" \
    's|used \* 100 / total|total \* 100 / used|'
mutate "full_restart stops calling ion_line" \
    's|^\([[:space:]]*\)ion_line$|\1:|'
```

Check each mutation's `sed` expression against the shipped file before relying on it. A mutation that does not change the file, or that leaves a file which does not parse, is rejected by the harness and must be rewritten rather than deleted.

- [ ] **Step 6: Run the mutation suite**

Run: `sh tools/service/test-supervise-mutation.sh`

Expected: every mutation caught, and the closing line `===== every mutation was caught =====`.

- [ ] **Step 7: Document it**

In `tools/README.md`, extend the `S98supervise` section with a short subsection. Keep to ASD-STE100. State: the line format; that it is written at each restart; that the carveout erodes with restarts and not with uptime; and that the reader never touches `/proc/cvitek/vb`. Give the example line:

```
2026-08-07 09:14:22 ion 31600640/78643200 40% gen=2
```

`tools/README.md` has no `_ZH` or `_JA` variant, so there is nothing to mirror.

- [ ] **Step 8: Check the line endings**

Run:

```shell
sh tools/test-line-endings.sh
git cat-file -p :tools/service/S98supervise | file -
```

Expected: no CRLF. `core.autocrlf` is `true`, so the worktree copy is not evidence — the staged blob is.

- [ ] **Step 9: Commit**

```bash
git add tools/service/S98supervise tools/service/test-supervise.sh \
        tools/service/test-supervise-mutation.sh tools/README.md
git commit -m "Record what the carveout held at every restart"
```

---

### Task 5: The web API call and the Settings row

The quiet surface. It never shouts.

**Files:**
- Modify: `web/src/api/vm.ts`
- Create: `web/src/pages/desktop/menu/settings/about/video-memory.tsx`
- Modify: `web/src/pages/desktop/menu/settings/about/index.tsx`
- Modify: `web/src/i18n/locales/en.ts`

**Interfaces:**
- Consumes: `GET /api/vm/ion` from Task 3.
- Produces: `getIon()` in `@/api/vm.ts`, and the exported `IonStatus` type that Task 6 also uses:

```ts
export type IonStatus = {
  total: number;
  used: number;
  free: number;
  usageRate: number;
  generations: number;
  reserve: number;
  measured: boolean;
  verdict: 'ok' | 'warn' | 'critical' | 'unavailable';
};
```

Put that type in `web/src/pages/desktop/menu/settings/about/video-memory.tsx` and export it. Task 6 imports it from there rather than declaring a second copy.

- [ ] **Step 1: Add the API call**

In `web/src/api/vm.ts`, beside `getMemoryLimit`:

```ts
// get ION carveout state
export function getIon() {
  return http.get('/api/vm/ion');
}
```

- [ ] **Step 2: Add the strings**

In `web/src/i18n/locales/en.ts`, inside `settings.about`, after `deviceKey`:

```ts
        videoMemory: 'Video Memory',
        videoMemoryTip:
          'Memory reserved for video capture. It is not shared with the rest of the system.',
        videoMemoryGenerations_one: '{{count}} server restart is holding video memory',
        videoMemoryGenerations_other: '{{count}} server restarts are holding video memory',
        videoMemoryReboot: 'Reboot to reclaim it.',
```

- [ ] **Step 3: Write the component**

Create `web/src/pages/desktop/menu/settings/about/video-memory.tsx`:

```tsx
import { useEffect, useState } from 'react';
import { Tooltip } from 'antd';
import { CircleHelpIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/vm.ts';

export type IonStatus = {
  total: number;
  used: number;
  free: number;
  usageRate: number;
  generations: number;
  reserve: number;
  measured: boolean;
  verdict: 'ok' | 'warn' | 'critical' | 'unavailable';
};

function megabytes(bytes: number) {
  return `${Math.round(bytes / (1024 * 1024))} MB`;
}

export const VideoMemory = () => {
  const { t } = useTranslation();
  const [status, setStatus] = useState<IonStatus>();

  useEffect(() => {
    api
      .getIon()
      .then((rsp: any) => {
        if (rsp.code !== 0) {
          console.log(rsp.msg);
          return;
        }
        setStatus(rsp.data);
      })
      .catch((err) => console.log(err));
  }, []);

  // A board that cannot report its carveout shows nothing at all. A diagnostic
  // is not worth a row that says it does not work.
  if (!status || status.verdict === 'unavailable') {
    return null;
  }

  const toneClass =
    status.verdict === 'critical'
      ? 'text-red-400'
      : status.verdict === 'warn'
        ? 'text-amber-400'
        : '';

  return (
    <div className="flex w-full items-start justify-between">
      <div className="flex items-center space-x-1">
        <span>{t('settings.about.videoMemory')}</span>
        <Tooltip title={t('settings.about.videoMemoryTip')}>
          <CircleHelpIcon size={14} className="text-neutral-500" />
        </Tooltip>
      </div>

      <div className="flex flex-col items-end space-y-1">
        <span className={toneClass}>
          {megabytes(status.used)} / {megabytes(status.total)} ({status.usageRate}%)
        </span>

        {status.generations > 1 && (
          <span className="text-xs text-neutral-500">
            {t('settings.about.videoMemoryGenerations', { count: status.generations - 1 })}{' '}
            {t('settings.about.videoMemoryReboot')}
          </span>
        )}
      </div>
    </div>
  );
};
```

Note the `- 1`: one generation is the live process, so the count of *orphaned* generations is one fewer.

- [ ] **Step 4: Place the row**

In `web/src/pages/desktop/menu/settings/about/information.tsx`, import `VideoMemory` and render it as the last child of the existing `<div className="mt-5 flex w-full flex-col space-y-5">` list, after the device key row.

- [ ] **Step 5: Verify it compiles and lints**

Run:

```shell
cd web && pnpm build && pnpm lint
```

Expected: a clean `tsc` pass, a successful Vite build, and no eslint findings.

- [ ] **Step 6: Commit**

```bash
git add web/src/api/vm.ts web/src/pages/desktop/menu/settings/about web/src/i18n/locales/en.ts
git commit -m "Show the carveout in Settings without shouting about it"
```

---

### Task 6: The desktop gate and the badge

The loud surface. It exists only when something is wrong, and it lands before the stream starts.

**Files:**
- Create: `web/src/pages/desktop/ion-status/use-ion-status.ts`
- Create: `web/src/pages/desktop/ion-status/overlay.tsx`
- Create: `web/src/pages/desktop/ion-status/index.ts`
- Modify: `web/src/pages/desktop/index.tsx:86-90`
- Modify: `web/src/i18n/locales/en.ts`

**Interfaces:**
- Consumes: `getIon()` from `@/api/vm.ts` and `IonStatus` from Task 5's `video-memory.tsx`.
- Produces: `useIonStatus()` returning `{ status: IonStatus | null; holdStream: boolean; continueAnyway: () => void }`, plus `IonWarningBadge` and `IonCriticalGate`.

- [ ] **Step 1: Add the strings**

In `web/src/i18n/locales/en.ts`, add a top-level `ion` section beside the others:

```ts
    ion: {
      warn: 'Video memory is low. One server restart would exhaust it. Reboot when convenient.',
      criticalTitle: 'Not enough video memory to start the stream',
      criticalBody:
        'Starting video would exhaust the reserved memory and stop the server. Every other function still works, including power control and reboot. Only a reboot of NanoKVM reclaims this memory.',
      criticalContinue: 'Start video anyway',
      criticalReboot: 'Reboot NanoKVM'
    },
```

- [ ] **Step 2: Write the hook**

Create `web/src/pages/desktop/ion-status/use-ion-status.ts`:

```ts
import { useEffect, useState } from 'react';

import * as api from '@/api/vm.ts';

import type { IonStatus } from '../menu/settings/about/video-memory';

// The check must land before the stream starts. At the critical verdict, opening
// the stream is the event that kills the server, so a warning that arrives after
// the crash has no value.
//
// The stream is held while the request is in flight, and released whether the
// request succeeds or fails. A broken endpoint must not cost the operator their
// video.
export function useIonStatus() {
  const [status, setStatus] = useState<IonStatus | null>(null);
  const [loading, setLoading] = useState(true);
  const [overridden, setOverridden] = useState(false);

  useEffect(() => {
    let live = true;

    api
      .getIon()
      .then((rsp: any) => {
        if (!live) return;
        if (rsp.code === 0) {
          setStatus(rsp.data);
        }
      })
      .catch((err) => console.log(err))
      .finally(() => {
        if (live) setLoading(false);
      });

    return () => {
      live = false;
    };
  }, []);

  const holdStream = loading || (status?.verdict === 'critical' && !overridden);

  return {
    status,
    holdStream,
    continueAnyway: () => setOverridden(true)
  };
}
```

- [ ] **Step 3: Write the components**

Create `web/src/pages/desktop/ion-status/overlay.tsx`:

```tsx
import { AlertCircle, AlertTriangle } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import * as api from '@/api/vm.ts';

import type { IonStatus } from '../menu/settings/about/video-memory';

export function IonWarningBadge({ status }: { status: IonStatus | null }) {
  const { t } = useTranslation();

  if (status?.verdict !== 'warn') {
    return null;
  }

  return (
    <div className="pointer-events-none absolute left-1/2 top-4 z-10 flex max-w-[calc(100%-2rem)] -translate-x-1/2 items-center gap-2.5 rounded-lg border border-amber-500/50 bg-neutral-900/90 px-4 py-2.5 text-sm font-medium text-amber-400 shadow-xl shadow-amber-900/20 backdrop-blur-md">
      <AlertTriangle className="h-5 w-5 shrink-0" />
      <span className="min-w-0">{t('ion.warn')}</span>
    </div>
  );
}

export function IonCriticalGate({ onContinue }: { onContinue: () => void }) {
  const { t } = useTranslation();

  function reboot() {
    api.reboot().catch((err: unknown) => console.log(err));
  }

  return (
    <div className="absolute inset-0 z-10 flex items-center justify-center bg-black/80 p-6">
      <div className="flex max-w-md flex-col gap-4 rounded-lg border border-red-500/50 bg-neutral-900/95 p-6 shadow-xl">
        <div className="flex items-center gap-2.5 text-red-400">
          <AlertCircle className="h-5 w-5 shrink-0" />
          <span className="font-medium">{t('ion.criticalTitle')}</span>
        </div>

        <p className="text-sm text-neutral-300">{t('ion.criticalBody')}</p>

        <div className="flex justify-end gap-3">
          <button
            className="rounded px-3 py-1.5 text-sm text-neutral-400 hover:text-neutral-200"
            onClick={onContinue}
          >
            {t('ion.criticalContinue')}
          </button>
          <button
            className="rounded bg-red-600 px-3 py-1.5 text-sm text-white hover:bg-red-500"
            onClick={reboot}
          >
            {t('ion.criticalReboot')}
          </button>
        </div>
      </div>
    </div>
  );
}
```

`api.reboot()` already exists in `web/src/api/vm.ts` for `POST /api/vm/system/reboot`. Use it. Do not add a second one.

Create `web/src/pages/desktop/ion-status/index.ts`:

```ts
export { IonCriticalGate, IonWarningBadge } from './overlay';
export { useIonStatus } from './use-ion-status';
```

- [ ] **Step 4: Gate the stream**

In `web/src/pages/desktop/index.tsx`, add the import:

```tsx
import { IonCriticalGate, IonWarningBadge, useIonStatus } from './ion-status';
```

and the hook beside `useCaptureStatus`:

```tsx
  const ion = useIonStatus();
```

Then replace the panel body. `Menu`, `Mouse` and `Keyboard` are siblings and stay mounted, which is the point: holding the stream keeps every other way of rescuing the board.

```tsx
                <div className="relative h-full min-h-0 w-full min-w-0 overflow-hidden bg-black">
                  {ion.holdStream ? (
                    ion.status?.verdict === 'critical' && (
                      <IonCriticalGate onContinue={ion.continueAnyway} />
                    )
                  ) : (
                    <>
                      <Screen />
                      <CaptureStatusOverlay status={captureStatus} />
                      <IonWarningBadge status={ion.status} />
                    </>
                  )}
                </div>
```

- [ ] **Step 5: Verify it compiles and lints**

Run:

```shell
cd web && pnpm build && pnpm lint
```

Expected: a clean `tsc` pass, a successful Vite build, and no eslint findings.

- [ ] **Step 6: Check the gate against a mocked critical verdict**

Run `pnpm mocked`, add a handler in `web/src/mocks` for `GET /api/vm/ion` that returns `verdict: "critical"`, and confirm in the browser that the gate appears, that the menu still opens, and that "Start video anyway" reveals the stream. Then change the handler to `verdict: "warn"` and confirm the badge appears over a working stream.

Record what you saw in the task report. This is the only check that proves the ordering requirement, and no unit test covers it.

- [ ] **Step 7: Commit**

```bash
git add web/src/pages/desktop/ion-status web/src/pages/desktop/index.tsx web/src/i18n/locales/en.ts
git commit -m "Hold the stream when starting it would kill the server"
```

---

### Task 7: Hardware acceptance

Nothing before this proves the feature on a device. Every step states what to run and what result counts as a pass.

**Files:**
- Modify: none. Record the results in the task report.

**Interfaces:**
- Consumes: everything.

The device is at `10.0.0.222`. It reboots twice during this task, so do not run it while the KVM is in use.

- [ ] **Step 1: Deploy**

Build and install the server with `tools/deploy/deploy-server`, which stages on `/data`, restarts, probes and restores the previous binary by itself. Build the web UI with `pnpm build`, rename `web/dist` to `web`, and upload it to `/kvmapp/server/`. Install `S98supervise` to `/etc/init.d/` **only**.

Confirm the running binary is the one you built:

```shell
ssh root@10.0.0.222 'strings -a /proc/$(pidof NanoKVM-Server)/exe | grep -o "dev\.[0-9]\{8\}\.[0-9]*\.[0-9a-f]*" | head -1'
```

Pass: the stamp matches your build. The file's own stamp proves nothing — the old server survives `killall` and keeps answering.

- [ ] **Step 2: Reboot to a clean carveout**

```shell
ssh root@10.0.0.222 'reboot'
```

Wait for it to answer, then:

```shell
ssh root@10.0.0.222 'cat /sys/kernel/debug/ion/cvi_carveout_heap_dump/alloc_mem'
```

Pass: `19050496`.

- [ ] **Step 3: Read the endpoint**

Call `GET /api/vm/ion` with a valid session or API key.

Pass: `total` is `78643200`, `used` agrees with `alloc_mem` read over ssh in the same minute, `generations` is `1`, `verdict` is `"ok"`, and `measured` is `false` because nothing has captured yet.

- [ ] **Step 4: Confirm the peak reset happened**

```shell
ssh root@10.0.0.222 'cat /sys/kernel/debug/ion/cvi_carveout_heap_dump/peak'
```

Pass: the value is below `alloc_mem`, or it equals the current `alloc_mem` if something has already allocated since startup. A `peak` equal to a historic high-water from before the server started is a **fail** — it means `Init` did not write.

- [ ] **Step 5: Measure the reserve**

Open the KVM in a browser, let video run for thirty seconds, then read the endpoint again.

Pass: `used` has risen, `measured` is now `true`, and `reserve` equals `peak - 19050496` computed from the two files over ssh.

- [ ] **Step 6: Prove `generations` counts restarts — twice**

The spec requires two restarts, not one. The relationship between `ISP_SHARED_BUFFER_0` and a server process rests on a single observation, and one more restart is what turns it into a rule.

```shell
ssh root@10.0.0.222 '/etc/init.d/S95nanokvm restart'
```

Read the endpoint. Pass: `generations` is `2`.

Repeat the restart once more and read again. Pass: `generations` is `3`.

**If the second restart does not give `3`,** the assumption is wrong. Stop, report it, and change `Read` to report `Generations: 0` rather than a number that cannot be defended. Do not adjust the test to match whatever the board did.

- [ ] **Step 7: Confirm the supervisor line**

```shell
ssh root@10.0.0.222 'grep " ion " /data/supervise.log | tail -5'
```

Pass: one line for each restart the supervisor performed, in the documented format, with `gen=` rising.

- [ ] **Step 8: Run the shell suites on the device**

```shell
ssh root@10.0.0.222 'sh /tmp/test-supervise.sh; sh /tmp/test-supervise-mutation.sh'
```

Pass: all cases pass and every mutation is caught, under the device's own `ash`.

- [ ] **Step 9: Reach a real warn verdict**

Keep restarting the server until `free` falls below twice `reserve`. Read the endpoint after each restart.

Pass: `verdict` becomes `"warn"`, and reloading the KVM page shows the amber badge over working video.

**Watch the headroom.** Stop before `free` falls below `reserve` unless you intend Step 10 immediately, because at that point opening the stream kills the server until a reboot.

- [ ] **Step 10: Reach a real critical verdict and confirm the gate**

Restart once more so that `free` falls below `reserve`.

Pass: `verdict` is `"critical"`; reloading the KVM page shows the gate and no video; the menu still opens; power control still responds; and the "Reboot NanoKVM" button reboots the board.

This is the one step that proves the whole feature, because it is the only one where the warning arrives before the crash rather than after it.

- [ ] **Step 11: Confirm recovery**

After the reboot, read the endpoint.

Pass: `alloc_mem` is `19050496`, `generations` is `1`, `verdict` is `"ok"`, and the web UI shows video with no badge and no gate.

- [ ] **Step 12: Confirm the board is left healthy**

```shell
ssh root@10.0.0.222 '/etc/init.d/S98supervise status'
```

Pass: `verdict : healthy`, `answering : yes`, `server : up`.

---

## Notes for the implementer

**The measurements are the specification.** Every number in this plan came off the device on 2026-08-06, and the spec records how. If a value looks arbitrary, read the spec before changing it. A "tidier" constant would be wrong: the working set is cumulative over which delivery paths the board has used, which is exactly why the server measures it rather than assuming it.

**`generations` is the weakest claim here.** It rests on one restart. Task 7 Step 6 is where it becomes a rule or gets withdrawn, and withdrawing it is an acceptable outcome — reporting a number we cannot defend is not.

**Nothing in this plan rebuilds `libkvm`.** Making the carveout survive a failed allocation is sub-project (c). Fixing the leak is sub-project (d). If a task seems to need either, it has gone out of scope.
