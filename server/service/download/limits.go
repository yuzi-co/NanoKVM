package download

import (
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"syscall"
	"time"

	"NanoKVM-Server/utils"
)

// reservedFreeBytes is kept back so a download cannot fill the card the device
// boots from. Running out of space there takes down the whole system, not just
// the download.
const reservedFreeBytes = 256 * 1024 * 1024

// imageDir is where mountable images live.
const imageDir = "/data"

var errNotEnoughSpace = errors.New("not enough free space for this image")

// imageClient bounds the parts of a transfer that should never take long.
//
// Deliberately no Client.Timeout: that caps the whole body, and a legitimate
// ISO can take an hour over 100M ethernet. These bound a server that never
// answers, which is the case that would otherwise hang forever.
var imageClient = &http.Client{
	Transport: &http.Transport{
		Proxy:                 utils.ProxyFromConfig,
		TLSHandshakeTimeout:   30 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		IdleConnTimeout:       90 * time.Second,
	},
}

// imageFilenameFromURL takes the name the image will be stored under, held to
// the same rules as an uploaded one.
func imageFilenameFromURL(rawURL string) (string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", errors.New("invalid url")
	}

	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.Path == "" {
		return "", errors.New("invalid url")
	}

	filename := filepath.Base(parsed.Path)
	if err := validateISOFilename(filename); err != nil {
		return "", err
	}

	return filename, nil
}

// fitsOnDisk reports whether a body of the declared size can be stored without
// eating the reserved headroom. A size of -1 means the server did not say.
func fitsOnDisk(size int64, available int64) bool {
	if size < 0 {
		return true
	}

	return size <= available-reservedFreeBytes
}

// availableBytes reports the free space on the filesystem holding path.
func availableBytes(path string) (int64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}

	return int64(stat.Bavail) * int64(stat.Bsize), nil
}
