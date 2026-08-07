package vm

import (
	"NanoKVM-Server/proto"
	"NanoKVM-Server/service/ion"

	"github.com/gin-gonic/gin"
)

// GetIon reports the ION carveout state. It always succeeds: a carveout it
// cannot read comes back with the verdict "unavailable", because a diagnostic
// that fails the request would be worse than no diagnostic.
func (s *Service) GetIon(c *gin.Context) {
	var rsp proto.Response

	status := ion.Read()

	rsp.OkRspWithData(c, &proto.GetIonRsp{
		Total:       status.Total,
		Used:        status.Used,
		Free:        status.Free,
		UsageRate:   status.UsageRate,
		Generations: status.Generations,
		Reserve:     status.Reserve,
		Measured:    status.Measured,
		Verdict:     status.Verdict,
	})
}
