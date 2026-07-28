package utils

import (
	"os"
	"strconv"
	"strings"

	log "github.com/sirupsen/logrus"
)

const (
	HDMIDisableFile = "/etc/kvm/hdmi_disable"

	// HDMISignalFile is written by the capture library: 1 once it is getting
	// frames off the port, 0 when it cannot. That is the physical presence of
	// a signal, which is not the same thing as capture being switched on.
	HDMISignalFile = "/kvmapp/kvm/state"
)

// HDMIIdleTimeoutFile holds how many minutes without a viewer capture should
// keep running. A variable rather than a constant so tests can redirect it.
var HDMIIdleTimeoutFile = "/etc/kvm/hdmi_idle_timeout"

// PersistHDMIIdleTimeout remembers the idle timeout across a restart.
func PersistHDMIIdleTimeout(minutes int) {
	if minutes < 0 {
		minutes = 0
	}

	if err := os.WriteFile(HDMIIdleTimeoutFile, []byte(strconv.Itoa(minutes)), 0o644); err != nil {
		log.Error("failed to persist hdmi idle timeout:", err)
	}
}

// GetHDMIIdleTimeout reports the configured idle timeout in minutes. Zero
// means capture is never stopped, which is what an unconfigured device gets:
// switching it off by itself would look like a fault.
func GetHDMIIdleTimeout() int {
	data, err := os.ReadFile(HDMIIdleTimeoutFile)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Error("failed to read hdmi idle timeout:", err)
		}
		return 0
	}

	minutes, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || minutes < 0 {
		return 0
	}

	return minutes
}

// HasHDMISignal reports whether the port is currently carrying a picture.
// Callers use this to tell a sleeping machine from an awake one.
func HasHDMISignal() bool {
	return readHDMISignal(HDMISignalFile)
}

func readHDMISignal(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Error("failed to read hdmi signal state:", err)
		}
		return false
	}

	return strings.TrimSpace(string(data)) == "1"
}

func PersistHDMIDisabled() {
	f, err := os.OpenFile(HDMIDisableFile, os.O_CREATE|os.O_RDONLY, 0644)
	if err != nil {
		log.Error("failed to create hdmi disable file:", err)
		return
	}
	f.Close()
}

func PersistHDMIEnabled() {
	if err := os.Remove(HDMIDisableFile); err != nil {
		log.Error("failed to remove hdmi disable file:", err)
		return
	}
}

func IsHdmiDisabled() bool {
	if _, err := os.Stat(HDMIDisableFile); err != nil {
		if os.IsNotExist(err) {
			return false // HDMI is enabled
		}
		log.Error("failed to check hdmi disable file:", err)
		return false // Assume HDMI is enabled on error
	}
	return true // HDMI is disabled
}
