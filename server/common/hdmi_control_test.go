package common

import "testing"

// kvmv_hdmi_control is declared to return uint8_t, so its -1 reaches Go as 255.
// The check it was written with, `result < 0`, therefore could never be true,
// and on hardware where the call is unsupported every caller was told the HDMI
// state had changed when nothing had happened.
func TestHDMIControlResultReportsTheUnsignedFailureValue(t *testing.T) {
	if got := hdmiControlResult(255); got >= 0 {
		t.Fatalf("hdmiControlResult(255) = %d, want a negative value: 255 is (uint8_t)-1", got)
	}
}

func TestHDMIControlResultAcceptsSuccess(t *testing.T) {
	if got := hdmiControlResult(0); got != 0 {
		t.Fatalf("hdmiControlResult(0) = %d, want 0", got)
	}
}

// Anything the library never documents is still not success. Treating only 255
// as a failure would let a future return value through as if it had worked.
func TestHDMIControlResultTreatsEveryOtherValueAsFailure(t *testing.T) {
	for _, raw := range []int{1, 2, 127, 128, 254} {
		if got := hdmiControlResult(raw); got >= 0 {
			t.Errorf("hdmiControlResult(%d) = %d, want a negative value", raw, got)
		}
	}
}
