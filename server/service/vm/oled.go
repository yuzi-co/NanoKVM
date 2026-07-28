package vm

import (
	"NanoKVM-Server/proto"
	"os"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

const (
	OLEDExistFile = "/etc/kvm/oled_exist"
	OLEDSleepFile = "/etc/kvm/oled_sleep"
)

const (
	// minSleepSeconds mirrors OLED_SLEEP_DELAY_MIN in kvm_system. Anything
	// below it disables the screen saver instead of shortening it.
	minSleepSeconds = 10

	// maxSleepSeconds is the largest value kvm_system can hold: it parses the
	// file into a uint16_t, so 65536 wraps back to "never".
	maxSleepSeconds = 65535
)

// clampSleepSeconds keeps a requested delay inside the range the firmware can
// act on. Out-of-range values are not rejected outright because the firmware
// reads them as "never sleep", which is the opposite of what was asked for.
func clampSleepSeconds(seconds int) int {
	switch {
	case seconds <= 0:
		return 0 // the one deliberate way to keep the screen on
	case seconds < minSleepSeconds:
		return minSleepSeconds
	case seconds > maxSleepSeconds:
		return maxSleepSeconds
	default:
		return seconds
	}
}

func writeSleepSetting(path string, seconds int) error {
	return os.WriteFile(path, []byte(strconv.Itoa(clampSleepSeconds(seconds))), 0o644)
}

func (s *Service) SetOLED(c *gin.Context) {
	var req proto.SetOledReq
	var rsp proto.Response

	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}

	if err := writeSleepSetting(OLEDSleepFile, req.Sleep); err != nil {
		rsp.ErrRsp(c, -2, "failed to write data")
		return
	}

	rsp.OkRsp(c)
	log.Debugf("set OLED sleep: %d", clampSleepSeconds(req.Sleep))
}

func (s *Service) GetOLED(c *gin.Context) {
	var rsp proto.Response

	if _, err := os.Stat(OLEDExistFile); err != nil {
		rsp.OkRspWithData(c, &proto.GetOLEDRsp{
			Exist: false,
			Sleep: 0,
		})
		return
	}

	data, err := os.ReadFile(OLEDSleepFile)
	if err != nil {
		rsp.OkRspWithData(c, &proto.GetOLEDRsp{
			Exist: true,
			Sleep: 0,
		})
		return
	}

	content := strings.TrimSpace(string(data))
	sleep, err := strconv.Atoi(content)
	if err != nil {
		log.Errorf("failed to parse OLED: %s", err)
		rsp.ErrRsp(c, -1, "failed to parse OLED config")
		return
	}

	rsp.OkRspWithData(c, &proto.GetOLEDRsp{
		Exist: true,
		Sleep: sleep,
	})
	log.Debugf("get OLED config successful, sleep %d", sleep)
}
