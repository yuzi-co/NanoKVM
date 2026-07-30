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

// resumeCapture rebuilds the pipeline. kvmv_init is the counterpart of
// kvmv_deinit: it recreates vi_mutex, reopens the camera and restarts libkvm's
// two threads. The "auto init" the header claims for kvmv_read_img is only the
// first-time path and does not undo a deinit.
func resumeCapture() {
	C.kvmv_init(C.uint8_t(0))
	log.Debugf("capture pipeline rebuilt")
}

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

	// If an idle stop released the pipeline, rebuild it before reading rather
	// than refusing: whoever is reading is a viewer. IMG_NOT_EXIST stays as the
	// answer if the rebuild itself did not take, which is what the streamers
	// already treat as "no frame this time".
	if !captureLifecycle.withRead(resumeCapture, func() {
		result = int(C.kvmv_read_img(
			C.uint16_t(width),
			C.uint16_t(height),
			C.uint8_t(0),
			C.uint16_t(quality),
			&kvmData,
			&dataSize,
		))
		if result < 0 {
			log.Errorf("failed to read kvm image: %v", result)
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

	// If an idle stop released the pipeline, rebuild it before reading rather
	// than refusing: whoever is reading is a viewer. IMG_NOT_EXIST stays as the
	// answer if the rebuild itself did not take, which is what the streamers
	// already treat as "no frame this time".
	if !captureLifecycle.withRead(resumeCapture, func() {
		result = int(C.kvmv_read_img(
			C.uint16_t(width),
			C.uint16_t(height),
			C.uint8_t(1),
			C.uint16_t(bitRate),
			&kvmData,
			&dataSize,
		))
		if result < 0 {
			log.Errorf("failed to read kvm image: %v", result)
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

	result := hdmiControlResult(int(C.kvmv_hdmi_control(hdmiEnable)))
	if result < 0 {
		log.Errorf("failed to set hdmi to %t: the library declined, which is what alpha and beta boards always do", enable)
	}

	return result
}

func (k *KvmVision) SetGop(gop uint8) {
	_gop := C.uint8_t(gop)
	C.set_h264_gop(_gop)
}

func (k *KvmVision) SetFrameDetect(frame uint8) {
	_frame := C.uint8_t(frame)
	C.set_frame_detact(_frame)
}

func (k *KvmVision) Close() {
	captureLifecycle.stop(func() {
		C.kvmv_deinit()
	})
	log.Debugf("stop kvm vision...")
}

// StopCapture releases the capture pipeline. kvmv_deinit joins libkvm's two
// threads, destroys vi_mutex, closes the camera and frees every buffer, so no
// read may be in flight - the gate guarantees that.
func (k *KvmVision) StopCapture() {
	captureLifecycle.stop(func() {
		C.kvmv_deinit()
		log.Debugf("capture pipeline released")
	})
}

// ResumeCapture builds the pipeline again. kvmv_init is the counterpart of
// kvmv_deinit and recreates the mutex, reopens the camera and restarts both
// threads; the "auto init" the header describes for kvmv_read_img is only the
// first-time path and does not undo a deinit.
func (k *KvmVision) ResumeCapture() {
	captureLifecycle.resume(resumeCapture)
}

// IsCapturing reports whether the pipeline is currently built.
func (k *KvmVision) IsCapturing() bool {
	return captureLifecycle.isLive()
}
