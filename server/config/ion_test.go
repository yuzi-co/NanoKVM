package config

import "testing"

// A server.yaml written before this feature existed has no ion block, so viper
// leaves the floor at zero. Zero would make every verdict unavailable, which is
// a silent loss of the whole feature on every existing device.
func TestAMissingIonBlockGetsTheDefaultFloor(t *testing.T) {
	original := instance
	t.Cleanup(func() { instance = original })

	instance = Config{}
	checkDefaultValue()

	if instance.Ion.ReserveFloor != 12582912 {
		t.Fatalf("ReserveFloor = %d, want 12582912", instance.Ion.ReserveFloor)
	}
}

func TestAConfiguredFloorIsKept(t *testing.T) {
	original := instance
	t.Cleanup(func() { instance = original })

	instance = Config{Ion: Ion{ReserveFloor: 12345678}}
	checkDefaultValue()

	if instance.Ion.ReserveFloor != 12345678 {
		t.Fatalf("ReserveFloor = %d, want the configured 12345678", instance.Ion.ReserveFloor)
	}
}
