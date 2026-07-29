package kvmcapture

import (
	"testing"

	"NanoKVM-Server/common"
)

// The snapshotter is handed width and height in that order. Getting them the
// wrong way round produces a capture request that is plausible and wrong.
func TestScreenDimensionsReportTheConfiguredResolution(t *testing.T) {
	common.SetScreen("resolution", 720)

	width, height := screenDimensions()

	if width != 1280 || height != 720 {
		t.Fatalf("got %dx%d, want 1280x720", width, height)
	}
}
