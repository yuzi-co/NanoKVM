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

func TestCommandsForStillHandlesNetworkAndDisk(t *testing.T) {
	for _, device := range []string{"network", "disk"} {
		if _, _, _, ok := commandsFor(device); !ok {
			t.Errorf("commandsFor rejected %q", device)
		}
	}
}
