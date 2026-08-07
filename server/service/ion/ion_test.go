package ion

import (
	"os"
	"path/filepath"
	"strconv"
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

	write("total_mem", strconv.FormatUint(total, 10))
	write("alloc_mem", strconv.FormatUint(alloc, 10))
	write("peak", strconv.FormatUint(peak, 10))
	if summary != "" {
		write("summary", summary)
	}

	original := Root
	Root = dir
	t.Cleanup(func() { Root = original })

	return dir
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

	// The board captures a stream on top of what it already held: alloc and
	// peak both reach the two-generation value.
	writeCounter(t, dir, "alloc_mem", 49459200)
	writeCounter(t, dir, "peak", 49459200)

	got := Read()

	// 49459200 - 19050496 = 30408704, above the 25165824 floor.
	if got.Reserve != 30408704 {
		t.Fatalf("Reserve = %d, want 30408704", got.Reserve)
	}
	if !got.Measured {
		t.Fatal("Measured = false, want true once the growth exceeds the floor")
	}
}

// TestTheFloorWinsWhileTheMeasurementIsBelowIt documents the asymmetry that
// TestReadMeasuresTheReserveFromPeakGrowth's numbers used to obscure: a
// measurement is a lower bound over the paths exercised so far, not an upper
// bound, so a smaller-than-floor measurement must not replace the floor.
//
// One full capture session measured 23,891,968 bytes on hardware, which is
// just under the 24MB floor. A board that has exercised only the paths seen
// so far therefore keeps the floor, and that is intended: the measurement
// cannot bound a path the board has not used yet.
func TestTheFloorWinsWhileTheMeasurementIsBelowIt(t *testing.T) {
	dir := fakeCarveout(t, 78643200, 19050496, 19050496, summaryIdle)
	Init(25165824)

	writeCounter(t, dir, "alloc_mem", 42942464)
	writeCounter(t, dir, "peak", 42942464)

	got := Read()

	if got.Reserve != 25165824 {
		t.Fatalf("Reserve = %d, want the floor 25165824", got.Reserve)
	}
	if got.Measured {
		t.Fatal("Measured = true, want false while growth stays below the floor")
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
	if err := os.Remove(filepath.Join(dir, "peak")); err != nil {
		t.Fatalf("remove peak: %s", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "peak"), 0o755); err != nil {
		t.Fatalf("mkdir peak: %s", err)
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
	body := []byte(strconv.FormatUint(v, 10))
	if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
		t.Fatalf("write %s: %s", name, err)
	}
}
