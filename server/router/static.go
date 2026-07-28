package router

import (
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	"NanoKVM-Server/utils"
)

const (
	gzipSuffix    = ".gz"
	gzipEncoding  = "gzip"
	indexFileName = "index.html"
)

const (
	// assetsPrefix is where the build puts content-hashed files.
	assetsPrefix = "/assets/"

	// immutableCache is only sound for a content-hashed name: changing the
	// file changes the name, so a cached copy can never be the wrong one.
	immutableCache = "public, max-age=31536000, immutable"

	// noCache covers everything else, index.html above all. Revalidation is
	// not enough here: the packaged files carry no meaningful modification
	// time, so a conditional request can be answered "unchanged" after an
	// update and leave a stale UI in place.
	noCache = "no-store"
)

// cacheControlFor picks a caching policy from the request path alone, so it
// applies whichever branch below ends up answering.
func cacheControlFor(urlPath string) string {
	clean := path.Clean("/" + strings.TrimPrefix(urlPath, "/"))
	if strings.HasPrefix(clean, assetsPrefix) {
		return immutableCache
	}

	return noCache
}

// servePrecompressed answers with the .gz sibling the build produced, when the
// client can decompress it. Compressing on the fly would cost more CPU than the
// board has to spare, and the assets never change between requests. Reports
// whether the request was answered.
func servePrecompressed(c *gin.Context, webPath string) bool {
	if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
		return false
	}

	if !acceptsGzip(c.Request.Header.Get("Accept-Encoding")) {
		return false
	}

	target, ok := resolveWebFile(webPath, c.Request.URL.Path)
	if !ok {
		return false
	}

	// A request for the compressed copy itself still has to be labelled, or
	// the browser downloads it instead of running it.
	plain := strings.TrimSuffix(target, gzipSuffix)

	compressed := plain + gzipSuffix
	if info, err := os.Stat(compressed); err != nil || !info.Mode().IsRegular() {
		return false
	}

	if contentType := mime.TypeByExtension(filepath.Ext(plain)); contentType != "" {
		c.Header("Content-Type", contentType)
	}
	c.Header("Content-Encoding", gzipEncoding)

	c.File(compressed)
	c.Abort()

	return true
}

// resolveWebFile maps a URL path to a file inside webPath, or reports that it
// leaves the root.
func resolveWebFile(webPath string, urlPath string) (string, bool) {
	// Cleaning an absolute path drops any traversal before it is joined.
	clean := path.Clean("/" + strings.TrimPrefix(urlPath, "/"))
	if strings.HasSuffix(clean, "/") || clean == "/" {
		clean = path.Join(clean, indexFileName)
	}

	target := filepath.Join(webPath, filepath.FromSlash(clean))
	if !utils.IsPathInside(webPath, target) {
		return "", false
	}

	return target, true
}

// acceptsGzip reports whether the header offers gzip without refusing it.
func acceptsGzip(header string) bool {
	for _, part := range strings.Split(header, ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(name), gzipEncoding) {
			continue
		}

		// "gzip;q=0" means the client would rather not.
		if strings.ReplaceAll(strings.TrimSpace(params), " ", "") == "q=0" {
			return false
		}

		return true
	}

	return false
}
