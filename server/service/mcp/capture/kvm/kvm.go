package kvmcapture

import (
	"NanoKVM-Server/common"
	mcpservice "NanoKVM-Server/service/mcp"
	"NanoKVM-Server/service/mcp/capture"
)

// screenDimensions reports the capture size the snapshotter should ask for.
// 0x0 is a legitimate answer: it is the "follow the source" resolution, which
// CheckScreen leaves alone. Snapshot is taken after CheckScreen the way the
// streamers do it, so that a repaired value is the one that gets used.
func screenDimensions() (uint16, uint16) {
	common.CheckScreen()
	values := common.GetScreen().Snapshot()
	return values.Width, values.Height
}

func New() mcpservice.Snapshotter {
	return mcpcapture.New(common.GetKvmVision(), screenDimensions)
}
