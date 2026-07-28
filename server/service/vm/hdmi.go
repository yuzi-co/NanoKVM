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

	utils.PersistHDMIEnabled()
	EnableHdmiCapture()

	rsp.OkRsp(c)
	log.Debug("enable hdmi")
}

func (s *Service) DisableHdmi(c *gin.Context) {
	var rsp proto.Response

	utils.PersistHDMIDisabled()
	DisableHdmiCapture()

	rsp.OkRsp(c)
	log.Debug("disable hdmi")
}

func (s *Service) SetHdmiIdleTimeout(c *gin.Context) {
	var req proto.SetHdmiIdleTimeoutReq
	var rsp proto.Response

	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}

	utils.PersistHDMIIdleTimeout(req.Minutes)
	ApplyHdmiIdleTimeout()

	rsp.OkRsp(c)
	log.Debugf("set hdmi idle timeout to %d minutes", req.Minutes)
}

func (s *Service) GetHdmiState(c *gin.Context) {
	var rsp proto.Response

	// With capture switched off nothing refreshes the signal state, so the
	// file holds whatever was true when it was last running. Report no signal
	// rather than that stale value.
	enabled := !utils.IsHdmiDisabled()

	rsp.OkRspWithData(c, &proto.GetGetHdmiStateRsp{
		Enabled:     enabled,
		Signal:      enabled && utils.HasHDMISignal(),
		IdleTimeout: utils.GetHDMIIdleTimeout(),
	})

	log.Debug("get hdmi state")
}
