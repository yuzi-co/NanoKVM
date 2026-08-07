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

// The floor is written twice: once in defaultConfig, which a device with no
// server.yaml at all gets through readByDefault, and once in the coercion
// above, which a device with an older server.yaml gets. A half-applied edit
// moves one and leaves the other, and then two devices grade the same carveout
// differently. Neither test above reads defaultConfig, so neither would notice.
//
// This compares the two rather than asserting a third copy of the number, so
// changing the floor on purpose needs no change here.
func TestBothCopiesOfTheDefaultFloorAgree(t *testing.T) {
	original := instance
	t.Cleanup(func() { instance = original })

	instance = Config{}
	checkDefaultValue()

	if defaultConfig.Ion.ReserveFloor != instance.Ion.ReserveFloor {
		t.Fatalf("defaultConfig floor is %d but the coercion gives %d - a device with no "+
			"server.yaml would grade its carveout differently from one with an old server.yaml",
			defaultConfig.Ion.ReserveFloor, instance.Ion.ReserveFloor)
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
