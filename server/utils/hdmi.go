package utils

import (
	"os"
	"strconv"
	"strings"

	log "github.com/sirupsen/logrus"
)

const (
	HDMIDisableFile           = "/etc/kvm/hdmi_disable"
	HDMIIdleTimeoutFile       = "/etc/kvm/hdmi_idle_timeout"
	DefaultHDMIIdleTimeout    = 0
	MaxHDMIIdleTimeoutMinutes = 7 * 24 * 60

	// HDMISignalFile is written by the capture library: 1 once it is getting
	// frames off the port, 0 when it cannot. That is the physical presence of
	// a signal, which is not the same thing as capture being switched on.
	HDMISignalFile = "/kvmapp/kvm/state"
)

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

func PersistHDMIIdleTimeout(minutes int) {
	if err := os.WriteFile(HDMIIdleTimeoutFile, []byte(strconv.Itoa(minutes)), 0644); err != nil {
		log.Error("failed to persist hdmi idle timeout:", err)
	}
}

func GetHDMIIdleTimeout() int {
	data, err := os.ReadFile(HDMIIdleTimeoutFile)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Error("failed to read hdmi idle timeout:", err)
		}
		return DefaultHDMIIdleTimeout
	}

	minutes, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || minutes < 0 || minutes > MaxHDMIIdleTimeoutMinutes {
		log.Error("invalid hdmi idle timeout")
		return DefaultHDMIIdleTimeout
	}

	return minutes
}
