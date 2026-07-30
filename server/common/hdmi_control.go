package common

// hdmiControlResult maps what kvmv_hdmi_control returns onto the Go convention
// that a negative value means failure.
//
// The C function is declared `uint8_t kvmv_hdmi_control(uint8_t)` and returns
// -1 when it cannot act, so the value that reaches Go is 255. `result < 0` can
// therefore never be true, and the error branch guarding it was dead code.
//
// That mattered on real hardware. kvmv_hdmi_control drives the HDMI receiver
// power on the PCIe board only; on alpha and beta it returns immediately with
// -1. Every caller on those boards was told the HDMI state had changed while
// nothing had happened, and nothing was logged.
//
// Only 0 means success. Anything else is a failure, including values the
// library does not document today: reading an unknown return as success is how
// this went unnoticed in the first place.
func hdmiControlResult(raw int) int {
	if raw != 0 {
		return -1
	}

	return 0
}
