package utils

import (
	"fmt"
	"math"
	"os"
	"runtime/debug"
	"strconv"
	"strings"

	log "github.com/sirupsen/logrus"
)

// GoMemLimitFile is a var so tests can point it somewhere writable.
var GoMemLimitFile = "/etc/kvm/GOMEMLIMIT"

// minGoMemLimitMB is the floor below which the runtime spends its time in the
// collector rather than serving. SetGoMemLimit applies it but writes the value
// it was given, so restoring has to apply the same floor: without it a limit
// stored below the floor comes back unclamped on the next boot, and the server
// runs against a limit its own setter would have refused.
const minGoMemLimitMB = 50

func InitGoMemLimit() {
	if !IsGoMemLimitExist() {
		return
	}

	limit, err := GetGoMemLimit()
	if err != nil {
		return
	}

	memoryLimit := max(limit, minGoMemLimitMB)
	debug.SetMemoryLimit(memoryLimit * 1024 * 1024)
	log.Debugf("set GOMEMLIMIT to %d MB", memoryLimit)
}

func SetGoMemLimit(limit int64) error {
	memoryLimit := max(limit, minGoMemLimitMB)
	debug.SetMemoryLimit(memoryLimit * 1024 * 1024)

	log.Debugf("set GOMEMLIMIT to %d MB", limit)

	data := []byte(fmt.Sprintf("%d", limit))
	err := os.WriteFile(GoMemLimitFile, data, 0o644)
	if err != nil {
		log.Errorf("failed to write GOMEMLIMIT: %s", err)
		return err
	}

	return nil
}

func GetGoMemLimit() (int64, error) {
	data, err := os.ReadFile(GoMemLimitFile)
	if err != nil {
		log.Errorf("failed to read GOMEMLIMIT: %s", err)
		return 0, err
	}

	content := strings.TrimSpace(string(data))
	limit, err := strconv.ParseInt(content, 10, 64)
	if err != nil {
		log.Errorf("failed to parse GOMEMLIMIT: %s", err)
		return 0, err
	}

	return limit, nil
}

func DelGoMemLimit() error {
	// math.MaxInt64 is Go's "no limit" value. A literal 1GB is a real cap on
	// the 1GB boards, which is the opposite of turning the limit off.
	debug.SetMemoryLimit(math.MaxInt64)

	err := os.Remove(GoMemLimitFile)
	if err != nil {
		log.Errorf("failed to delete GOMEMLIMIT: %s", err)
		return err
	}

	return nil
}

func IsGoMemLimitExist() bool {
	_, err := os.Stat(GoMemLimitFile)
	return err == nil
}
