package common

import (
	"strings"
	"sync"
	"testing"

	log "github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
)

// A healthy board reads frames and nothing else happens. The tracker starts in
// that state, so a working pipeline never reports anything at all.
func TestCaptureReadLogSaysNothingWhileReadsSucceed(t *testing.T) {
	var l captureReadLog

	for i := range 600 {
		if changed, _, _ := l.note(captureReadOK); changed {
			t.Fatalf("expected read %d to be unremarkable", i)
		}
	}
}

// The first failure is the line worth having.
func TestCaptureReadLogReportsTheFirstFailure(t *testing.T) {
	var l captureReadLog

	changed, previous, _ := l.note(-1)
	if !changed {
		t.Fatal("expected the first failure to be reported")
	}
	if previous != captureReadOK {
		t.Fatalf("expected the previous outcome to be a success, got %d", previous)
	}
}

// The reason this type exists. A monitor that is switched off makes every read
// return the same failure, once per frame, for as long as a viewer is
// connected. One line describes that; six hundred describe it no better and
// fill the tmpfs the restart path needs.
func TestCaptureReadLogStaysQuietWhileAFailureHolds(t *testing.T) {
	var l captureReadLog

	l.note(-1)

	for i := range 600 {
		if changed, _, _ := l.note(-1); changed {
			t.Fatalf("expected repeat %d to be suppressed", i)
		}
	}
}

// Recovery is one line, and it carries how long the failure lasted. That is the
// part a per-frame line buries.
func TestCaptureReadLogReportsRecoveryWithTheRunLength(t *testing.T) {
	var l captureReadLog

	l.note(-1)
	for range 600 {
		l.note(-1)
	}

	changed, previous, previousRun := l.note(captureReadOK)
	if !changed {
		t.Fatal("expected the recovery to be reported")
	}
	if previous != -1 {
		t.Fatalf("expected the previous outcome to be -1, got %d", previous)
	}
	if previousRun != 601 {
		t.Fatalf("expected a run of 601 failed reads, got %d", previousRun)
	}
}

// H.264 reads return 3 for a keyframe and 0 otherwise, so the raw result
// changes at every GOP boundary. Callers collapse success to one value before
// noting it, and the tracker must then see no change at all - otherwise the
// flood this type removes comes back once per keyframe.
func TestCaptureReadLogTreatsEverySuccessAlike(t *testing.T) {
	var l captureReadLog

	l.note(captureReadOK)

	for _, raw := range []int{0, 3, 0, 0, 3, 0} {
		if changed, _, _ := l.note(captureReadOutcome(raw)); changed {
			t.Fatalf("expected raw success %d to read as unchanged", raw)
		}
	}
}

// A failure that clears and returns is two episodes, not one.
func TestCaptureReadLogReportsAFailureThatReturns(t *testing.T) {
	var l captureReadLog

	for _, result := range []int{-1, captureReadOK, -1} {
		if changed, _, _ := l.note(result); !changed {
			t.Fatalf("expected outcome %d to be reported", result)
		}
	}
}

// Distinct failures are distinct events.
func TestCaptureReadLogReportsEachDistinctFailure(t *testing.T) {
	var l captureReadLog

	for _, result := range []int{-1, -3, -1} {
		if changed, _, _ := l.note(result); !changed {
			t.Fatalf("expected result %d to be reported", result)
		}
	}
}

// The streamers read on their own goroutines, so the tracker is shared.
func TestCaptureReadLogIsSafeUnderConcurrentUse(t *testing.T) {
	var l captureReadLog
	var wg sync.WaitGroup

	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 200 {
				l.note(captureReadOutcome(i%3 - 1))
			}
		}()
	}

	wg.Wait()
}

// captureLogs collects what the package writes to the global logger.
func captureLogs(t *testing.T) *test.Hook {
	t.Helper()

	hook := test.NewGlobal()
	t.Cleanup(hook.Reset)

	return hook
}

// The whole point, stated as the caller sees it: a failure that repeats every
// frame is one line, not one line per frame.
func TestReportCaptureReadWritesOneLinePerEpisode(t *testing.T) {
	hook := captureLogs(t)
	var l captureReadLog

	for range 600 {
		reportCaptureRead(&l, -1)
	}

	if len(hook.Entries) != 1 {
		t.Fatalf("expected one line for 600 identical failures, got %d", len(hook.Entries))
	}

	entry := hook.Entries[0]
	if entry.Level != log.ErrorLevel {
		t.Fatalf("expected the failure at error level, got %s", entry.Level)
	}
	if !strings.Contains(entry.Message, "-1") {
		t.Fatalf("expected the result code in the message, got %q", entry.Message)
	}
}

// A working pipeline is silent, including across the keyframe boundaries that
// change the raw result.
func TestReportCaptureReadSaysNothingWhileReadsSucceed(t *testing.T) {
	hook := captureLogs(t)
	var l captureReadLog

	for _, raw := range []int{0, 3, 0, 0, 3, 5, 0} {
		reportCaptureRead(&l, raw)
	}

	if len(hook.Entries) != 0 {
		t.Fatalf("expected silence from a working pipeline, got %d lines: %v", len(hook.Entries), hook.Entries)
	}
}

// Recovery is worth one line, and it says how long the outage lasted.
func TestReportCaptureReadReportsRecoveryOnce(t *testing.T) {
	hook := captureLogs(t)
	var l captureReadLog

	for range 600 {
		reportCaptureRead(&l, -1)
	}
	for range 10 {
		reportCaptureRead(&l, 0)
	}

	if len(hook.Entries) != 2 {
		t.Fatalf("expected a failure line and a recovery line, got %d", len(hook.Entries))
	}

	recovery := hook.Entries[1]
	if recovery.Level != log.InfoLevel {
		t.Fatalf("expected the recovery at info level, got %s", recovery.Level)
	}
	if !strings.Contains(recovery.Message, "600") {
		t.Fatalf("expected the run length in the message, got %q", recovery.Message)
	}
}
