package vm

import (
	"errors"
	"os"
	"os/exec"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"NanoKVM-Server/proto"
	"NanoKVM-Server/service/hid"
)

const (
	virtualNetwork = "/boot/usb.rndis0"
	virtualDisk    = "/boot/usb.disk0"
	virtualAudio   = "/boot/usb.uac"
)

var (
	mountConsoleCommands = []string{
		"touch /boot/usb.acm",
		"/etc/init.d/S03usbdev stop",
		"/etc/init.d/S03usbdev start",
	}

	// The function directory stays. `rmdir functions/acm.GS0` blocks forever:
	// /etc/inittab respawns a getty on /dev/ttyGS0, so the character device
	// always has a holder, and recovering from that wedge needs a full teardown
	// of the gadget. Remove the config symlink and nothing else - the same rule
	// the audio function follows.
	unmountConsoleCommands = []string{
		"/etc/init.d/S03usbdev stop",
		"rm -rf /sys/kernel/config/usb_gadget/g0/configs/c.1/acm.GS0",
		"rm /boot/usb.acm",
		"/etc/init.d/S03usbdev start",
	}

	mountNetworkCommands = []string{
		"touch /boot/usb.rndis0",
		"/etc/init.d/S03usbdev stop",
		"/etc/init.d/S03usbdev start",
	}

	// The network has two markers - usb.ncm and usb.rndis0 - because S03usbdev
	// prefers NCM and falls back to RNDIS. Clearing only one leaves the other
	// on disk, and the function comes straight back at the next boot. Both
	// config symlinks are removed the same way: whichever one did not bind is
	// simply absent, and `rm -rf` does not error on a path that is not there.
	unmountNetworkCommands = []string{
		"/etc/init.d/S03usbdev stop",
		"rm -rf /sys/kernel/config/usb_gadget/g0/configs/c.1/ncm.usb0",
		"rm -rf /sys/kernel/config/usb_gadget/g0/configs/c.1/rndis.usb0",
		"rm -f /boot/usb.ncm",
		"rm -f /boot/usb.rndis0",
		"/etc/init.d/S03usbdev start",
	}

	mountDiskCommands = []string{
		"touch /boot/usb.disk0",
		"/etc/init.d/S03usbdev stop",
		"/etc/init.d/S03usbdev start",
	}

	unmountDiskCommands = []string{
		"/etc/init.d/S03usbdev stop",
		"rm -rf /sys/kernel/config/usb_gadget/g0/configs/c.1/mass_storage.disk0",
		"rm /boot/usb.disk0",
		"/etc/init.d/S03usbdev start",
	}

	mountAudioCommands = []string{
		"touch /boot/usb.uac",
		"/etc/init.d/S03usbdev stop",
		"/etc/init.d/S03usbdev start",
	}

	// The function directory stays. Removing it blocks until every holder of
	// its character device closes it, and recovering from that needs a full
	// teardown of the gadget.
	unmountAudioCommands = []string{
		"/etc/init.d/S03usbdev stop",
		"rm -rf /sys/kernel/config/usb_gadget/g0/configs/c.1/uac1.usb0",
		"rm /boot/usb.uac",
		"/etc/init.d/S03usbdev start",
	}
)

// enabledForToggle decides whether a device is already on, for the purpose of
// picking mount or unmount. It goes through functionForDevice and checks every
// marker the function declares, not just the single marker its own mount
// command creates.
//
// The network has two markers - usb.ncm and usb.rndis0 - and commandsFor's
// device name still resolves to whichever one the API device name's own mount
// command touches (usb.rndis0). A board enabled through the other marker alone
// would check that single marker as false, take the mount branch, touch
// usb.rndis0, and restart the gadget with the network already on: it stays on,
// and a stray second marker is left behind. Checking every marker through the
// function's enabled method is what keeps that from happening.
func enabledForToggle(device string, present func(string) bool) bool {
	function, ok := functionForDevice(device)
	return ok && function.enabled(present)
}

// commandsFor maps a device name onto its marker and the two command lists.
// It exists so that the mapping can be tested without running anything.
func commandsFor(device string) (marker string, mount []string, unmount []string, ok bool) {
	switch device {
	case "console":
		return virtualConsole, mountConsoleCommands, unmountConsoleCommands, true
	case "network":
		return virtualNetwork, mountNetworkCommands, unmountNetworkCommands, true
	case "disk":
		return virtualDisk, mountDiskCommands, unmountDiskCommands, true
	case "audio":
		return virtualAudio, mountAudioCommands, unmountAudioCommands, true
	default:
		return "", nil, nil, false
	}
}

func (s *Service) GetVirtualDevice(c *gin.Context) {
	var rsp proto.Response

	present := func(marker string) bool {
		exist, _ := isDeviceExist(marker)
		return exist
	}

	state := func(device string) proto.VirtualDeviceState {
		function, ok := functionForDevice(device)
		if !ok {
			return proto.VirtualDeviceState{}
		}

		return proto.VirtualDeviceState{
			Enabled: function.enabled(present),
			Active:  isFunctionActive(function.name),
			Cost:    function.cost,
		}
	}

	rsp.OkRspWithData(c, &proto.GetVirtualDeviceRsp{
		Console: state("console"),
		Network: state("network"),
		Disk:    state("disk"),
		Audio:   state("audio"),
		Used:    usedEndpoints(present),
		Total:   endpointBudget(),
	})

	log.Debugf("get virtual device success")
}

func (s *Service) UpdateVirtualDevice(c *gin.Context) {
	var req proto.UpdateVirtualDeviceReq
	var rsp proto.Response

	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "invalid argument")
		return
	}

	device, mount, unmount, ok := commandsFor(req.Device)
	if !ok {
		rsp.ErrRsp(c, -2, "invalid arguments")
		return
	}

	present := func(marker string) bool {
		exist, _ := isDeviceExist(marker)
		return exist
	}

	commands := mount
	if enabledForToggle(req.Device, present) {
		// Turning a function off always fits, so it is never checked.
		commands = unmount
	} else if ok, free, relief := canEnable(req.Device, present); !ok {
		// Refuse rather than drop something. A person is here to be told, and
		// silently switching off what they configured earlier is worse than
		// declining what they asked for now.
		log.Infof("refused %s: %d endpoints free", req.Device, free)
		rsp.ErrRsp(c, -4, refusalMessage(req.Device, free, relief))
		return
	}

	h := hid.GetHid()
	h.Lock()
	h.CloseNoLock()
	defer func() {
		h.OpenNoLock()
		h.Unlock()
	}()

	for _, command := range commands {
		err := exec.Command("sh", "-c", command).Run()
		if err != nil {
			rsp.ErrRsp(c, -3, "operation failed")
			return
		}
	}

	on, _ := isDeviceExist(device)
	rsp.OkRspWithData(c, &proto.UpdateVirtualDeviceRsp{
		On: on,
	})

	log.Debugf("update virtual device %s success", req.Device)
}

func isDeviceExist(device string) (bool, error) {
	_, err := os.Stat(device)

	if err == nil {
		return true, nil
	}

	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}

	log.Errorf("check file %s err: %s", device, err)
	return false, err
}
