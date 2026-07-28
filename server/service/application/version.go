package application

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"NanoKVM-Server/proto"
	"NanoKVM-Server/utils"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

// maxPackageSize caps an update package. The rootfs lives on the SD card, so
// an unbounded download fills the device it boots from.
const maxPackageSize = 512 * 1024 * 1024

// manifestTimeout bounds the latest.json fetch.
const manifestTimeout = 30 * time.Second

// maxManifestSize caps the manifest body itself.
const maxManifestSize = 64 * 1024

type Latest struct {
	Version string `json:"version"`
	Name    string `json:"name"`
	Sha512  string `json:"sha512"`
	Size    uint   `json:"size"`
	Url     string `json:"url"`
}

func (s *Service) GetVersion(c *gin.Context) {
	var rsp proto.Response

	// current version
	currentVersion := "1.0.0"

	versionFile := fmt.Sprintf("%s/version", AppDir)
	if version, err := os.ReadFile(versionFile); err == nil {
		currentVersion = strings.ReplaceAll(string(version), "\n", "")
	}

	log.Debugf("current version: %s", currentVersion)

	// latest version
	latestVersion := ""
	latest, err := getLatest()
	if err == nil && latest != nil {
		latestVersion = latest.Version
	}

	rsp.OkRspWithData(c, &proto.GetVersionRsp{
		Current: currentVersion,
		Latest:  latestVersion,
	})
}

func getLatest() (*Latest, error) {
	baseURL := StableURL
	if isPreviewEnabled() {
		baseURL = PreviewURL
	}

	url := fmt.Sprintf("%s/latest.json?now=%d", baseURL, time.Now().Unix())

	// The UI calls this, so a wedged server would otherwise pin a goroutine
	// and a socket for every version check.
	client := &http.Client{Timeout: manifestTimeout}

	resp, err := client.Get(url)
	if err != nil {
		log.Debugf("failed to request version: %v", err)
		return nil, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxManifestSize))
	if err != nil {
		log.Errorf("failed to read response: %v", err)
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		log.Errorf("server responded with status code: %d", resp.StatusCode)
		return nil, fmt.Errorf("status code %d", resp.StatusCode)
	}

	latest, err := parseLatest(body, baseURL)
	if err != nil {
		return nil, err
	}

	log.Debugf("get application latest version: %s", latest.Version)
	return latest, nil
}

// parseLatest reads the manifest and refuses anything it would not be safe to
// act on. The name decides both the URL and where the package is written, and
// it is written before the checksum has had a chance to reject the package, so
// it has to be a plain file name.
func parseLatest(body []byte, baseURL string) (*Latest, error) {
	var latest Latest
	if err := json.Unmarshal(body, &latest); err != nil {
		log.Errorf("failed to unmarshal response: %s", err)
		return nil, err
	}

	if !utils.IsSafeFileName(latest.Name) {
		log.Errorf("refusing update package name: %q", latest.Name)
		return nil, fmt.Errorf("invalid package name")
	}

	if latest.Size > maxPackageSize {
		log.Errorf("refusing update package of %d bytes", latest.Size)
		return nil, fmt.Errorf("package is too large")
	}

	latest.Url = fmt.Sprintf("%s/%s", baseURL, latest.Name)

	return &latest, nil
}
