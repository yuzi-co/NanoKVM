package vm

import (
	"strings"
	"testing"
)

func TestCommandsForAudioTouchesAndRebuilds(t *testing.T) {
	marker, mount, _, ok := commandsFor("audio")

	if !ok {
		t.Fatal("commandsFor rejected the audio device")
	}

	if marker != virtualAudio {
		t.Errorf("marker is %q, want %q", marker, virtualAudio)
	}

	if len(mount) == 0 || !strings.Contains(mount[0], "touch /boot/usb.uac") {
		t.Errorf("mount commands start with %v, want a touch of the marker", mount)
	}
}

// Removing a function directory blocks until every holder of its character
// device closes it. The teardown must only remove the symlink.
func TestCommandsForAudioRemovesOnlyTheSymlink(t *testing.T) {
	_, _, unmount, ok := commandsFor("audio")
	if !ok {
		t.Fatal("commandsFor rejected the audio device")
	}

	for _, command := range unmount {
		if strings.Contains(command, "rmdir") {
			t.Errorf("unmount runs %q, which can block forever", command)
		}
	}

	var removesLink bool
	for _, command := range unmount {
		if strings.Contains(command, "configs/c.1/uac1.usb0") {
			removesLink = true
		}
	}

	if !removesLink {
		t.Error("unmount never removes the config symlink")
	}
}

func TestCommandsForRejectsUnknownDevices(t *testing.T) {
	if _, _, _, ok := commandsFor("speaker"); ok {
		t.Error("commandsFor accepted a device it does not know")
	}
}

// Audio became a third case in commandsFor, and the point of that refactor was
// that network and disk still behave exactly as they did. Checking only that
// the lookup succeeds does not show it: a swap of virtualNetwork and
// virtualDisk inside commandsFor would pass, and the settings switch would
// then rebuild the USB gadget for the wrong device.
func TestCommandsForStillHandlesNetworkAndDisk(t *testing.T) {
	for _, want := range []struct {
		device  string
		marker  string
		mount   string
		unmount string
	}{
		{"network", virtualNetwork, "touch /boot/usb.rndis0", "rndis.usb0"},
		{"disk", virtualDisk, "touch /boot/usb.disk0", "mass_storage.disk0"},
	} {
		marker, mount, unmount, ok := commandsFor(want.device)

		if !ok {
			t.Errorf("commandsFor rejected %q", want.device)
			continue
		}

		if marker != want.marker {
			t.Errorf("%s marker is %q, want %q", want.device, marker, want.marker)
		}

		if len(mount) == 0 || mount[0] != want.mount {
			t.Errorf("%s mount commands are %v, want the first to be %q",
				want.device, mount, want.mount)
		}

		var removesLink bool
		for _, command := range unmount {
			if strings.Contains(command, want.unmount) {
				removesLink = true
			}
		}

		if !removesLink {
			t.Errorf("%s unmount commands are %v, want one naming %q",
				want.device, unmount, want.unmount)
		}
	}
}
