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

// HID's share is the largest single number in the budget and it is spelled in
// both copies.
func TestShellAndGoAgreeOnWhatHidCosts(t *testing.T) {
	script := readInitScript(t)

	start := strings.Index(script, "usb_hid_cost() {")
	if start < 0 {
		t.Fatal("S03usbdev has no usb_hid_cost function")
	}

	end := strings.Index(script[start:], "\n}")
	body := script[start : start+end]

	if !strings.Contains(body, strconv.Itoa(hidCost)) {
		t.Errorf("usb_hid_cost does not mention %d, which is hidCost in Go", hidCost)
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

	if strings.Contains(script[start:start+end], "dmesg") {
		t.Error("usb_budget consults dmesg, which reports 8 while 9 endpoints bind")
	}
}
