//go:build novision

package common

import (
	"sync"

	log "github.com/sirupsen/logrus"
)

// captureLifecycle mirrors the real implementation so the state machine behaves
// the same way in a build with no hardware behind it.
var captureLifecycle = newCaptureGate()

// This stub replaces the cgo capture bindings when the "novision" build tag is
// set. The real implementation links against libkvm, which is not part of this
// repository, so without it the rest of the server cannot be type-checked or
// unit-tested off-device. It is never used in a device build.

var (
	kvmVision     *KvmVision
	kvmVisionOnce sync.Once
)

type KvmVision struct{}

func GetKvmVision() *KvmVision {
	kvmVisionOnce.Do(func() {
		kvmVision = &KvmVision{}
		log.Debugf("kvm vision stub initialized")
	})

	return kvmVision
}

// One read tracker per encoding, mirroring the real implementation so that the
// call shape it uses is type-checked by a build that has no libkvm behind it.
var (
	mjpegReads captureReadLog
	h264Reads  captureReadLog
)

func (k *KvmVision) ReadMjpeg(width uint16, height uint16, quality uint16) (data []byte, result int) {
	result = -1
	reportCaptureRead(&mjpegReads, result)

	return nil, result
}

func (k *KvmVision) ReadH264(width uint16, height uint16, bitRate uint16) (data []byte, result int) {
	result = -1
	reportCaptureRead(&h264Reads, result)

	return nil, result
}

func (k *KvmVision) SetHDMI(enable bool) int {
	return 0
}

// HasHDMISignal answers false off-device. There is no capture hardware to ask,
// and a stub that claimed a signal would make every test that reads the HDMI
// state assert against a picture that is not there.
func (k *KvmVision) HasHDMISignal() bool {
	return false
}

func (k *KvmVision) SetGop(gop uint8) {}

func (k *KvmVision) SetFrameDetect(frame uint8) {}

func (k *KvmVision) Close() {
	captureLifecycle.stop(func() {})
}
