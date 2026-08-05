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

// The network has two markers - usb.ncm and usb.rndis0 - because S03usbdev
// prefers NCM and falls back to RNDIS. Clearing only one would leave the
// other on disk, and the function would come straight back at the next boot.
func TestCommandsForNetworkUnmountClearsBothMarkers(t *testing.T) {
	_, _, unmount, ok := commandsFor("network")
	if !ok {
		t.Fatal("commandsFor rejected the network device")
	}

	for _, command := range unmount {
		if strings.Contains(command, "rmdir") {
			t.Errorf("unmount runs %q, which can block forever", command)
		}
	}

	for _, marker := range []string{"/boot/usb.ncm", "/boot/usb.rndis0"} {
		var removed bool
		for _, command := range unmount {
			if strings.Contains(command, marker) {
				removed = true
			}
		}
		if !removed {
			t.Errorf("unmount commands %v never remove %s", unmount, marker)
		}
	}

	for _, dir := range []string{"configs/c.1/ncm.usb0", "configs/c.1/rndis.usb0"} {
		var removed bool
		for _, command := range unmount {
			if strings.Contains(command, dir) {
				removed = true
			}
		}
		if !removed {
			t.Errorf("unmount commands %v never remove the %s symlink", unmount, dir)
		}
	}
}

// enabledForToggle is what UpdateVirtualDevice asks before picking mount or
// unmount. It has to recognise a board enabled through either of the
// network's two markers, not just the one its own mount command creates.
func TestEnabledForToggleRecognisesEitherNetworkMarker(t *testing.T) {
	if !enabledForToggle("network", presence(virtualNetworkNCM)) {
		t.Error("an NCM-only board was not recognised as having the network on")
	}

	if !enabledForToggle("network", presence(virtualNetwork)) {
		t.Error("an RNDIS-only board was not recognised as having the network on")
	}
}

func TestEnabledForToggleIsFalseWhenNeitherNetworkMarkerIsSet(t *testing.T) {
	if enabledForToggle("network", presence()) {
		t.Error("the network was reported on with neither marker present")
	}
}

// This is the bug finding 3 in fix round 1 describes end to end: on an
// NCM-only board, deciding mount-or-unmount from the single marker
// commandsFor returns (virtualNetwork, the RNDIS flavour) reads false and
// takes the mount branch - it touches /boot/usb.rndis0, restarts the gadget,
// and the network stays on with a stray second marker left behind.
// enabledForToggle has to send this case down the unmount path instead.
func TestNetworkToggleOnAnNCMOnlyBoardChoosesUnmount(t *testing.T) {
	_, mount, unmount, ok := commandsFor("network")
	if !ok {
		t.Fatal("commandsFor rejected the network device")
	}

	present := presence(virtualNetworkNCM)

	commands := mount
	if enabledForToggle("network", present) {
		commands = unmount
	}

	var choseMount, choseUnmount bool
	for _, command := range commands {
		if strings.Contains(command, "touch /boot/usb.rndis0") {
			choseMount = true
		}
		if strings.Contains(command, "rm -f /boot/usb.ncm") {
			choseUnmount = true
		}
	}

	if choseMount {
		t.Error("an NCM-only board chose the mount commands - it would stay on and gain a second marker")
	}

	if !choseUnmount {
		t.Error("an NCM-only board did not choose the unmount commands")
	}
}
