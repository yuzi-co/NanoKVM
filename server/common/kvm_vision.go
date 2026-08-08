//go:build !novision

package common

/*
	#cgo CFLAGS: -I../include
	#cgo LDFLAGS: -L../dl_lib -lkvm
	#include "kvm_vision.h"
*/
import "C"
import (
	"sync"
	"unsafe"

	log "github.com/sirupsen/logrus"
)

var (
	kvmVision     *KvmVision
	kvmVisionOnce sync.Once

	// captureLifecycle keeps frame reads out of the way of teardown. See
	// capture_gate.go for why libkvm cannot be trusted to do this itself.
	captureLifecycle = newCaptureGate()
)

// imgNotExist mirrors IMG_NOT_EXIST from kvm_vision.h.
const imgNotExist = -1

// One read tracker per encoding, because the two are read independently and a
// shared tracker would report a change every time they disagreed. service/hid
// keeps one health record per endpoint for the same reason.
var (
	mjpegReads captureReadLog
	h264Reads  captureReadLog
)

// KvmVision carries no state. The lifecycle lives in captureLifecycle, which
// holds the read lock across each call rather than exposing a flag to test
// beforehand - see capture_gate.go for why a flag would not close the race.
type KvmVision struct{}

func GetKvmVision() *KvmVision {
	kvmVisionOnce.Do(func() {
		kvmVision = &KvmVision{}

		logLevel := C.uint8_t(0)
		C.kvmv_init(logLevel)
		log.Debugf("kvm vision initialized")
	})

	return kvmVision
}

func (k *KvmVision) ReadMjpeg(width uint16, height uint16, quality uint16) (data []byte, result int) {
	var (
		kvmData  *C.uint8_t
		dataSize C.uint32_t
	)

	// A read after the teardown answers IMG_NOT_EXIST rather than reaching
	// kvmv_read_img with the mutex already destroyed. The streamers treat that
	// as "no frame this time", which is what they do on a live board whenever
	// libkvm has nothing ready.
	if !captureLifecycle.withLive(func() {
		result = int(C.kvmv_read_img(
			C.uint16_t(width),
			C.uint16_t(height),
			C.uint8_t(0),
			C.uint16_t(quality),
			&kvmData,
			&dataSize,
		))

		reportCaptureRead(&mjpegReads, result)
		if result < 0 {
			return
		}
		defer C.free_kvmv_data(&kvmData)

		data = C.GoBytes(unsafe.Pointer(kvmData), C.int(dataSize))
	}) {
		return nil, imgNotExist
	}

	return
}

func (k *KvmVision) ReadH264(width uint16, height uint16, bitRate uint16) (data []byte, result int) {
	var (
		kvmData  *C.uint8_t
		dataSize C.uint32_t
	)

	// A read after the teardown answers IMG_NOT_EXIST rather than reaching
	// kvmv_read_img with the mutex already destroyed. The streamers treat that
	// as "no frame this time", which is what they do on a live board whenever
	// libkvm has nothing ready.
	if !captureLifecycle.withLive(func() {
		result = int(C.kvmv_read_img(
			C.uint16_t(width),
			C.uint16_t(height),
			C.uint8_t(1),
			C.uint16_t(bitRate),
			&kvmData,
			&dataSize,
		))

		reportCaptureRead(&h264Reads, result)
		if result < 0 {
			return
		}
		defer C.free_kvmv_data(&kvmData)

		data = C.GoBytes(unsafe.Pointer(kvmData), C.int(dataSize))
	}) {
		return nil, imgNotExist
	}

	return
}

func (k *KvmVision) SetHDMI(enable bool) int {
	hdmiEnable := C.uint8_t(0)
	if enable {
		hdmiEnable = C.uint8_t(1)
	}

	result := -1
	if !captureLifecycle.withLive(func() {
		result = hdmiControlResult(int(C.kvmv_hdmi_control(hdmiEnable)))
	}) {
		return -1
	}

	if result < 0 {
		log.Errorf("failed to set hdmi to %t: the library declined, which is what alpha and beta boards always do", enable)
	}

	return result
}

func (k *KvmVision) HasHDMISignal() bool {
	active := false
	captureLifecycle.withLive(func() {
		active = C.kvmv_hdmi_signal_active() != 0
	})

	return active
}

func (k *KvmVision) SetGop(gop uint8) {
	_gop := C.uint8_t(gop)
	captureLifecycle.withLive(func() {
		C.set_h264_gop(_gop)
	})
}

func (k *KvmVision) SetFrameDetect(frame uint8) {
	_frame := C.uint8_t(frame)
	captureLifecycle.withLive(func() {
		C.set_frame_detact(_frame)
	})
}

func (k *KvmVision) Close() {
	captureLifecycle.stop(func() {
		C.kvmv_deinit()
	})
	log.Debugf("stop kvm vision...")
}

// There used to be StopCapture, ResumeCapture and IsCapturing here. Their only
// caller was service/vm/hdmi_idle.go, which stopped capture after an idle
// timeout, and the 2026-08-01 rebase dropped that file - so the three methods
// survived their consumer by months with nothing calling them.
//
// The gate itself stays, and it is what keeps a read from reaching a destroyed
// vi_mutex while Close is running. What it no longer carries is a way back:
// see the note at the end of capture_gate.go for why nothing on a device can
// stop capture without also ending the process.
