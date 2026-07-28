package utils

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	log "github.com/sirupsen/logrus"
)

// DefaultDownloadTimeout bounds a whole download. Without it a hostile or
// wedged server pins the goroutine and the socket for the life of the process.
const DefaultDownloadTimeout = 10 * time.Minute

// ErrDownloadTooLarge is returned when a body runs past the caller's limit.
var ErrDownloadTooLarge = errors.New("download exceeds the maximum size")

type DownloadOptions struct {
	// MaxBytes caps the body. Required: the rootfs lives on the SD card, so an
	// unbounded body fills the device.
	MaxBytes int64

	// Timeout defaults to DefaultDownloadTimeout.
	Timeout time.Duration
}

func Download(req *http.Request, target string, opts DownloadOptions) error {
	if opts.MaxBytes <= 0 {
		return errors.New("download size limit is required")
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultDownloadTimeout
	}

	log.Debugf("downloading %s to %s", req.URL.String(), target)

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		log.Errorf("create dir %s err: %s", filepath.Dir(target), err)
		return err
	}

	resp, err := OutboundClient(timeout).Do(req)
	if err != nil {
		log.Errorf("request error: %s", err)
		return err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		log.Errorf("request failed, status code: %d", resp.StatusCode)
		return errors.New("update website is inaccessible right now")
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType != "application/octet-stream" && contentType != "application/zip" && contentType != "application/gzip" {
		log.Debugf("unexpected content-type, it should be either octet-stream or (g)zip, but got: %s", contentType)
		return errors.New("unsupported content type")
	}

	if resp.ContentLength > opts.MaxBytes {
		log.Errorf("declared size %d exceeds the %d byte limit", resp.ContentLength, opts.MaxBytes)
		return ErrDownloadTooLarge
	}

	// Nothing has verified this file yet, so it must not be executable.
	out, err := os.OpenFile(target, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		log.Errorf("cannot create file '%s', error: %s", target, err)
		return err
	}

	if err := copyWithin(out, resp.Body, opts.MaxBytes); err != nil {
		_ = out.Close()
		_ = os.Remove(target)

		return err
	}

	return out.Close()
}

// copyWithin copies at most max bytes and fails if the source has more, so a
// server that lies about Content-Length cannot fill the disk.
func copyWithin(dst io.Writer, src io.Reader, max int64) error {
	written, err := io.Copy(dst, io.LimitReader(src, max+1))
	if err != nil {
		log.Errorf("download failed: %s", err)
		return fmt.Errorf("download failed: %w", err)
	}

	if written > max {
		log.Errorf("download exceeded the %d byte limit", max)
		return ErrDownloadTooLarge
	}

	return nil
}
