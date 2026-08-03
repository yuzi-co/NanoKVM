package router

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
)

func newTestEngine(t *testing.T, webPath string) *gin.Engine {
	t.Helper()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(staticHandler(webPath))

	return r
}

func get(t *testing.T, r *gin.Engine, path string) *httptest.ResponseRecorder {
	t.Helper()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))

	return w
}

func TestStaticFilesAreStillServed(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatalf("failed to create the directory: %s", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "app.js"), []byte("web ui"), 0o644); err != nil {
		t.Fatalf("failed to write the file: %s", err)
	}

	r := newTestEngine(t, dir)

	if body := get(t, r, "/assets/app.js").Body.String(); body != "web ui" {
		t.Fatalf("expected the static file to be served, got %q", body)
	}
}

func TestAPIPathsNeverReachTheFilesystem(t *testing.T) {
	// The static middleware runs an os.Stat before routing, so every API call
	// and websocket upgrade pays a syscall against the SD card. Worse, a file
	// that happens to sit at the same path shadows the real handler.
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "api", "vm"), 0o755); err != nil {
		t.Fatalf("failed to create the directory: %s", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "api", "vm", "info"), []byte("decoy"), 0o644); err != nil {
		t.Fatalf("failed to write the file: %s", err)
	}

	r := newTestEngine(t, dir)
	r.GET("/api/vm/info", func(c *gin.Context) {
		c.String(http.StatusOK, "handler")
	})

	if body := get(t, r, "/api/vm/info").Body.String(); body != "handler" {
		t.Fatalf("expected the API handler to answer, got %q", body)
	}
}

func TestUnknownAPIPathsAreNotServedFromDisk(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "api"), 0o755); err != nil {
		t.Fatalf("failed to create the directory: %s", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "api", "secret"), []byte("leak"), 0o644); err != nil {
		t.Fatalf("failed to write the file: %s", err)
	}

	r := newTestEngine(t, dir)

	w := get(t, r, "/api/secret")
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d with body %q", w.Code, w.Body.String())
	}
}
