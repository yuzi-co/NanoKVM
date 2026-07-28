package tailscale

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"NanoKVM-Server/utils"

	log "github.com/sirupsen/logrus"
)

const (
	OriginalURL = "https://pkgs.tailscale.com/stable/tailscale_latest_riscv64.tgz"
	Workspace   = "/root/.tailscale"

	// packageHost pins where the binaries may come from. The download URL is
	// whatever the redirect points at, and these binaries run as root.
	packageHost = "pkgs.tailscale.com"

	// checksumSuffix is the digest the release server publishes next to each
	// versioned package. It only exists for the resolved, versioned name.
	checksumSuffix = ".sha256"

	// maxPackageSize caps the download; the release is around 34MB and the
	// rootfs lives on the SD card.
	maxPackageSize = 128 * 1024 * 1024

	downloadTimeout = 10 * time.Minute
	checksumTimeout = 30 * time.Second
)

func isInstalled() bool {
	_, err1 := os.Stat(TailscalePath)
	_, err2 := os.Stat(TailscaledPath)

	return err1 == nil && err2 == nil
}

func install() error {
	_ = os.MkdirAll(Workspace, 0o755)
	defer func() {
		_ = os.RemoveAll(Workspace)
	}()

	tarFile := fmt.Sprintf("%s/tailscale_riscv64.tgz", Workspace)

	// download
	if err := download(tarFile); err != nil {
		log.Errorf("failed to download tailscale: %s", err)
		return err
	}

	// decompress
	dir, err := utils.UnTarGz(tarFile, Workspace)
	if err != nil {
		log.Errorf("failed to decompress tailscale: %s", err)
		return err
	}

	// move
	tailscalePath := fmt.Sprintf("%s/tailscale", dir)
	err = utils.MoveFile(tailscalePath, TailscalePath)
	if err != nil {
		log.Errorf("failed to move tailscale: %s", err)
		return err
	}

	tailscaledPath := fmt.Sprintf("%s/tailscaled", dir)
	err = utils.MoveFile(tailscaledPath, TailscaledPath)
	if err != nil {
		log.Errorf("failed to move tailscaled: %s", err)
		return err
	}

	log.Debugf("install tailscale successfully")
	return nil
}

func download(target string) error {
	url, err := getDownloadURL()
	if err != nil {
		log.Errorf("failed to get Tailscale download url: %s", err)
		return err
	}

	// Fetch the digest first: there is no point pulling 34MB that is going to
	// be thrown away, and a missing digest means the package cannot be trusted.
	expected, err := fetchChecksum(url + checksumSuffix)
	if err != nil {
		log.Errorf("failed to get Tailscale checksum: %s", err)
		return err
	}

	if err := fetchPackage(url, target); err != nil {
		return err
	}

	if err := verifyFileSHA256(target, expected); err != nil {
		_ = os.Remove(target)
		log.Errorf("Tailscale package failed verification: %s", err)

		return err
	}

	log.Debugf("download Tailscale successfully")
	return nil
}

func fetchPackage(url string, target string) error {
	resp, err := utils.OutboundClient(downloadTimeout).Get(url)
	if err != nil {
		log.Errorf("failed to download Tailscale: %s", err)
		return err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	if resp.ContentLength > maxPackageSize {
		return fmt.Errorf("package is too large: %d bytes", resp.ContentLength)
	}

	// Nothing has verified this yet, so it must not be executable.
	out, err := os.OpenFile(target, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		log.Errorf("failed to create file: %s", err)
		return err
	}
	defer func() {
		_ = out.Close()
	}()

	written, err := io.Copy(out, io.LimitReader(resp.Body, maxPackageSize+1))
	if err != nil {
		log.Errorf("failed to copy response body to file: %s", err)
		return err
	}

	if written > maxPackageSize {
		return fmt.Errorf("package is too large")
	}

	return nil
}

// fetchChecksum reads the digest the release server publishes next to the
// package.
func fetchChecksum(url string) ([]byte, error) {
	if err := checkDownloadHost(url); err != nil {
		return nil, err
	}

	resp, err := utils.OutboundClient(checksumTimeout).Get(url)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return nil, err
	}

	return parseSHA256Checksum(string(body))
}

func parseSHA256Checksum(body string) ([]byte, error) {
	// The file holds the digest, optionally followed by the file name.
	digest, _, _ := strings.Cut(strings.TrimSpace(body), " ")

	sum, err := hex.DecodeString(strings.TrimSpace(digest))
	if err != nil || len(sum) != sha256.Size {
		return nil, fmt.Errorf("failed to parse sha256 checksum")
	}

	return sum, nil
}

func verifyFileSHA256(path string, expected []byte) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() {
		_ = file.Close()
	}()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return err
	}

	if !bytes.Equal(hasher.Sum(nil), expected) {
		return fmt.Errorf("sha256 mismatch")
	}

	return nil
}

// checkDownloadHost keeps the resolved URL on the release host over TLS.
func checkDownloadHost(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid download url: %w", err)
	}

	if parsed.Scheme != "https" {
		return fmt.Errorf("refusing a non-https download url")
	}

	if !strings.EqualFold(parsed.Hostname(), packageHost) {
		return fmt.Errorf("refusing a download from %q", parsed.Hostname())
	}

	return nil
}

// getDownloadURL resolves the "latest" alias to the versioned package. A HEAD
// is enough: a GET would pull the whole archive only to discard it and fetch
// it again.
func getDownloadURL() (string, error) {
	resp, err := utils.OutboundClient(checksumTimeout).Head(OriginalURL)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusFound {
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	resolved := resp.Request.URL.String()
	if err := checkDownloadHost(resolved); err != nil {
		return "", err
	}

	return resolved, nil
}
