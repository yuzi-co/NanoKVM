package vm

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The USB controller has a fixed number of endpoints and every gadget function
// takes a share of them. Overrunning it does not disable the last function
// added: the whole gadget refuses to bind, so the keyboard and both mice
// disappear as well, and the error names whichever function happens to be
// linked last rather than the one that overran.
//
// The number is measured, not read. dwc2 announces
//
//	dwc2 4340000.usb: EPs: 8, dedicated fifos, 3072 entries in SPRAM
//
// and that is not the budget. Three configurations totalling nine endpoints
// bind and work on the device: acm+network, acm+disk+audio, and
// disk+network+audio, each with all three HID functions. Ten fails. Deriving
// the budget from the kernel's line looks responsible and would refuse two
// configurations that are known good.
const DefaultEndpointBudget = 9

// hidCost is what the keyboard, the relative mouse and the absolute pointer
// cost together: one interrupt endpoint each.
const hidCost = 3

// The markers that decide which functions the gadget carries. usb.ncm and
// usb.rndis0 are alternatives for one function - S03usbdev prefers NCM - so
// they belong to a single entry and are counted once.
const (
	virtualConsole    = "/boot/usb.acm"
	virtualNetworkNCM = "/boot/usb.ncm"
	disableHid        = "/boot/disable_hid"
)

// usbFunction is one optional gadget function.
//
// device is the name the API accepts to switch it. gadget is the configfs
// directory that proves the function actually linked; gadgetAlt is a second
// accepted directory for a function with two forms (the network's NCM and
// RNDIS). priority decides what survives when more is enabled than fits:
// higher survives longer.
type usbFunction struct {
	name      string
	device    string
	markers   []string
	gadget    string
	gadgetAlt string
	cost      int
	priority  int
}

// The console outranks everything except HID because it is the only way into a
// board whose network is gone. Audio is last because it is the only entry that
// costs nothing to lose.
var usbFunctions = []usbFunction{
	{name: "console", device: "console", markers: []string{virtualConsole}, gadget: "acm.GS0", cost: 3, priority: 40},
	{name: "disk", device: "disk", markers: []string{virtualDisk}, gadget: "mass_storage.disk0", cost: 2, priority: 30},
	{name: "network", device: "network", markers: []string{virtualNetworkNCM, virtualNetwork}, gadget: "ncm.usb0", gadgetAlt: "rndis.usb0", cost: 3, priority: 20},
	{name: "audio", device: "audio", markers: []string{virtualAudio}, gadget: "uac1.usb0", cost: 1, priority: 10},
}

// endpointBudget is what the controller fits.
func endpointBudget() int {
	return DefaultEndpointBudget
}

// enabled reports whether any of the function's markers is present.
func (f usbFunction) enabled(present func(string) bool) bool {
	for _, marker := range f.markers {
		if present(marker) {
			return true
		}
	}

	return false
}

// hidEndpointCost charges nothing when HID is switched off, because those
// endpoints are then genuinely free. Charging for them would refuse a function
// that fits.
func hidEndpointCost(present func(string) bool) int {
	if present(disableHid) {
		return 0
	}

	return hidCost
}

// usedEndpoints totals HID and every enabled function.
func usedEndpoints(present func(string) bool) int {
	used := hidEndpointCost(present)

	for _, function := range usbFunctions {
		if function.enabled(present) {
			used += function.cost
		}
	}

	return used
}

// endpointCost reports what one function costs, by its table name.
func endpointCost(name string) (int, bool) {
	for _, function := range usbFunctions {
		if function.name == name {
			return function.cost, true
		}
	}

	return 0, false
}

// functionForDevice finds the table entry an API device name refers to.
func functionForDevice(device string) (usbFunction, bool) {
	if device == "" {
		return usbFunction{}, false
	}

	for _, function := range usbFunctions {
		if function.device == device {
			return function, true
		}
	}

	return usbFunction{}, false
}

// dropOrder lists the optional functions lowest priority first, which is the
// order they are given up in.
func dropOrder() []usbFunction {
	order := make([]usbFunction, len(usbFunctions))
	copy(order, usbFunctions)

	sort.SliceStable(order, func(i, j int) bool {
		return order[i].priority < order[j].priority
	})

	return order
}

// canEnable reports whether one more function fits, how many endpoints are
// free, and which enabled functions would free enough on their own.
//
// The suggestions are the point: an operator told only "no room" turns
// something off, is refused again, and learns the rule by exhaustion. Only
// functions that would actually make room are named, cheapest first, so the
// operator gives up as little as possible.
func canEnable(device string, present func(string) bool) (bool, int, []string) {
	wanted, ok := functionForDevice(device)
	if !ok {
		return false, 0, nil
	}

	free := endpointBudget() - usedEndpoints(present)

	if wanted.enabled(present) || wanted.cost <= free {
		return true, free, nil
	}

	needed := wanted.cost - free

	candidates := make([]usbFunction, 0, len(usbFunctions))
	for _, function := range usbFunctions {
		if function.name == wanted.name || !function.enabled(present) {
			continue
		}

		if function.cost >= needed {
			candidates = append(candidates, function)
		}
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].cost < candidates[j].cost
	})

	relief := make([]string, 0, len(candidates))
	for _, function := range candidates {
		relief = append(relief, function.name)
	}

	return false, free, relief
}

// gadgetConfigPath is where configfs records the functions this gadget carries.
// A symlink here means the function was built; a marker only means it was
// asked for, and the two differ whenever the budget dropped something.
const gadgetConfigPath = "/sys/kernel/config/usb_gadget/g0/configs/c.1"

// active reports whether the function is linked into the running gadget.
func (f usbFunction) active(linked func(string) bool) bool {
	if linked(f.gadget) {
		return true
	}

	return f.gadgetAlt != "" && linked(f.gadgetAlt)
}

// isFunctionActive answers the same question against the real configfs.
func isFunctionActive(name string) bool {
	for _, function := range usbFunctions {
		if function.name != name {
			continue
		}

		return function.active(func(dir string) bool {
			_, err := os.Lstat(filepath.Join(gadgetConfigPath, dir))
			return err == nil
		})
	}

	return false
}

// refusalMessage tells the operator what was refused, how short the budget is,
// and what would make room. Naming a function that would not free enough is
// worse than naming none: they turn it off and are refused again.
func refusalMessage(device string, free int, relief []string) string {
	wanted, ok := functionForDevice(device)
	if !ok {
		return "unknown device"
	}

	message := fmt.Sprintf("%s needs %d USB endpoints, %d free", device, wanted.cost, free)

	if len(relief) == 0 {
		return message
	}

	options := make([]string, 0, len(relief))
	for _, name := range relief {
		cost, _ := endpointCost(name)
		options = append(options, fmt.Sprintf("%s (%d)", name, cost))
	}

	return message + " — turn off " + strings.Join(options, " or ") + " first"
}
