package vm

import (
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"NanoKVM-Server/config"
	"NanoKVM-Server/proto"
)

func (s *Service) SetGpio(c *gin.Context) {
	var req proto.SetGpioReq
	var rsp proto.Response

	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, fmt.Sprintf("invalid arguments: %s", err))
		return
	}

	device := ""
	conf := config.GetInstance().Hardware

	switch req.Type {
	case "power":
		device = conf.GPIOPower
	case "reset":
		device = conf.GPIOReset
	default:
		rsp.ErrRsp(c, -2, fmt.Sprintf("invalid power event: %s", req.Type))
		return
	}

	if err := writeGpio(device, pressDuration(req.Duration)); err != nil {
		rsp.ErrRsp(c, -3, fmt.Sprintf("operation failed: %s", err))
		return
	}

	log.Debugf("gpio %s set successfully", device)
	rsp.OkRsp(c)
}

func (s *Service) GetGpio(c *gin.Context) {
	var rsp proto.Response

	conf := config.GetInstance().Hardware

	pwr, err := readGpio(conf.GPIOPowerLED)
	if err != nil {
		rsp.ErrRsp(c, -2, fmt.Sprintf("failed to read power led: %s", err))
		return
	}

	hdd := false
	if conf.Version == config.HWVersionAlpha {
		hdd, err = readGpio(conf.GPIOHDDLed)
		if err != nil {
			rsp.ErrRsp(c, -2, fmt.Sprintf("failed to read hdd led: %s", err))
			return
		}
	}

	data := &proto.GetGpioRsp{
		PWR: pwr,
		HDD: hdd,
	}
	rsp.OkRspWithData(c, data)
}

const (
	defaultPressDuration = 800 * time.Millisecond
	maxPressDuration     = 10 * time.Second
)

// pressDuration is how long the line is held for a request.
//
// The value arrives from the client, and the line stays asserted for the whole
// of it, so an unbounded one holds the attached machine in reset for as long as
// the caller cares to name. Nobody presses a power button for more than a few
// seconds.
func pressDuration(milliseconds uint) time.Duration {
	if milliseconds == 0 {
		return defaultPressDuration
	}

	duration := time.Duration(milliseconds) * time.Millisecond
	if duration > maxPressDuration || duration < 0 {
		return maxPressDuration
	}

	return duration
}

var (
	gpioLocksMutex sync.Mutex
	gpioLocks      = map[string]*sync.Mutex{}
)

// gpioLock returns the lock for one line. Presses of the same line have to be
// serialized: interleaved, one press releases the line while the other still
// believes it is holding it, so a reset becomes a no-op or the line is left
// asserted after both callers have gone. Separate lines are independent.
func gpioLock(device string) *sync.Mutex {
	gpioLocksMutex.Lock()
	defer gpioLocksMutex.Unlock()

	lock, ok := gpioLocks[device]
	if !ok {
		lock = &sync.Mutex{}
		gpioLocks[device] = lock
	}

	return lock
}

func writeGpio(device string, duration time.Duration) error {
	lock := gpioLock(device)
	lock.Lock()
	defer lock.Unlock()

	if err := os.WriteFile(device, []byte("1"), 0o666); err != nil {
		log.Errorf("write gpio %s failed: %s", device, err)
		return err
	}

	time.Sleep(duration)

	if err := os.WriteFile(device, []byte("0"), 0o666); err != nil {
		log.Errorf("write gpio %s failed: %s", device, err)
		return err
	}

	return nil
}

func readGpio(device string) (bool, error) {
	content, err := os.ReadFile(device)
	if err != nil {
		log.Errorf("read gpio %s failed: %s", device, err)
		return false, err
	}

	contentStr := string(content)
	if len(contentStr) > 1 {
		contentStr = contentStr[:len(contentStr)-1]
	}

	value, err := strconv.Atoi(contentStr)
	if err != nil {
		log.Errorf("invalid gpio content: %s", content)
		return false, nil
	}

	return value == 0, nil
}
