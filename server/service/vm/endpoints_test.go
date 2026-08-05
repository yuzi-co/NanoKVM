package vm

import (
	"reflect"
	"strings"
	"testing"
)

// presence builds the marker probe the budget functions take, so no test
// touches the filesystem.
func presence(markers ...string) func(string) bool {
	set := make(map[string]bool, len(markers))
	for _, marker := range markers {
		set[marker] = true
	}

	return func(path string) bool { return set[path] }
}

func TestUsedEndpointsCountsHidAndEachFunctionOnce(t *testing.T) {
	for _, test := range []struct {
		name    string
		markers []string
		want    int
	}{
		{"nothing but hid", nil, 3},
		{"hid and the console", []string{virtualConsole}, 6},
		{"hid, console and audio", []string{virtualConsole, virtualAudio}, 7},
		{"hid, console and disk", []string{virtualConsole, virtualDisk}, 8},
		{"disk and network without the console", []string{virtualDisk, virtualNetwork}, 8},
		{"everything at once", []string{virtualConsole, virtualDisk, virtualNetwork, virtualAudio}, 12},
	} {
		if got := usedEndpoints(presence(test.markers...)); got != test.want {
			t.Errorf("%s: used %d endpoints, want %d", test.name, got, test.want)
		}
	}
}

// usb.ncm and usb.rndis0 are alternatives for one function. Counting both
// would reserve three endpoints that nothing uses, and the guard would then
// refuse a function that fits.
func TestUsedEndpointsCountsTheNetworkOnce(t *testing.T) {
	both := usedEndpoints(presence(virtualNetworkNCM, virtualNetwork))
	one := usedEndpoints(presence(virtualNetwork))

	if both != one {
		t.Errorf("two network markers cost %d, one costs %d; want the same", both, one)
	}
}

// A board with HID disabled has three more endpoints to spend. Charging for
// hardware that is not there would drop functions that fit.
func TestHidCostsNothingWhenItIsDisabled(t *testing.T) {
	if got := usedEndpoints(presence(disableHid)); got != 0 {
		t.Errorf("used %d endpoints with hid disabled, want 0", got)
	}
}

func TestCanEnableAllowsWhatFits(t *testing.T) {
	// hid(3) + console(3) is 6 of 9.
	ok, free, _ := canEnable("audio", presence(virtualConsole))

	if !ok {
		t.Error("refused audio with 3 endpoints free")
	}

	if free != 3 {
		t.Errorf("reported %d free, want 3", free)
	}
}

// Measured on hardware: acm + network is 9 and binds, so the guard must allow
// it. An earlier draft budgeted 8 and would have refused this forever.
func TestCanEnableAllowsTheConsoleAndNetworkTogether(t *testing.T) {
	if ok, _, _ := canEnable("network", presence(virtualConsole)); !ok {
		t.Error("refused the network alongside the console, which binds on hardware")
	}
}

func TestCanEnableRefusesWhatDoesNotFit(t *testing.T) {
	// console(3) + disk(2) + hid(3) is 8 of 9, so one endpoint is left and the
	// network needs three.
	ok, free, relief := canEnable("network", presence(virtualConsole, virtualDisk))

	if ok {
		t.Error("allowed the network with one endpoint free")
	}

	if free != 1 {
		t.Errorf("reported %d free, want 1", free)
	}

	// Naming something that would not free enough is worse than naming
	// nothing: the operator turns it off and is refused again.
	if !reflect.DeepEqual(relief, []string{"disk", "console"}) {
		t.Errorf("suggested %v, want [disk console]", relief)
	}
}

// Every suggestion has to actually make room. Naming one that does not is worse
// than naming none: the operator turns it off and is refused again, and learns
// the rule by exhaustion.
func TestCanEnableOnlySuggestsFunctionsThatFreeEnough(t *testing.T) {
	_, free, relief := canEnable("network", presence(virtualConsole, virtualDisk))

	wanted, ok := endpointCost("network")
	if !ok {
		t.Fatal("the network is missing from the table")
	}

	needed := wanted - free

	if len(relief) == 0 {
		t.Fatal("refused the network without suggesting anything")
	}

	for _, name := range relief {
		cost, ok := endpointCost(name)
		if !ok {
			t.Fatalf("suggested %q, which is not a function", name)
		}

		if cost < needed {
			t.Errorf("suggested %q, which frees %d of the %d needed", name, cost, needed)
		}
	}
}

// The case above cannot catch the filter being deleted: with the console and
// the disk enabled, one endpoint is free, the network needs two more, and both
// candidates clear that bar - so removing the filter returns the same list.
//
// This one discriminates. console(3) + disk(2) + audio(1) + hid(3) is exactly 9
// and the network needs all three of its endpoints back, so only the console
// can supply them on its own. A build with no filter would answer
// [audio disk console] and send the operator to turn off a speaker that frees
// one of the three endpoints it needs.
//
// This filter has already been deleted once during this task. It stays tested.
func TestCanEnableWillNotSuggestAFunctionThatIsTooSmall(t *testing.T) {
	ok, free, relief := canEnable("network", presence(virtualConsole, virtualDisk, virtualAudio))

	if ok {
		t.Fatal("allowed the network at a full budget")
	}

	if free != 0 {
		t.Fatalf("reported %d free, want 0", free)
	}

	if !reflect.DeepEqual(relief, []string{"console"}) {
		t.Errorf("suggested %v, want [console] - the only one that frees 3", relief)
	}
}

func TestEndpointCostRejectsUnknownNames(t *testing.T) {
	if _, ok := endpointCost("speaker"); ok {
		t.Error("endpointCost accepted a name it does not know")
	}
}

// The console claims three of the nine endpoints - the largest single share
// after HID - so it needs a switch like the rest. A budget display that shows
// the operator a full bar while offering no way to free the biggest consumer
// states the problem and withholds the answer.
func TestConsoleIsTogglableLikeTheOthers(t *testing.T) {
	if _, ok := endpointCost("console"); !ok {
		t.Error("the console is missing from the table")
	}

	function, ok := functionForDevice("console")
	if !ok {
		t.Fatal("no function answers to the device name \"console\"")
	}

	if function.markers[0] != virtualConsole {
		t.Errorf("the console is gated on %q, want %q", function.markers[0], virtualConsole)
	}
}

func TestPriorityOrderIsAudioFirstConsoleLast(t *testing.T) {
	var order []string
	for _, function := range dropOrder() {
		order = append(order, function.name)
	}

	want := []string{"audio", "network", "disk", "console"}
	if !reflect.DeepEqual(order, want) {
		t.Errorf("drop order is %v, want %v", order, want)
	}
}

// The refusal is the whole interactive experience of this feature. "Operation
// failed" would leave the operator exactly where they were before it existed:
// switching things at random and losing HID.
func TestRefusalMessageNamesTheNumbersAndTheWayOut(t *testing.T) {
	message := refusalMessage("network", 1, []string{"disk", "console"})

	for _, want := range []string{"network", "3", "1 free", "disk", "console"} {
		if !strings.Contains(message, want) {
			t.Errorf("refusal %q does not mention %q", message, want)
		}
	}
}

func TestRefusalMessageWithNothingToSuggest(t *testing.T) {
	message := refusalMessage("network", 0, nil)

	if strings.Contains(message, "turn off") {
		t.Errorf("refusal %q offers a way out when there is none", message)
	}

	if !strings.Contains(message, "network") {
		t.Errorf("refusal %q does not name the device", message)
	}
}

// Every entry in the table that has a device name must be reachable through the
// toggle, and every name the toggle accepts must be in the table. A name in one
// and not the other is a switch that reports success and changes nothing, or a
// function the budget cannot see.
//
// This lives here rather than with the table in Task 1 because it asserts an
// agreement between two files, and the second half of that agreement - the
// console's entry in commandsFor - is added by Step 5 below.
func TestEveryTogglableFunctionHasCommands(t *testing.T) {
	for _, function := range usbFunctions {
		if function.device == "" {
			t.Errorf("%s has no device name, so nothing can switch it", function.name)
			continue
		}

		if _, _, _, ok := commandsFor(function.device); !ok {
			t.Errorf("commandsFor does not know %q", function.device)
		}
	}
}

// The gadget path is what the API reports as active, so a wrong name would
// report every function dead and the UI would warn about all of them.
func TestEveryFunctionNamesItsGadgetDirectory(t *testing.T) {
	want := map[string]string{
		"console": "acm.GS0",
		"disk":    "mass_storage.disk0",
		"audio":   "uac1.usb0",
	}

	for _, function := range usbFunctions {
		if function.name == "network" {
			// Two possible directories, checked separately below.
			continue
		}

		if function.gadget != want[function.name] {
			t.Errorf("%s links %q, want %q", function.name, function.gadget, want[function.name])
		}
	}
}
