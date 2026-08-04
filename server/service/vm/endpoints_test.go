package vm

import (
	"reflect"
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
	ok, free, _ := canEnable("audio", presence(virtualConsole))

	if !ok {
		t.Error("refused audio with 2 endpoints free")
	}

	if free != 2 {
		t.Errorf("reported %d free, want 2", free)
	}
}

func TestCanEnableRefusesWhatDoesNotFit(t *testing.T) {
	// console(3) + disk(2) + hid(3) is 8, so nothing is left.
	ok, free, relief := canEnable("network", presence(virtualConsole, virtualDisk))

	if ok {
		t.Error("allowed the network with no endpoints free")
	}

	if free != 0 {
		t.Errorf("reported %d free, want 0", free)
	}

	// Naming something that would not free enough is worse than naming
	// nothing: the operator turns it off and is refused again.
	if !reflect.DeepEqual(relief, []string{"disk", "console"}) {
		t.Errorf("suggested %v, want [disk console]", relief)
	}
}

// Every suggestion has to actually make room, and the cheapest sufficient one
// comes first so the operator gives up as little as possible.
func TestCanEnableOnlySuggestsFunctionsThatFreeEnough(t *testing.T) {
	_, _, relief := canEnable("audio", presence(virtualConsole, virtualDisk))

	for _, name := range relief {
		cost, ok := endpointCost(name)
		if !ok {
			t.Fatalf("suggested %q, which is not a function", name)
		}

		if cost < 1 {
			t.Errorf("suggested %q, which frees %d and does not help", name, cost)
		}
	}
}

func TestEndpointCostRejectsUnknownNames(t *testing.T) {
	if _, ok := endpointCost("speaker"); ok {
		t.Error("endpointCost accepted a name it does not know")
	}
}

// The console is accounted for but has no toggle, so the API must not offer it
// as a device somebody can switch on.
func TestConsoleIsAccountedButNotTogglable(t *testing.T) {
	if _, ok := endpointCost("console"); !ok {
		t.Error("the console is missing from the table")
	}

	if _, _, _, ok := commandsFor("console"); ok {
		t.Error("commandsFor offers the console as a togglable device")
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
