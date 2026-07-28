package vm

import (
	"time"

	"NanoKVM-Server/common"
	"NanoKVM-Server/proto"
	"NanoKVM-Server/utils"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

func (s *Service) ResetHdmi(c *gin.Context) {
	var rsp proto.Response

	vision := common.GetKvmVision()

	vision.SetHDMI(false)
	time.Sleep(1 * time.Second)
	vision.SetHDMI(true)
	utils.PersistHDMIEnabled()

	rsp.OkRsp(c)
	log.Debug("reset hdmi")
}

func (s *Service) EnableHdmi(c *gin.Context) {
	var rsp proto.Response

	vision := common.GetKvmVision()

	vision.SetHDMI(true)
	utils.PersistHDMIEnabled()

	rsp.OkRsp(c)
	log.Debug("enable hdmi")
}

func (s *Service) DisableHdmi(c *gin.Context) {
	var rsp proto.Response

	vision := common.GetKvmVision()

	vision.SetHDMI(false)
	utils.PersistHDMIDisabled()

	rsp.OkRsp(c)
	log.Debug("disable hdmi")
}

func (s *Service) GetHdmiState(c *gin.Context) {
	var rsp proto.Response

	// With capture switched off nothing refreshes the signal state, so the
	// file holds whatever was true when it was last running. Report no signal
	// rather than that stale value.
	enabled := !utils.IsHdmiDisabled()

	rsp.OkRspWithData(c, &proto.GetGetHdmiStateRsp{
		Enabled: enabled,
		Signal:  enabled && utils.HasHDMISignal(),
	})

	log.Debug("get hdmi state")
}
