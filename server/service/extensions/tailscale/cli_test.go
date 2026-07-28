//go:build linux

package tailscale

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

const loginLine = "To authenticate, visit:\n\n\thttps://login.tailscale.com/a/abcdef\n\n"

func waitForFile(t *testing.T, path string, within time.Duration) bool {
	t.Helper()

	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}

	return false
}

func TestLoginReturnsTheURL(t *testing.T) {
	cmd := exec.Command("sh", "-c", fmt.Sprintf("printf %q >&2", loginLine))

	url, err := loginURL(cmd, 5*time.Second)
	if err != nil {
		t.Fatalf("expected the url to be read: %s", err)
	}

	if url != "https://login.tailscale.com/a/abcdef" {
		t.Fatalf("unexpected url %q", url)
	}
}

func TestLoginLeavesTheCommandRunning(t *testing.T) {
	// tailscale login keeps running until the user finishes in the browser.
	// Closing the pipe as soon as the URL is read hands the child a SIGPIPE on
	// its next line and kills the login it was told to complete.
	marker := filepath.Join(t.TempDir(), "finished")
	script := fmt.Sprintf("printf %q >&2; sleep 0.4; echo still-here >&2; touch %q", loginLine, marker)

	cmd := exec.Command("sh", "-c", script)

	if _, err := loginURL(cmd, 5*time.Second); err != nil {
		t.Fatalf("expected the url to be read: %s", err)
	}

	if !waitForFile(t, marker, 3*time.Second) {
		t.Fatal("expected the login command to run to completion")
	}
}

func TestLoginGivesUpWhenNoURLAppears(t *testing.T) {
	// Otherwise the HTTP handler blocks for the command's whole ten minutes.
	// Production runs the binary directly, so the test does too: killing a
	// shell wrapper would leave the real command holding the pipe.
	cmd := exec.Command("sleep", "30")

	start := time.Now()
	_, err := loginURL(cmd, 300*time.Millisecond)

	if err == nil {
		t.Fatal("expected an error when no url is printed")
	}

	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("expected loginURL to give up quickly, took %s", elapsed)
	}
}

func TestLoginReapsACommandThatGaveUp(t *testing.T) {
	// An abandoned command must be waited on, or it lingers as a zombie for
	// the life of the server, one per login attempt.
	//
	// Asked of the OS rather than of cmd.ProcessState, which Wait writes from
	// another goroutine.
	cmd := exec.Command("sleep", "30")

	if _, err := loginURL(cmd, 200*time.Millisecond); err == nil {
		t.Fatal("expected an error when no url is printed")
	}

	pid := cmd.Process.Pid

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		// A killed but unreaped child is still a zombie, and signal 0 finds
		// it. Once Wait has run, the pid is gone.
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	t.Fatal("expected the abandoned command to be reaped")
}
