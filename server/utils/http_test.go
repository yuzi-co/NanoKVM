package utils

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func octetStreamServer(t *testing.T, body []byte) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)

	return server
}

func request(t *testing.T, url string) *http.Request {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("failed to build the request: %s", err)
	}

	return req
}

func TestDownloadWritesABodyWithinTheLimit(t *testing.T) {
	server := octetStreamServer(t, []byte("firmware"))
	target := filepath.Join(t.TempDir(), "app.tar.gz")

	if err := Download(request(t, server.URL), target, DownloadOptions{MaxBytes: 1024}); err != nil {
		t.Fatalf("expected the download to succeed: %s", err)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("expected the file to exist: %s", err)
	}

	if string(data) != "firmware" {
		t.Fatalf("expected the body, got %q", data)
	}
}

func TestDownloadRefusesABodyOverTheLimit(t *testing.T) {
	// The rootfs lives on the SD card, so an oversized body fills the device.
	server := octetStreamServer(t, make([]byte, 4096))
	target := filepath.Join(t.TempDir(), "app.tar.gz")

	err := Download(request(t, server.URL), target, DownloadOptions{MaxBytes: 1024})
	if err == nil {
		t.Fatal("expected an oversized body to be refused")
	}

	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Fatal("expected the partial file to be cleaned up")
	}
}

func TestDownloadGivesUpOnAStalledServer(t *testing.T) {
	// Without a timeout a hostile or wedged server pins the goroutine forever.
	stalled := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(30 * time.Second):
		}
	}))
	t.Cleanup(stalled.Close)

	target := filepath.Join(t.TempDir(), "app.tar.gz")

	start := time.Now()
	err := Download(request(t, stalled.URL), target, DownloadOptions{
		MaxBytes: 1024,
		Timeout:  300 * time.Millisecond,
	})

	if err == nil {
		t.Fatal("expected the stalled download to fail")
	}

	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("expected the download to give up quickly, took %s", elapsed)
	}
}

func TestDownloadedFileIsNotExecutable(t *testing.T) {
	// The file exists before anything has verified it, so it must not be
	// something the system will happily run.
	server := octetStreamServer(t, []byte("firmware"))
	target := filepath.Join(t.TempDir(), "app.tar.gz")

	if err := Download(request(t, server.URL), target, DownloadOptions{MaxBytes: 1024}); err != nil {
		t.Fatalf("expected the download to succeed: %s", err)
	}

	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("expected the file to exist: %s", err)
	}

	if info.Mode().Perm()&0o111 != 0 {
		t.Fatalf("expected a non-executable file, got mode %o", info.Mode().Perm())
	}
}
