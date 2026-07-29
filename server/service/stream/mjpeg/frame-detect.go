package mjpeg

import (
	"sync"
	"time"

	"NanoKVM-Server/common"
	"NanoKVM-Server/proto"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

const FrameDetectInterval uint8 = 60

const (
	defaultPauseDuration = 10 * time.Second
	maxPauseDuration     = 5 * time.Minute
)

// setFrameDetect is a variable so the pause logic can be tested without the
// capture hardware.
var setFrameDetect = func(frames uint8) {
	common.GetKvmVision().SetFrameDetect(frames)
}

// pauseDuration is how long detection stays off for a request. The value comes
// from the client, and detection is what notices the screen has changed, so an
// unbounded one switches the feature off for as long as the caller likes.
func pauseDuration(seconds int) time.Duration {
	if seconds <= 0 {
		return defaultPauseDuration
	}

	duration := time.Duration(seconds) * time.Second
	if duration > maxPauseDuration || duration < 0 {
		return maxPauseDuration
	}

	return duration
}

var (
	pauseMutex sync.Mutex
	pauseTimer *time.Timer
	pauseUntil time.Time
)

// pauseFrameDetect switches detection off and schedules it back on, without
// holding the request open for the duration.
//
// Overlapping requests extend the pause but never shorten it: a caller that
// asked for a minute must not have it cut to a second by whoever asks next.
func pauseFrameDetect(duration time.Duration) {
	pauseMutex.Lock()
	defer pauseMutex.Unlock()

	deadline := time.Now().Add(duration)
	if !deadline.After(pauseUntil) {
		// Already paused at least this long, and already switched off.
		return
	}

	pauseUntil = deadline
	setFrameDetect(0)

	if pauseTimer != nil {
		pauseTimer.Stop()
	}
	pauseTimer = time.AfterFunc(duration, resumeFrameDetect)
}

func resumeFrameDetect() {
	pauseMutex.Lock()
	defer pauseMutex.Unlock()

	// A later request extended the pause after this timer was armed; the timer
	// it armed is the one that gets to resume.
	if time.Now().Before(pauseUntil) {
		return
	}

	pauseTimer = nil
	pauseUntil = time.Time{}
	setFrameDetect(FrameDetectInterval)
}

// resetFrameDetectPause drops any pause in flight, for tests.
func resetFrameDetectPause() {
	pauseMutex.Lock()
	defer pauseMutex.Unlock()

	if pauseTimer != nil {
		pauseTimer.Stop()
		pauseTimer = nil
	}
	pauseUntil = time.Time{}
}

func UpdateFrameDetect(c *gin.Context) {
	var req proto.UpdateFrameDetectReq
	var rsp proto.Response

	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "invalid parameters")
		return
	}

	var frame uint8 = 0
	if req.Enabled {
		frame = FrameDetectInterval
	}

	// An explicit setting overrides a pause still in flight, so its timer must
	// not come along later and undo it.
	resetFrameDetectPause()
	setFrameDetect(frame)

	rsp.OkRsp(c)
	log.Debugf("update frame detect: %t", req.Enabled)
}

func StopFrameDetect(c *gin.Context) {
	var req proto.StopFrameDetectReq
	var rsp proto.Response

	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "invalid parameters")
		return
	}

	pauseFrameDetect(pauseDuration(req.Duration))

	rsp.OkRsp(c)
}
