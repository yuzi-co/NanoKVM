package router

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

const scriptBody = "console.log('nanokvm');"

// webRootWithGzip writes name plus a gzipped copy and returns the root and the
// exact bytes the compressed file holds.
func webRootWithGzip(t *testing.T, name string, body string) (string, []byte) {
	t.Helper()

	dir := t.TempDir()
	full := filepath.Join(dir, filepath.FromSlash(name))

	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("failed to create the directory: %s", err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatalf("failed to write the file: %s", err)
	}

	var buf bytes.Buffer
	writer := gzip.NewWriter(&buf)
	if _, err := writer.Write([]byte(body)); err != nil {
		t.Fatalf("failed to compress: %s", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("failed to close the writer: %s", err)
	}

	if err := os.WriteFile(full+".gz", buf.Bytes(), 0o644); err != nil {
		t.Fatalf("failed to write the compressed file: %s", err)
	}

	return dir, buf.Bytes()
}

func getWithEncoding(t *testing.T, r *gin.Engine, path string, encoding string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, path, nil)
	if encoding != "" {
		req.Header.Set("Accept-Encoding", encoding)
	}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	return w
}

func TestPrecompressedAssetIsServedWhenTheClientAcceptsGzip(t *testing.T) {
	// Compressing on the fly would cost more CPU than the C906 has to spare,
	// so the build ships the compressed copy alongside the original.
	dir, compressed := webRootWithGzip(t, "assets/app.js", scriptBody)
	r := newTestEngine(t, dir)

	w := getWithEncoding(t, r, "/assets/app.js", "gzip")

	if !bytes.Equal(w.Body.Bytes(), compressed) {
		t.Fatalf("expected the precompressed file, got %d bytes", w.Body.Len())
	}

	if got := w.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("expected Content-Encoding gzip, got %q", got)
	}

	if got := w.Header().Get("Content-Type"); !strings.Contains(got, "javascript") {
		t.Fatalf("expected a javascript content type, got %q", got)
	}
}

func TestPlainAssetIsServedWhenTheClientCannotDecompress(t *testing.T) {
	dir, _ := webRootWithGzip(t, "assets/app.js", scriptBody)
	r := newTestEngine(t, dir)

	w := getWithEncoding(t, r, "/assets/app.js", "")

	if w.Body.String() != scriptBody {
		t.Fatalf("expected the plain file, got %q", w.Body.String())
	}

	if got := w.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("expected no Content-Encoding, got %q", got)
	}
}

func TestPlainAssetIsServedWhenThereIsNoCompressedCopy(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatalf("failed to create the directory: %s", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "app.js"), []byte(scriptBody), 0o644); err != nil {
		t.Fatalf("failed to write the file: %s", err)
	}

	r := newTestEngine(t, dir)

	w := getWithEncoding(t, r, "/assets/app.js", "gzip")

	if w.Body.String() != scriptBody {
		t.Fatalf("expected the plain file, got %q", w.Body.String())
	}
	if got := w.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("expected no Content-Encoding, got %q", got)
	}
}

func TestRootServesThePrecompressedIndex(t *testing.T) {
	dir, compressed := webRootWithGzip(t, "index.html", "<html></html>")
	r := newTestEngine(t, dir)

	w := getWithEncoding(t, r, "/", "gzip")

	if !bytes.Equal(w.Body.Bytes(), compressed) {
		t.Fatalf("expected the precompressed index, got %d bytes", w.Body.Len())
	}
}

func TestCompressedLookupCannotEscapeTheWebRoot(t *testing.T) {
	dir, _ := webRootWithGzip(t, "assets/app.js", scriptBody)

	outside := filepath.Join(filepath.Dir(dir), "outside.js")
	if err := os.WriteFile(outside+".gz", []byte("secret"), 0o644); err != nil {
		t.Fatalf("failed to write the file: %s", err)
	}
	t.Cleanup(func() { _ = os.Remove(outside + ".gz") })

	r := newTestEngine(t, dir)

	w := getWithEncoding(t, r, "/../outside.js", "gzip")

	if strings.Contains(w.Body.String(), "secret") {
		t.Fatal("expected the lookup to stay inside the web root")
	}
}

func TestVaryIsSetSoCachesDoNotMixEncodings(t *testing.T) {
	dir, _ := webRootWithGzip(t, "assets/app.js", scriptBody)
	r := newTestEngine(t, dir)

	for _, encoding := range []string{"gzip", ""} {
		w := getWithEncoding(t, r, "/assets/app.js", encoding)
		if got := w.Header().Get("Vary"); !strings.Contains(got, "Accept-Encoding") {
			t.Fatalf("expected Vary to mention Accept-Encoding for encoding %q, got %q", encoding, got)
		}
	}
}

func TestCompressedCopyIsNotServedDirectly(t *testing.T) {
	// Handing back a .gz without the header would make the browser download it.
	dir, _ := webRootWithGzip(t, "assets/app.js", scriptBody)
	r := newTestEngine(t, dir)

	w := getWithEncoding(t, r, "/assets/app.js.gz", "gzip")

	if got := w.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("expected an explicit encoding on a .gz request, got %q", got)
	}
}
