package vm

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

const initScript = "../../../kvmapp/system/init.d/S03usbdev"

func readInitScript(t *testing.T) string {
	t.Helper()

	body, err := os.ReadFile(filepath.FromSlash(initScript))
	if err != nil {
		t.Fatalf("cannot read %s: %s", initScript, err)
	}

	return string(body)
}

// shellCosts pulls the case arms out of usb_cost:
//
//	console) echo 3 ;;
func shellCosts(t *testing.T, script string) map[string]int {
	t.Helper()

	start := strings.Index(script, "usb_cost() {")
	if start < 0 {
		t.Fatal("S03usbdev has no usb_cost function")
	}

	end := strings.Index(script[start:], "\n}")
	if end < 0 {
		t.Fatal("usb_cost is not terminated")
	}

	arm := regexp.MustCompile(`(?m)^\s*([a-z]+}?\))\s*echo\s+(\d+)`)
	costs := make(map[string]int)

	for _, match := range arm.FindAllStringSubmatch(script[start:start+end], -1) {
		name := strings.TrimSuffix(match[1], ")")
		if name == "*" {
			continue
		}

		value, err := strconv.Atoi(match[2])
		if err != nil {
			t.Fatalf("usb_cost gives %q a non-numeric cost %q", name, match[2])
		}

		costs[name] = value
	}

	return costs
}

// The costs decide whether a set fits. If the two copies disagree the guard
// permits a set the boot script then refuses to build, or the other way round,
// and the symptom is the silent HID loss this whole feature exists to prevent.
func TestShellAndGoAgreeOnEveryCost(t *testing.T) {
	costs := shellCosts(t, readInitScript(t))

	if len(costs) != len(usbFunctions) {
		t.Errorf("usb_cost names %d functions, the Go table has %d: %v vs %v",
			len(costs), len(usbFunctions), costs, usbFunctions)
	}

	for _, function := range usbFunctions {
		shell, ok := costs[function.name]
		if !ok {
			t.Errorf("usb_cost has no arm for %q", function.name)
			continue
		}

		if shell != function.cost {
			t.Errorf("%s costs %d in S03usbdev and %d in Go", function.name, shell, function.cost)
		}
	}
}

// The order decides what is given up. Disagreement here loses the wrong
// function, and the one that matters is the console: it is the only way into a
// board whose network is gone.
func TestShellAndGoAgreeOnTheDropOrder(t *testing.T) {
	script := readInitScript(t)

	order := regexp.MustCompile(`usb_drop_order\(\) \{\s*echo "([^"]+)"`).FindStringSubmatch(script)
	if order == nil {
		t.Fatal("S03usbdev has no usb_drop_order function")
	}

	shell := strings.Fields(order[1])

	var goOrder []string
	for _, function := range dropOrder() {
		goOrder = append(goOrder, function.name)
	}

	if !reflect.DeepEqual(shell, goOrder) {
		t.Errorf("S03usbdev drops %v, Go drops %v", shell, goOrder)
	}
}

// usb_resolve - the function that actually decides what gets built at boot -
// reads usb_keep_order, not usb_drop_order. dropOrder() is otherwise used
// only by tests, so pinning just the drop order leaves the order that decides
// boot behaviour checked only transitively, through
// tools/service/test-usb-endpoints.sh asserting that the keep order and the
// drop order are each other's reverse. This test pins usb_keep_order directly
// against the Go table - highest priority first, the reverse of dropOrder() -
// so drift in the boot-critical order is caught here even if that other
// script is ever deleted.
func TestShellAndGoAgreeOnTheKeepOrder(t *testing.T) {
	script := readInitScript(t)

	order := regexp.MustCompile(`usb_keep_order\(\) \{\s*echo "([^"]+)"`).FindStringSubmatch(script)
	if order == nil {
		t.Fatal("S03usbdev has no usb_keep_order function")
	}

	shell := strings.Fields(order[1])

	drop := dropOrder()
	goOrder := make([]string, len(drop))
	for i, function := range drop {
		goOrder[len(drop)-1-i] = function.name
	}

	if !reflect.DeepEqual(shell, goOrder) {
		t.Errorf("S03usbdev keeps %v, Go's dropOrder() reversed keeps %v", shell, goOrder)
	}
}

// usb_hid_cost has two arms and only one of them is hidCost: the disable_hid
// arm must charge nothing, and the other arm must charge exactly hidCost. A
// substring search over the whole function body would stay green if the two
// arms were swapped - HID would then cost nothing while still being built,
// the guard would approve 9 endpoints of optional functions on top of the 3
// HID actually uses, the gadget would ask for 12 against a budget of 9, and
// every /dev/hidg* would disappear along with everything else.
var hidCostShape = regexp.MustCompile(
	`(?s)if\s+usb_marker\s+disable_hid\s*\n\s*then\s*\n\s*echo\s+(\d+)\s*\n\s*else\s*\n\s*echo\s+(\d+)`)

func TestShellAndGoAgreeOnWhatHidCosts(t *testing.T) {
	script := readInitScript(t)

	start := strings.Index(script, "usb_hid_cost() {")
	if start < 0 {
		t.Fatal("S03usbdev has no usb_hid_cost function")
	}

	end := strings.Index(script[start:], "\n}")
	if end < 0 {
		t.Fatal("usb_hid_cost is not terminated")
	}

	body := script[start : start+end]

	arms := hidCostShape.FindStringSubmatch(body)
	if arms == nil {
		t.Fatalf("usb_hid_cost does not have the if usb_marker disable_hid / then / else shape this test parses:\n%s", body)
	}

	disabled, err := strconv.Atoi(arms[1])
	if err != nil {
		t.Fatalf("usb_hid_cost's disable_hid arm echoes %q, which is not a number", arms[1])
	}

	built, err := strconv.Atoi(arms[2])
	if err != nil {
		t.Fatalf("usb_hid_cost's else arm echoes %q, which is not a number", arms[2])
	}

	if disabled != 0 {
		t.Errorf("usb_hid_cost charges %d when disable_hid is present, want 0", disabled)
	}

	if built != hidCost {
		t.Errorf("usb_hid_cost charges %d when HID is built, Go's hidCost is %d", built, hidCost)
	}
}

// shellGadgetDirs pulls the case arms out of usb_gadget_dirs:
//
//	network) echo "ncm.usb0 rndis.usb0" ;;
func shellGadgetDirs(t *testing.T, script string) map[string][]string {
	t.Helper()

	start := strings.Index(script, "usb_gadget_dirs() {")
	if start < 0 {
		t.Fatal("S03usbdev has no usb_gadget_dirs function")
	}

	end := strings.Index(script[start:], "\n}")
	if end < 0 {
		t.Fatal("usb_gadget_dirs is not terminated")
	}

	arm := regexp.MustCompile(`(?m)^\s*([a-z]+)\)\s*echo\s+"([^"]*)"`)
	dirs := make(map[string][]string)

	for _, match := range arm.FindAllStringSubmatch(script[start:start+end], -1) {
		dirs[match[1]] = strings.Fields(match[2])
	}

	return dirs
}

// usb_gadget_dirs is what the boot script's prune reads to decide which config
// symlinks to remove, and the Go table's gadget/gadgetAlt is what the UI reads
// to decide whether a function is actually running. They are two hand-kept
// copies of the same names. If the shell copy loses one, the prune stops
// removing it: a function the budget dropped stays linked from an earlier
// start, the total goes back over 9, and the gadget refuses to bind with every
// /dev/hidg* gone - the exact failure this feature exists to prevent.
func TestShellAndGoAgreeOnEveryGadgetDirectory(t *testing.T) {
	dirs := shellGadgetDirs(t, readInitScript(t))

	if len(dirs) != len(usbFunctions) {
		t.Errorf("usb_gadget_dirs names %d functions, the Go table has %d: %v",
			len(dirs), len(usbFunctions), dirs)
	}

	for _, function := range usbFunctions {
		want := []string{function.gadget}
		if function.gadgetAlt != "" {
			want = append(want, function.gadgetAlt)
		}

		shell, ok := dirs[function.name]
		if !ok {
			t.Errorf("usb_gadget_dirs has no arm for %q", function.name)
			continue
		}

		if !reflect.DeepEqual(shell, want) {
			t.Errorf("%s is %v in S03usbdev and %v in Go", function.name, shell, want)
		}
	}

	// HID is gated on /boot/disable_hid alone and never reaches the keep set,
	// so a hid.GS* arm here would hand the prune the keyboard and both mice.
	for name, shell := range dirs {
		for _, dir := range shell {
			if strings.HasPrefix(dir, "hid.") {
				t.Errorf("usb_gadget_dirs maps %q to %q - the prune would unlink a HID function", name, dir)
			}
		}
	}
}

// Both copies carry the same measured constant. Nothing derives the budget
// from dmesg or from the kernel at all - it is 9 in Go and 9 in the shell
// because both were set by hand from the same measurement, and this test is
// what notices if a future edit moves only one of them.
func TestShellAndGoAgreeOnTheBudget(t *testing.T) {
	script := readInitScript(t)

	budget := regexp.MustCompile(`usb_budget\(\) \{\s*echo (\d+)`).FindStringSubmatch(script)
	if budget == nil {
		t.Fatal("S03usbdev has no usb_budget function, or it does not echo a constant")
	}

	shell, err := strconv.Atoi(budget[1])
	if err != nil {
		t.Fatalf("usb_budget echoes %q, which is not a number", budget[1])
	}

	if shell != DefaultEndpointBudget {
		t.Errorf("S03usbdev budgets %d endpoints, Go budgets %d", shell, DefaultEndpointBudget)
	}
}

// The budget is measured, and the kernel's own number disagrees with it. A
// later change that "fixes" the constant by parsing dmesg would silently refuse
// acm+network and acm+disk+audio, both of which bind on hardware.
func TestTheShellBudgetIsNotReadFromDmesg(t *testing.T) {
	script := readInitScript(t)

	start := strings.Index(script, "usb_budget() {")
	if start < 0 {
		t.Fatal("S03usbdev has no usb_budget function")
	}

	end := strings.Index(script[start:], "\n}")
	if end < 0 {
		t.Fatal("usb_budget is not terminated")
	}

	if strings.Contains(script[start:start+end], "dmesg") {
		t.Error("usb_budget consults dmesg, which reports 8 while 9 endpoints bind")
	}
}
