package application

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	buildversion "NanoKVM-Server/common/version"
	"NanoKVM-Server/proto"
	"NanoKVM-Server/utils"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

// maxPackageSize caps an update package. The rootfs lives on the SD card, so
// an unbounded download fills the device it boots from.
const maxPackageSize = 512 * 1024 * 1024

type Latest struct {
	Version string `json:"version"`
	Name    string `json:"name"`
	Sha512  string `json:"sha512"`
	Size    uint64 `json:"size"`
	Url     string `json:"-"`
}

const (
	maxLatestJSONSize = 64 * 1024
)

var (
	latestClient       = utils.NewUpdateHTTPClient(15 * time.Second)
	packageNamePattern = regexp.MustCompile(`^nanokvm_[0-9]+\.[0-9]+\.[0-9]+\.tar\.gz$`)
	versionPattern     = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)
)

// versionFile is written by the updater. Tests point it elsewhere.
var versionFile = fmt.Sprintf("%s/version", AppDir)

// currentVersion reports the installed application version, carrying the stamp
// of the binary serving it.
func currentVersion() string {
	version := "1.0.0"

	if content, err := os.ReadFile(versionFile); err == nil {
		version = strings.ReplaceAll(string(content), "\n", "")
	}

	return buildversion.Decorate(version)
}

func (s *Service) GetVersion(c *gin.Context) {
	var rsp proto.Response

	currentVersion := currentVersion()

	log.Debugf("current version: %s", currentVersion)

	// latest version
	latestVersion := ""
	latest, err := getLatest()
	if err != nil {
		log.Errorf("failed to get latest version: %s", err)
		rsp.ErrRsp(c, -1, "failed to query latest version")
		return
	}
	latestVersion = latest.Version

	rsp.OkRspWithData(c, &proto.GetVersionRsp{
		Current: currentVersion,
		Latest:  latestVersion,
	})
}

func getLatest() (*Latest, error) {
	baseURL, err := resolveUpdateBaseURL()
	if err != nil {
		return nil, err
	}

	// latestClient carries both the proxy and the redirect rule, so the manifest
	// fetch reaches a custom update server the same way the download does.
	manifestURL, err := joinUpdateURL(baseURL, "latest.json")
	if err != nil {
		return nil, err
	}
	parsedManifestURL, err := url.Parse(manifestURL)
	if err != nil {
		return nil, err
	}
	query := parsedManifestURL.Query()
	query.Set("now", fmt.Sprintf("%d", time.Now().Unix()))
	parsedManifestURL.RawQuery = query.Encode()

	request, err := utils.NewAuthenticatedRequest("GET", parsedManifestURL.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := latestClient.Do(request)
	if err != nil {
		log.Debugf("failed to request version from %s", parsedManifestURL.Redacted())
		return nil, errors.New("update server is inaccessible")
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxLatestJSONSize+1))
	if err != nil {
		log.Errorf("failed to read response: %v", err)
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		log.Errorf("server responded with status code: %d", resp.StatusCode)
		return nil, fmt.Errorf("status code %d", resp.StatusCode)
	}
	if len(body) > maxLatestJSONSize {
		return nil, fmt.Errorf("latest manifest exceeds %d bytes", maxLatestJSONSize)
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
	if err := validateLatest(&latest); err != nil {
		return nil, err
	}

	// validateLatest has already refused any name that is not
	// nanokvm_X.Y.Z.tar.gz, so the name cannot carry a path separator by the
	// time it reaches the URL or the file on disk. The size is the part it
	// does not bound: the package is written to the SD card the device boots
	// from, so a manifest may not ask for more than the card can take.
	if latest.Size > maxPackageSize {
		log.Errorf("refusing update package of %d bytes", latest.Size)
		return nil, fmt.Errorf("package is too large")
	}

	joined, err := joinUpdateURL(baseURL, latest.Name)
	if err != nil {
		return nil, err
	}
	latest.Url = joined

	return &latest, nil
}

func joinUpdateURL(baseURL string, element string) (string, error) {
	joined, err := url.JoinPath(baseURL, element)
	if err != nil {
		return "", fmt.Errorf("join update URL: %w", err)
	}
	return joined, nil
}

func validateLatest(latest *Latest) error {
	if !versionPattern.MatchString(latest.Version) {
		return errors.New("invalid latest version")
	}
	if !packageNamePattern.MatchString(latest.Name) {
		return errors.New("invalid update package name")
	}
	digest, err := base64.StdEncoding.DecodeString(latest.Sha512)
	if err != nil || len(digest) != 64 {
		return errors.New("invalid update package sha512")
	}
	if latest.Size == 0 {
		return errors.New("invalid update package size")
	}
	return nil
}
