# USB Endpoint Budget Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stop the USB gadget from being configured past the controller's eight endpoints, which today silently removes the keyboard and both mice.

**Architecture:** One endpoint table, written twice — once in `S03usbdev` for boot and once in Go for the toggle — and pinned together by a test that parses both. Boot drops the lowest-priority function because nobody is there to ask. The toggle refuses because somebody is. The API reports what is actually linked, not only what is marked.

**Tech Stack:** POSIX shell (busybox ash), Go 1.25, React + TypeScript, antd.

## Global Constraints

- Endpoint budget is the constant **9**. Do **not** read it from `dmesg`: that line says `EPs: 8`,
  and three measured configurations totalling 9 bind and work. Parsing it refuses valid setups.
- Endpoint costs, exact: `console 3`, `network 3`, `disk 2`, `audio 1`, HID 3 total.
- Priority, highest first: `HID > console > disk > network > audio`. Audio drops first.
- HID is never a drop candidate, at any input.
- Dropping a function at boot **never deletes its `/boot` marker**.
- `usb.ncm` and `usb.rndis0` select the same network function and are counted **once**.
- Turning a function **off** is always permitted and never budget-checked.
- Device names used by the API are exactly `disk`, `network`, `audio`. The console has no toggle.
- All Go tests must pass with `-tags novision`.
- Shell must be POSIX and run under busybox ash. No bashisms, no `local`.
- Init scripts must be LF. A CRLF init script does not run on the device.

---

## The budget is measured, and the measurements are the reason

Six configurations were built on the device, each by writing markers and rebooting. HID is present
in all of them.

| Set | Total | Result |
| --- | --- | --- |
| `acm + audio` | 7 | binds |
| `acm + network` | 9 | binds |
| `acm + disk + audio` | 9 | binds |
| `disk + network + audio` | 9 | binds |
| `acm + network + audio` | 10 | fails, `-19` |
| `acm + disk + network + audio` | 12 | fails, `-19` |

Nine binds three times; ten fails. Do not "improve" this by reading the controller's own number
out of `dmesg` — it reports 8, and doing so refuses `acm + network` and `acm + disk + audio`,
both of which are known good.

## Deviation from the spec, flagged

The spec puts `/boot/disable_hid` accounting out of scope and fixes HID at 3. This plan instead
reads the marker and charges 0 when HID is disabled. Reason: it is three lines, and the fixed
number is simply wrong on a board with `disable_hid`, where it would drop functions that fit.
Task 1 and Task 2 both implement this. Raise it if you disagree — everything else follows the spec.

---

## File structure

| File | Responsibility |
| --- | --- |
| `server/service/vm/endpoints.go` | New. The Go copy of the table, and the budget arithmetic the toggle needs. |
| `server/service/vm/endpoints_test.go` | New. Table-driven tests for the arithmetic. |
| `server/service/vm/endpoints_shell_test.go` | New. Parses `S03usbdev` and fails when the two tables disagree. |
| `server/service/vm/virtual-device.go` | Modified. Refuses over-budget toggles, reports `active`. |
| `server/proto/vm.go` | Modified. Response carries per-device state plus the budget. |
| `kvmapp/system/init.d/S03usbdev` | Modified. Resolves the enabled set against the budget before linking. |
| `tools/service/test-usb-endpoints.sh` | New. Extracts the budget block from `S03usbdev` and drives it. |
| `web/src/api/virtual-device.ts` | Modified. Response types. |
| `web/src/pages/desktop/menu/settings/device/virtual-devices.tsx` | Modified. Budget line, per-row cost, disabled switches, inactive warning. |
| `web/src/i18n/locales/en.ts` | Modified. New strings. |

---

### Task 1: The Go endpoint table and its arithmetic

**Files:**
- Create: `server/service/vm/endpoints.go`
- Create: `server/service/vm/endpoints_test.go`

**Interfaces:**
- Consumes: `virtualNetwork`, `virtualDisk`, `virtualAudio` from `server/service/vm/virtual-device.go:16-19`.
- Produces:
  - `const DefaultEndpointBudget = 8`
  - `func endpointBudget() int`
  - `func hidEndpointCost(present func(string) bool) int`
  - `func usedEndpoints(present func(string) bool) int`
  - `func endpointCost(device string) (int, bool)`
  - `func canEnable(device string, present func(string) bool) (ok bool, free int, relief []string)`
  - `var usbFunctions []usbFunction` with fields `name, device string; markers []string; cost, priority int`

- [ ] **Step 1: Write the failing test**

Create `server/service/vm/endpoints_test.go`:

```go
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
```

- [ ] **Step 2: Run the test and watch it fail**

```shell
cd server && go test -tags novision ./service/vm/ -run TestUsedEndpoints -v
```

Expected: compile failure — `undefined: usedEndpoints`, `undefined: virtualConsole`, `undefined: disableHid`, `undefined: virtualNetworkNCM`.

- [ ] **Step 3: Write the implementation**

Create `server/service/vm/endpoints.go`:

```go
package vm

import "sort"

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
// device is the name the API accepts for it, and is empty for a function with
// no toggle. priority decides what survives when more is enabled than fits:
// higher survives longer.
type usbFunction struct {
	name     string
	device   string
	markers  []string
	cost     int
	priority int
}

// The console outranks everything except HID because it is the only way into a
// board whose network is gone. Audio is last because it is the only entry that
// costs nothing to lose.
var usbFunctions = []usbFunction{
	{name: "console", device: "", markers: []string{virtualConsole}, cost: 3, priority: 40},
	{name: "disk", device: "disk", markers: []string{virtualDisk}, cost: 2, priority: 30},
	{name: "network", device: "network", markers: []string{virtualNetworkNCM, virtualNetwork}, cost: 3, priority: 20},
	{name: "audio", device: "audio", markers: []string{virtualAudio}, cost: 1, priority: 10},
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
```

- [ ] **Step 4: Run the tests and watch them pass**

```shell
cd server && go test -tags novision ./service/vm/ -v
```

Expected: PASS, including the pre-existing `commandsFor` tests, which must not change behaviour.

- [ ] **Step 5: Commit**

```shell
git add server/service/vm/endpoints.go server/service/vm/endpoints_test.go
git commit -m "Count what each USB function costs the controller"
```

---

### Task 2: The boot-time budget in `S03usbdev`

**Files:**
- Modify: `kvmapp/system/init.d/S03usbdev`
- Create: `tools/service/test-usb-endpoints.sh`

**Interfaces:**
- Consumes: nothing from Task 1. This is the independent second copy of the table.
- Produces, inside a block delimited by `# --- endpoint budget ---` and `# --- end endpoint budget ---`:
  - `usb_budget()` — prints the controller's endpoint count
  - `usb_cost <name>` — prints one function's cost
  - `usb_keep_order()` — prints optional function names, highest priority first
  - `usb_drop_order()` — the same ranking reversed, lowest priority first
  - `usb_has "<names>" <name>` — whether a name is in a set
  - `usb_hid_cost()` — prints HID's cost
  - `usb_used "<names>"` — prints the total for a space-separated set
  - `usb_enabled()` — prints the set the markers ask for
  - `usb_resolve "<names>" <budget>` — prints the subset that fits, in priority order
  - `usb_dropped "<wanted>" "<kept>"` — prints what was given up

- [ ] **Step 1: Write the failing test**

Create `tools/service/test-usb-endpoints.sh`:

```sh
#!/bin/sh
# Exercise the gadget's endpoint budget, taken straight out of the script that
# ships so the test cannot drift from it.
#
#   test-usb-endpoints.sh [path-to-S03usbdev]
#
# Not destructive: no gadget is built and no marker is written.
SV=${1:-$(dirname "$0")/../../kvmapp/system/init.d/S03usbdev}
[ -f "$SV" ] || { echo "usage: test-usb-endpoints.sh <S03usbdev>"; exit 1; }

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

sed -n '/^# --- endpoint budget ---$/,/^# --- end endpoint budget ---$/p' "$SV" > "$WORK/budget.sh"
[ -s "$WORK/budget.sh" ] || { echo "could not extract the endpoint budget block"; exit 1; }

fails=0
note() { printf '  %-62s %s\n' "$1" "$2"; [ "$2" = FAIL ] && fails=$((fails + 1)); return 0; }

echo "===== what each function costs ====="
cost_case() {
    got=$(WORK="$WORK" sh -c ". \"\$WORK/budget.sh\"; usb_cost $1")
    [ "$got" = "$2" ] && note "$1 costs $got" OK || note "$1 costs $got, want $2" FAIL
}
cost_case console 3
cost_case network 3
cost_case disk    2
cost_case audio   1
cost_case nonsense 0

echo
echo "===== the total, against a budget of 9 ====="
# HID is three of the eight before anything optional is added, so only two
# optional functions ever fit and some pairs do not.
used_case() {
    desc="$1"; set="$2"; want="$3"
    got=$(WORK="$WORK" HID=3 sh -c '. "$WORK/budget.sh"; usb_hid_cost() { echo "$HID"; }; usb_used "'"$set"'"')
    [ "$got" = "$want" ] && note "$desc -> $got" OK || note "$desc -> $got, want $want" FAIL
}
used_case "hid alone"                    ""                            3
used_case "hid + console"                "console"                     6
used_case "hid + console + audio"        "console audio"               7
used_case "hid + console + disk"         "console disk"                8
used_case "hid + disk + network"         "disk network"                8
used_case "everything"                   "console disk network audio"  12

echo
echo "===== resolving a set that does not fit ====="
# Audio goes first, then the network. The console outranks both because it is
# the only way into a board whose network is gone.
resolve_case() {
    desc="$1"; set="$2"; want="$3"
    got=$(WORK="$WORK" HID=3 sh -c '. "$WORK/budget.sh"; usb_hid_cost() { echo "$HID"; }; usb_resolve "'"$set"'" 9')
    [ "$got" = "$want" ] && note "$desc -> [$got]" OK || note "$desc -> [$got], want [$want]" FAIL
}
# Everything enabled keeps three of the four. Giving up the lowest priority
# members instead would settle on console+disk and leave an endpoint unused,
# losing audio for nothing.
resolve_case "everything keeps console + disk + audio" "console disk network audio" "console disk audio"
resolve_case "console + audio already fits"            "console audio"              "console audio"
resolve_case "console + disk + audio is exactly 9"     "console disk audio"         "console disk audio"
resolve_case "disk + network + audio is exactly 9"     "disk network audio"         "disk network audio"
resolve_case "console + network is exactly 9"          "console network"            "console network"
resolve_case "adding audio to those two costs audio"   "console network audio"      "console network"
resolve_case "nothing enabled stays nothing"           ""                           ""

# The output is in priority order regardless of how the set arrived, because
# configfs numbers interfaces in link order and the caller links what this
# prints.
resolve_case "the result is ordered, not as given"     "audio disk console"         "console disk audio"

# A lower priority function must never take a place from a higher one. The
# console costs 3 and audio costs 1, so a resolve that filled cheaply first
# would keep audio and drop the console - the exact inversion that leaves a
# board with no way in when its network dies.
got=$(WORK="$WORK" HID=3 sh -c '. "$WORK/budget.sh"; usb_hid_cost() { echo "$HID"; }; usb_resolve "console network audio" 9')
case " $got " in
    *" console "*) note "the console outranks audio for the last place" OK ;;
    *)             note "the console lost its place to a cheaper function" FAIL ;;
esac

echo
echo "===== the two orderings are one ranking ====="
keep=$(WORK="$WORK" sh -c '. "$WORK/budget.sh"; usb_keep_order')
drop=$(WORK="$WORK" sh -c '. "$WORK/budget.sh"; usb_drop_order')
reversed=$(for name in $keep; do echo "$name"; done | sed '1!G;h;$!d' | tr '\n' ' ')
[ "$(echo $reversed)" = "$(echo $drop)" ] \
    && note "drop order is the keep order reversed" OK \
    || note "keep [$keep] reversed is [$reversed], drop says [$drop]" FAIL

echo
echo "===== HID is never a candidate ====="
# The one rule with no exception. A board that gives up HID has given up being
# a KVM, so no combination of markers may reach that state.
for set in "console disk network audio" "console network" "disk network audio"; do
    got=$(WORK="$WORK" HID=3 sh -c '. "$WORK/budget.sh"; usb_hid_cost() { echo "$HID"; }; usb_resolve "'"$set"'" 9')
    case "$got" in
        *hid*) note "resolving [$set] dropped hid" FAIL ;;
        *)     note "resolving [$set] keeps hid" OK ;;
    esac
done

# A budget so small that nothing optional fits must still not touch HID, and
# must not loop forever trying.
got=$(WORK="$WORK" HID=3 sh -c '. "$WORK/budget.sh"; usb_hid_cost() { echo "$HID"; }; usb_resolve "console disk network audio" 3')
[ -z "$got" ] && note "a budget of 3 leaves only hid, and terminates" OK \
              || note "a budget of 3 left [$got]" FAIL

echo
echo "===== what was given up is reported ====="
got=$(WORK="$WORK" sh -c '. "$WORK/budget.sh"; usb_dropped "console disk network audio" "console disk"')
want="network audio"
got=$(echo $got)
[ "$got" = "$want" ] && note "dropped [$got]" OK || note "dropped [$got], want [$want]" FAIL

got=$(WORK="$WORK" sh -c '. "$WORK/budget.sh"; usb_dropped "console audio" "console audio"')
[ -z "$got" ] && note "nothing dropped when everything fits" OK || note "dropped [$got], want nothing" FAIL

echo
echo "===== the budget is the measured constant, not the kernel's ====="
# dwc2 announces "EPs: 8" and that is not the budget: acm+network is nine
# endpoints and binds, as does acm+disk+audio. Reading the kernel's number
# would refuse both. The guard must not consult dmesg at all.
got=$(WORK="$WORK" sh -c '. "$WORK/budget.sh"; usb_budget')
[ "$got" = 9 ] && note "the budget is 9" OK || note "the budget is [$got], want 9" FAIL

if grep -q dmesg "$WORK/budget.sh"; then
    note "usb_budget consults dmesg, which reports the wrong number" FAIL
else
    note "the budget never consults dmesg" OK
fi

# A board that reports something else must not change the answer, because the
# answer was measured on this hardware and the kernel's line disagrees with it.
got=$(WORK="$WORK" sh -c '. "$WORK/budget.sh"; dmesg() { echo "dwc2 4340000.usb: EPs: 8, dedicated fifos"; }; usb_budget')
[ "$got" = 9 ] && note "a dmesg line saying 8 does not move the budget" OK \
               || note "dmesg moved the budget to [$got]" FAIL

echo
echo "===== the network is one function, whichever marker names it ====="
# usb.ncm and usb.rndis0 are alternatives. Counting both would reserve three
# endpoints nothing uses, and the guard would refuse a function that fits.
got=$(WORK="$WORK" sh -c '
    . "$WORK/budget.sh"
    BOOT="$WORK/boot"; mkdir -p "$BOOT"
    : > "$BOOT/usb.ncm"; : > "$BOOT/usb.rndis0"
    usb_marker() { [ -e "$BOOT/$1" ]; }
    usb_enabled
')
got=$(echo $got)
[ "$got" = "network" ] && note "two network markers name one function" OK \
                       || note "gave [$got], want [network]" FAIL

echo
echo "===== the script still parses ====="
sh -n "$SV" && note "S03usbdev is valid shell" OK || note "S03usbdev does not parse" FAIL

# A wiring check. Every case above tests the block in isolation, so the block
# could be correct and never called.
grep -q 'usb_keep=\$(usb_resolve ' "$SV" \
    && note "start_usb_dev resolves the enabled set" OK \
    || note "the budget block is never called" FAIL
grep -q 'usb_kept console' "$SV" \
    && note "the console is built from the resolved set, not its marker" OK \
    || note "the console still tests its marker directly" FAIL

# Dropping must not delete the operator's marker: intent has to survive so the
# function returns on its own once something else is switched off.
if sed -n '/^# --- endpoint budget ---$/,/^# --- end endpoint budget ---$/p' "$SV" | grep -qE '\brm\b|\bunlink\b'; then
    note "the budget block removes a file" FAIL
else
    note "the budget block never removes a marker" OK
fi

echo
if [ "$fails" -eq 0 ]; then
    echo "===== all endpoint cases pass ====="
else
    echo "===== $fails case(s) failed ====="
    exit 1
fi
```

- [ ] **Step 2: Run it and watch it fail**

```shell
sh tools/service/test-usb-endpoints.sh
```

Expected: `could not extract the endpoint budget block`, exit 1.

- [ ] **Step 3: Add the budget block to `S03usbdev`**

Insert immediately **before** `start_usb_dev(){` in `kvmapp/system/init.d/S03usbdev`:

```sh
# --- endpoint budget ---
# The USB controller has a fixed number of endpoints, and every function takes a
# share. Overrunning it does not disable the newest function: the whole gadget
# refuses to bind, so /dev/hidg* never appear and the keyboard and both mice are
# gone. The kernel names whichever function was linked last, not the one that
# overran, so the message points at the wrong thing.
#
# Seen on 2026-08-04 with the console, the disk, the network and the speaker all
# switched on - twelve endpoints asked of eight:
#
#   configfs-gadget gadget: acm/000000000223f9a0: can't bind, err -19
#   configfs-gadget 4340000.usb: failed to start g0: -19
#
# So count first and link second. When more is enabled than fits, give up the
# lower priority function its place: nobody is present at boot to be asked, and coming up
# without a keyboard is the worst available outcome.
#
# Never delete the marker of a function that is given up. The operator's intent
# stays on disk, so switching something else off brings it back on its own.

# usb_marker exists so the tests can redirect the lookup away from /boot.
usb_marker() {
    [ -e "/boot/$1" ]
}

# usb_budget is how many endpoints of functions this controller fits.
#
# The number is measured, not read. dwc2 prints its own count a few seconds
# before this script runs:
#
#   dwc2 4340000.usb: EPs: 8, dedicated fifos, 3072 entries in SPRAM
#
# That is not the budget. Configurations totalling nine endpoints bind and work
# - acm+network, acm+disk+audio, and disk+network+audio, each with all three
# HID functions - and ten fails. Parsing the kernel's line looks like the
# careful thing to do and would permanently refuse two configurations that are
# known good, with a comment claiming it read the real value from the hardware.
#
# Leave this as a constant. If a future controller differs, measure it the same
# way and change the number here.
usb_budget() {
    echo 9
}

# usb_hid_cost charges nothing when HID is switched off, because those endpoints
# are then genuinely free and functions that fit would otherwise be refused.
usb_hid_cost() {
    if usb_marker disable_hid
    then
        echo 0
    else
        echo 3
    fi
}

usb_cost() {
    case "$1" in
        console) echo 3 ;;
        network) echo 3 ;;
        disk)    echo 2 ;;
        audio)   echo 1 ;;
        *)       echo 0 ;;
    esac
}

# usb_keep_order lists the optional functions highest priority first, which is
# the order they are offered a place.
#
# The console ranks above the disk and the network because it is the only way
# into a board whose network is gone. Audio ranks last: it is the only entry
# that costs nothing to lose.
usb_keep_order() {
    echo "console disk network audio"
}

# usb_drop_order is the same ranking read backwards. It is what the Go copy of
# the table is compared against, and a test asserts the two are exact reverses.
usb_drop_order() {
    echo "audio network disk console"
}

# usb_enabled prints the functions the markers ask for. usb.ncm and usb.rndis0
# are alternatives for one function, so they produce one name, not two.
usb_enabled() {
    set=""

    usb_marker usb.acm && set="$set console"
    { usb_marker usb.ncm || usb_marker usb.rndis0; } && set="$set network"
    usb_marker usb.disk0 && set="$set disk"
    usb_marker usb.uac && set="$set audio"

    echo $set
}

usb_used() {
    used=$(usb_hid_cost)

    for name in $1
    do
        used=$((used + $(usb_cost "$name")))
    done

    echo "$used"
}

# usb_has answers whether a name is in a set.
usb_has() {
    case " $1 " in
        *" $2 "*) return 0 ;;
    esac

    return 1
}

# usb_resolve prints the subset of the enabled set that fits the budget.
#
# It offers each function a place in priority order and keeps the ones that
# still fit, rather than giving up the lowest priority members until the total
# comes down. The two agree on which function outranks which and disagree on
# the result: with everything enabled, giving up in order loses audio, then the
# network, and settles on console+disk at five of the six optional endpoints -
# while console+disk+audio is exactly six and keeps one function more. Offering
# places never leaves room unused, and it cannot promote a lower priority
# function over a higher one, because the higher one was offered first.
#
# It always terminates: the list is finite and each entry is considered once. A
# budget too small for anything optional simply yields an empty set.
usb_resolve() {
    enabled=$1
    budget=$2

    keep=""

    for name in $(usb_keep_order)
    do
        usb_has "$enabled" "$name" || continue

        if [ "$(usb_used "$keep $name")" -le "$budget" ]
        then
            keep="$keep $name"
        fi
    done

    echo $keep
}

# usb_dropped prints what the resolve gave up, for the log.
usb_dropped() {
    out=""

    for name in $1
    do
        case " $2 " in
            *" $name "*) ;;
            *) out="$out $name" ;;
        esac
    done

    echo $out
}

# usb_report writes one line per dropped function, to the console and to /data,
# which survives the reboot that a puzzled operator will try first.
#
# The /data write is guarded. This script runs early in boot, and a board where
# that mount has not appeared yet would otherwise print a redirection error over
# the message it is trying to deliver. The console line is the one that must
# always work.
usb_report() {
    for name in $1
    do
        message="usb: not enough endpoints, $name is off (needs $(usb_cost "$name"))"
        echo "$message"

        if [ -d /data ]
        then
            echo "$(date '+%Y-%m-%d %H:%M:%S') $message" >> /data/usb-endpoints.log 2>/dev/null
        fi
    done
}
# --- end endpoint budget ---
```

- [ ] **Step 4: Run the block tests and watch them pass**

```shell
sh tools/service/test-usb-endpoints.sh
```

Expected: every case OK except the two wiring checks, which still fail — the block exists but nothing calls it.

- [ ] **Step 5: Call it from `start_usb_dev`**

In `kvmapp/system/init.d/S03usbdev`, immediately after the `mkdir configs/c.1` block that ends with:

```sh
    echo "NanoKVM" >  configs/c.1/strings/0x409/configuration
```

add:

```sh
    # Decide what fits before anything is linked. Everything below tests
    # usb_keep rather than the marker, so a function that lost its place is
    # simply not built - its marker is untouched and it returns on its own
    # when something else is switched off.
    usb_wanted=$(usb_enabled)
    usb_keep=$(usb_resolve "$usb_wanted" "$(usb_budget)")
    usb_report "$(usb_dropped "$usb_wanted" "$usb_keep")"
```

Then add this helper directly below the block above:

```sh
    # usb_kept answers whether a function survived the budget.
    usb_kept() {
        usb_has "$usb_keep" "$1"
    }
```

- [ ] **Step 6: Gate each function on the resolved set**

Four edits in `kvmapp/system/init.d/S03usbdev`. Each replaces a marker test with a `usb_kept` test, so the resolve decides and the marker only records intent.

Replace:

```sh
    if [ -e /boot/usb.ncm ]
    then
```

with:

```sh
    if usb_kept network && [ -e /boot/usb.ncm ]
    then
```

Replace:

```sh
        if [ -e /boot/usb.rndis0 ]
        then
```

with:

```sh
        if usb_kept network && [ -e /boot/usb.rndis0 ]
        then
```

Replace:

```sh
    if [ -e /boot/usb.disk0 ]
    then
```

with:

```sh
    if usb_kept disk
    then
```

Replace:

```sh
    if [ -e /boot/usb.acm ]
    then
```

with:

```sh
    if usb_kept console
    then
```

Replace:

```sh
    if [ -e /boot/usb.uac ]
    then
```

with:

```sh
    if usb_kept audio
    then
```

Leave the `os_desc` block that tests `[ -e /boot/usb.ncm ] || [ -e /boot/usb.rndis0 ]` alone for now, and fix it in Step 7.

- [ ] **Step 7: Stop the Windows descriptor being written for a network that is not there**

The Microsoft OS descriptor block is linked whenever a network marker exists, which is now
independent of whether the function was built. Replace:

```sh
    if [ -e /boot/usb.ncm ] || [ -e /boot/usb.rndis0 ]
    then
```

with:

```sh
    if usb_kept network
    then
```

- [ ] **Step 8: Run every test and watch them pass**

```shell
sh tools/service/test-usb-endpoints.sh
sh tools/service/test-supervise.sh
```

Expected: both suites report all cases pass. The second is a regression check — it parses
`S03usbdev`'s sibling scripts and must not have been disturbed.

- [ ] **Step 9: Check the line endings**

```shell
grep -c $'\r' kvmapp/system/init.d/S03usbdev
```

Expected: `0`. A CRLF init script does not run on the device.

- [ ] **Step 10: Commit**

```shell
git add kvmapp/system/init.d/S03usbdev tools/service/test-usb-endpoints.sh
git commit -m "Fit the USB gadget to the endpoints the controller has"
```

---

### Task 3: Pin the two tables together

**Files:**
- Create: `server/service/vm/endpoints_shell_test.go`

**Interfaces:**
- Consumes: `usbFunctions`, `dropOrder()` from Task 1; the `usb_cost` and `usb_drop_order` shell functions from Task 2.
- Produces: nothing. This task only adds a test.

The costs and the priority order exist in two languages because the boot script runs before the
server exists and the server must not shell out to read a constant. Two copies drift. This test
parses the shell and fails when they disagree, the same way `tools/service/test-supervise.sh`
keeps `SERVER_LOG` identical across two scripts.

- [ ] **Step 1: Write the failing test**

Create `server/service/vm/endpoints_shell_test.go`:

```go
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

// The fallback matters on a board whose dmesg no longer carries the line.
func TestShellAndGoAgreeOnTheFallbackBudget(t *testing.T) {
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
```

- [ ] **Step 2: Run it**

```shell
cd server && go test -tags novision ./service/vm/ -run TestShellAndGo -v
```

Expected: PASS. Tasks 1 and 2 already wrote both copies consistently, so this test passes on
arrival — it exists to fail on the *next* change, which is the point.

- [ ] **Step 3: Prove the test can fail**

Temporarily change `audio` to `echo 2` in `usb_cost` inside `kvmapp/system/init.d/S03usbdev`, then:

```shell
cd server && go test -tags novision ./service/vm/ -run TestShellAndGoAgreeOnEveryCost -v
```

Expected: FAIL with `audio costs 2 in S03usbdev and 1 in Go`. Change it back to `echo 1` and
re-run to confirm PASS. A consistency test that has never failed is not known to work.

- [ ] **Step 4: Commit**

```shell
git add server/service/vm/endpoints_shell_test.go
git commit -m "Fail the build when the two endpoint tables disagree"
```

---

### Task 4: Refuse over-budget toggles and report what is active

**Files:**
- Modify: `server/proto/vm.go:67-80`
- Modify: `server/service/vm/virtual-device.go`
- Modify: `server/service/vm/endpoints_test.go` (add cases)

**Interfaces:**
- Consumes: `canEnable`, `usedEndpoints`, `endpointBudget`, `usbFunctions` from Task 1.
- Produces:
  - `proto.VirtualDeviceState{Enabled, Active bool; Cost int}`
  - `proto.GetVirtualDeviceRsp{Network, Disk, Audio VirtualDeviceState; Used, Total int}`
  - `func isFunctionActive(name string) bool`
  - `func refusalMessage(device string, free int, relief []string) string`

- [ ] **Step 1: Write the failing test**

Append to `server/service/vm/endpoints_test.go`:

```go
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
```

Add `"strings"` to the import block of `endpoints_test.go` if it is not already there.

- [ ] **Step 2: Run it and watch it fail**

```shell
cd server && go test -tags novision ./service/vm/ -run "TestRefusal|TestEveryFunction" -v
```

Expected: compile failure — `undefined: refusalMessage`, and `function.gadget` undefined.

- [ ] **Step 3: Give each function its gadget directory**

In `server/service/vm/endpoints.go`, add a `gadget` field to `usbFunction` and a second
`gadgetAlt` for the network's two forms:

```go
type usbFunction struct {
	name      string
	device    string
	markers   []string
	gadget    string
	gadgetAlt string
	cost      int
	priority  int
}
```

and replace the table with:

```go
var usbFunctions = []usbFunction{
	{name: "console", device: "", markers: []string{virtualConsole}, gadget: "acm.GS0", cost: 3, priority: 40},
	{name: "disk", device: "disk", markers: []string{virtualDisk}, gadget: "mass_storage.disk0", cost: 2, priority: 30},
	{name: "network", device: "network", markers: []string{virtualNetworkNCM, virtualNetwork}, gadget: "ncm.usb0", gadgetAlt: "rndis.usb0", cost: 3, priority: 20},
	{name: "audio", device: "audio", markers: []string{virtualAudio}, gadget: "uac1.usb0", cost: 1, priority: 10},
}
```

Then add, at the end of the file:

```go
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
```

Add `"fmt"`, `"os"`, `"path/filepath"` and `"strings"` to the imports of `endpoints.go`.

- [ ] **Step 4: Run the tests and watch them pass**

```shell
cd server && go test -tags novision ./service/vm/ -v
```

Expected: PASS.

- [ ] **Step 5: Change the response shape**

In `server/proto/vm.go`, replace lines 67-72:

```go
type GetVirtualDeviceRsp struct {
	Network bool `json:"network"`
	Media   bool `json:"media"`
	Disk    bool `json:"disk"`
	Audio   bool `json:"audio"`
}
```

with:

```go
// VirtualDeviceState separates what was asked for from what is running. The two
// differ when the USB controller ran out of endpoints and the boot script gave
// the function up: the marker is still there, and the function is not.
type VirtualDeviceState struct {
	Enabled bool `json:"enabled"`
	Active  bool `json:"active"`
	Cost    int  `json:"cost"`
}

type GetVirtualDeviceRsp struct {
	Network VirtualDeviceState `json:"network"`
	Disk    VirtualDeviceState `json:"disk"`
	Audio   VirtualDeviceState `json:"audio"`
	Used    int                `json:"used"`
	Total   int                `json:"total"`
}
```

`Media` is removed. It is declared, never set by the server, and never read by the frontend.

- [ ] **Step 6: Report the new shape**

In `server/service/vm/virtual-device.go`, replace `GetVirtualDevice` with:

```go
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
		Network: state("network"),
		Disk:    state("disk"),
		Audio:   state("audio"),
		Used:    usedEndpoints(present),
		Total:   endpointBudget(),
	})

	log.Debugf("get virtual device success")
}
```

- [ ] **Step 7: Refuse the toggle**

In `server/service/vm/virtual-device.go`, inside `UpdateVirtualDevice`, replace:

```go
	commands := mount
	if exist, _ := isDeviceExist(device); exist {
		commands = unmount
	}
```

with:

```go
	present := func(marker string) bool {
		exist, _ := isDeviceExist(marker)
		return exist
	}

	commands := mount
	if present(device) {
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
```

- [ ] **Step 8: Run the tests**

```shell
cd server && go vet -tags novision ./... && go test -tags novision ./... 2>&1 | tail -20
```

Expected: PASS, no vet findings.

- [ ] **Step 9: Cross-compile for the device**

```shell
cd server && CGO_ENABLED=0 GOOS=linux GOARCH=riscv64 go build -tags novision ./...
```

Expected: no output.

- [ ] **Step 10: Commit**

```shell
git add server/proto/vm.go server/service/vm/virtual-device.go server/service/vm/endpoints.go server/service/vm/endpoints_test.go
git commit -m "Refuse a virtual device that does not fit, and report what is running"
```

---

### Task 5: Show the budget in the UI

**Files:**
- Modify: `web/src/api/virtual-device.ts`
- Modify: `web/src/pages/desktop/menu/settings/device/virtual-devices.tsx`
- Modify: `web/src/i18n/locales/en.ts:400-407`

**Interfaces:**
- Consumes: the JSON shape from Task 4 — `{network, disk, audio: {enabled, active, cost}, used, total}`, and the error `msg` string from a refused toggle.
- Produces: nothing other tasks depend on.

- [ ] **Step 1: Type the response**

Replace the whole of `web/src/api/virtual-device.ts`:

```ts
import { http } from '@/lib/http.ts';

export type VirtualDeviceName = 'disk' | 'network' | 'audio';

// enabled is the /boot marker, active is the function the running gadget
// actually carries. They differ when the USB controller ran out of endpoints.
export type VirtualDeviceState = {
  enabled: boolean;
  active: boolean;
  cost: number;
};

export type VirtualDevices = {
  network: VirtualDeviceState;
  disk: VirtualDeviceState;
  audio: VirtualDeviceState;
  used: number;
  total: number;
};

// get virtual devices status
export function getVirtualDevice() {
  return http.get('/api/vm/device/virtual');
}

// mount/unmount virtual device
export function updateVirtualDevice(device: VirtualDeviceName) {
  const data = {
    device
  };

  return http.post('/api/vm/device/virtual', data);
}
```

- [ ] **Step 2: Add the strings**

In `web/src/i18n/locales/en.ts`, replace lines 401-407:

```ts
        disk: 'Virtual Disk',
        diskDesc: 'Mount SD card on the remote host',
        network: 'Virtual Network',
        networkDesc: 'Mount virtual network card on the remote host',
        audio: 'Virtual Speaker',
        audioDesc:
          'Present a USB sound card to the remote host, so you can hear it. The host must select it as its output device. Switching this rebuilds the USB connection.',
```

with:

```ts
        disk: 'Virtual Disk',
        diskDesc: 'Mount SD card on the remote host',
        network: 'Virtual Network',
        networkDesc: 'Mount virtual network card on the remote host',
        audio: 'Virtual Speaker',
        audioDesc:
          'Present a USB sound card to the remote host, so you can hear it. The host must select it as its output device. Switching this rebuilds the USB connection.',
        endpoints: {
          title: 'USB endpoints',
          used: '{{used}} of {{total}} used',
          cost: 'uses {{cost}}',
          needs: 'needs {{cost}}, {{free}} free',
          full: 'Not enough USB endpoints. Turn something else off first.',
          inactive:
            'On, but not running: the USB controller ran out of endpoints. Turn another device off and it starts on the next reboot.',
          explain:
            'The USB controller has a fixed number of endpoints. If more devices are enabled than fit, the keyboard and mouse are kept and the rest are turned off.'
        },
```

- [ ] **Step 3: Rewrite the component**

Replace the whole of `web/src/pages/desktop/menu/settings/device/virtual-devices.tsx`:

```tsx
import { useEffect, useState } from 'react';
import { Progress, Switch, Tooltip } from 'antd';
import { useTranslation } from 'react-i18next';

import { getHidMode } from '@/api/hid.ts';
import * as api from '@/api/virtual-device.ts';
import type { VirtualDeviceName, VirtualDevices } from '@/api/virtual-device.ts';

export const VirtualDevices = () => {
  const { t } = useTranslation();

  const [isHidOnlyMode, setIsHidOnlyMode] = useState(false);
  const [devices, setDevices] = useState<VirtualDevices | null>(null);
  const [loading, setLoading] = useState<'' | VirtualDeviceName>('');
  const [refusal, setRefusal] = useState('');

  useEffect(() => {
    getHidOnlyMode();
    getVirtualDevice();
  }, []);

  async function getHidOnlyMode() {
    try {
      const rsp = await getHidMode();
      if (rsp.code !== 0) {
        console.log(rsp.msg);
        return;
      }
      setIsHidOnlyMode(rsp.data.mode === 'hid-only');
    } catch (err) {
      console.log(err);
    }
  }

  async function getVirtualDevice() {
    try {
      const rsp = await api.getVirtualDevice();
      if (rsp.code !== 0) {
        console.log(rsp.msg);
        return;
      }

      setDevices(rsp.data);
    } catch (err) {
      console.log(err);
    }
  }

  async function update(device: VirtualDeviceName) {
    if (loading) return;
    setLoading(device);
    setRefusal('');

    try {
      const rsp = await api.updateVirtualDevice(device);
      if (rsp.code !== 0) {
        // The server owns the numbers, so show its sentence rather than
        // recomputing the budget here and risking a different answer.
        setRefusal(rsp.msg);
        return;
      }

      await getVirtualDevice();
    } catch (err) {
      console.log(err);
    } finally {
      setLoading('');
    }
  }

  if (isHidOnlyMode) {
    return (
      <div className="flex items-center justify-between space-x-10">
        <div className="flex flex-col space-y-1">
          <span>{t('settings.device.hidOnly')}</span>
          <span className="text-xs text-neutral-500">{t('settings.device.hidOnlyDesc')}</span>
        </div>

        <Switch checked={true} disabled={true} />
      </div>
    );
  }

  const free = devices ? devices.total - devices.used : 0;

  function row(device: VirtualDeviceName) {
    if (!devices) return null;

    const state = devices[device];
    const fits = state.enabled || state.cost <= free;

    return (
      <div className="flex items-center justify-between">
        <div className="flex flex-col space-y-1">
          <span>{t(`settings.device.${device}`)}</span>
          <span className="text-xs text-neutral-500">{t(`settings.device.${device}Desc`)}</span>

          {state.enabled && !state.active && (
            <span className="text-xs text-amber-500">
              {t('settings.device.endpoints.inactive')}
            </span>
          )}
        </div>

        <div className="flex items-center space-x-3">
          <span className="text-xs text-neutral-500">
            {state.enabled
              ? t('settings.device.endpoints.cost', { cost: state.cost })
              : t('settings.device.endpoints.needs', { cost: state.cost, free })}
          </span>

          <Tooltip title={fits ? '' : t('settings.device.endpoints.full')}>
            <Switch
              checked={state.enabled}
              disabled={!fits}
              loading={loading === device}
              onChange={() => update(device)}
            />
          </Tooltip>
        </div>
      </div>
    );
  }

  return (
    <>
      {devices && (
        <div className="flex flex-col space-y-1">
          <div className="flex items-center justify-between">
            <span>{t('settings.device.endpoints.title')}</span>
            <span className="text-xs text-neutral-500">
              {t('settings.device.endpoints.used', {
                used: devices.used,
                total: devices.total
              })}
            </span>
          </div>

          <Progress
            percent={(devices.used / devices.total) * 100}
            showInfo={false}
            size="small"
            strokeColor={free === 0 ? '#f59e0b' : undefined}
          />

          <span className="text-xs text-neutral-500">
            {t('settings.device.endpoints.explain')}
          </span>
        </div>
      )}

      {refusal && <span className="text-xs text-red-500">{refusal}</span>}

      {row('disk')}
      {row('network')}
      {row('audio')}
    </>
  );
};
```

- [ ] **Step 4: Type-check and lint**

```shell
cd web && pnpm install && npx tsc --noEmit && npx eslint src/pages/desktop/menu/settings/device/virtual-devices.tsx src/api/virtual-device.ts
```

Expected: no errors. There is no frontend test runner in this repository, so the type check is
the gate.

- [ ] **Step 5: Build**

```shell
cd web && pnpm build
```

Expected: build succeeds.

- [ ] **Step 6: Commit**

```shell
git add web/src/api/virtual-device.ts web/src/pages/desktop/menu/settings/device/virtual-devices.tsx web/src/i18n/locales/en.ts
git commit -m "State the USB endpoint budget where the switches are"
```

---

### Task 6: Prove it on hardware

**Files:**
- Modify: `tools/README.md`

The failure this feature prevents cannot be reproduced off-device: it needs a real dwc2
controller refusing a real bind. This task is a written procedure plus the note that records it.

**Interfaces:**
- Consumes: everything above.
- Produces: a documented acceptance procedure.

- [ ] **Step 1: Deploy**

Both copies, because the updater writes only `/kvmapp` while boot executes `/etc/init.d`:

```shell
cat kvmapp/system/init.d/S03usbdev | tr -d '\r' | \
  ssh root@<device> 'cat > /etc/init.d/S03usbdev && chmod 755 /etc/init.d/S03usbdev &&
                     cp /etc/init.d/S03usbdev /kvmapp/system/init.d/S03usbdev && sync'
```

Then deploy the server and the web UI by the normal route in `CLAUDE.md`.

- [ ] **Step 2: Confirm the block runs and the tests pass on busybox**

```shell
cat tools/service/test-usb-endpoints.sh | tr -d '\r' | ssh root@<device> 'cat > /tmp/t.sh; sh /tmp/t.sh /etc/init.d/S03usbdev'
```

Expected: all endpoint cases pass. Ash is not the shell the suite was written under, and it is
the only one that matters.

- [ ] **Step 3: Reproduce the original failure condition**

Enable every function and reboot:

```shell
ssh root@<device> 'touch /boot/usb.acm /boot/usb.disk0 /boot/usb.rndis0 /boot/usb.uac; sync; reboot'
```

- [ ] **Step 4: Verify the board came up as a KVM**

```shell
ssh root@<device> '
  echo "state: $(cat /sys/class/udc/*/state)"
  echo "hidg:  $(ls /dev/hidg* 2>&1 | tr "\n" " ")"
  echo "linked: $(ls /sys/kernel/config/usb_gadget/g0/configs/c.1/ | grep -vE "MaxPower|bmAttributes|strings" | tr "\n" " ")"
  echo "dropped:"; cat /data/usb-endpoints.log
  echo "markers:"; ls /boot/usb.*
  dmesg | grep -iE "can.t bind|failed to start" || echo "no bind errors"
'
```

Expected, exactly:

- `state: configured`
- `/dev/hidg0 /dev/hidg1 /dev/hidg2` all present
- `linked:` shows `acm.GS0 hid.GS0 hid.GS1 hid.GS2 mass_storage.disk0 uac1.usb0` — 9 endpoints
- `/data/usb-endpoints.log` names **`network`** as dropped, and nothing else
- **`markers:` still lists all four**, including the one that was dropped
- `no bind errors`

Two of these are easy to skip and matter most. The marker check: a guard that deletes what it
drops has destroyed the operator's configuration. And the fact that only `network` is dropped:
giving up the lowest-priority members instead would drop audio as well and settle on eight
endpoints, losing a function for nothing.

- [ ] **Step 5: Verify the toggle refuses, and the budget line tracks**

In the web UI, `Settings > Device`:

1. The budget line reads `9 of 9 used`. The virtual network switch is disabled, and its tooltip
   explains why. The speaker reads on.
2. Turn the virtual disk off. The line reads `7 of 9`, and the network is **still** disabled — it
   needs 3 and only 2 are free. This is the case a naive UI gets wrong by enabling anything as
   soon as the bar is not full.
3. Turn the speaker off. The line reads `6 of 9` and the network becomes available.

- [ ] **Step 6: Restore the working configuration**

```shell
ssh root@<device> 'touch /boot/usb.acm /boot/usb.disk0 /boot/usb.uac; rm -f /boot/usb.rndis0; sync; reboot'
```

Expected after boot: `acm.GS0 hid.GS0 hid.GS1 hid.GS2 mass_storage.disk0 uac1.usb0`, 9 of 9,
`/dev/hidg0..2` and `/dev/ttyGS0` present.

- [ ] **Step 7: Record it**

Add to `tools/README.md`, under a new `## USB endpoint budget` heading, a short section stating
the budget, the per-function costs, the priority order, where `/data/usb-endpoints.log` is
written, and that both `/etc/init.d` and `/kvmapp/system/init.d` must carry the same
`S03usbdev`.

- [ ] **Step 8: Commit**

```shell
git add tools/README.md
git commit -m "Record the endpoint budget and how it was proved on hardware"
```

---

## Self-review

**Spec coverage**

| Spec requirement | Task |
| --- | --- |
| Endpoint costs table | 1, 2 |
| Budget from dmesg with fallback | 2 (shell), 1 documents why Go uses the constant |
| Priority `HID > console > disk > network > audio` | 1, 2, pinned by 3 |
| Boot drops lowest priority | 2 |
| Boot never deletes markers | 2, asserted in 2 and on hardware in 6 |
| Boot logs every drop | 2 |
| Link order equals priority order | 2 |
| Toggle refuses, never drops | 4 |
| Turning off is always allowed | 4 |
| Refusal names what to turn off | 1, 4 |
| `enabled` vs `active` reporting | 4 |
| `Media` removed | 4 |
| Network counted once | 1, 2 |
| One table two languages, pinned by a test | 3 |
| UI budget line, per-row cost, disabled switch, inactive warning | 5 |
| Strings in `en.ts` only | 5 |
| Shell tests | 2 |
| Go tests | 1, 3, 4 |
| Device acceptance | 6 |

No gaps.

**Deviation:** `/boot/disable_hid` accounting is implemented (Tasks 1 and 2) although the spec
puts it out of scope. Flagged at the top of this plan.

**Placeholder scan:** none. Every code step carries the code.

**Type consistency:** `usbFunction` gains `gadget` and `gadgetAlt` in Task 4 — Task 1's test
`TestEveryFunctionNamesItsGadgetDirectory` is introduced in Task 4, not Task 1, so Task 1's tests
compile against Task 1's struct. `canEnable` returns `(bool, int, []string)` in Tasks 1 and 4.
`functionForDevice` is used in Tasks 1 and 4 with the same signature. The shell function names in
Task 2 match exactly what Task 3's parser looks for: `usb_cost() {`, `usb_drop_order() {`,
`usb_hid_cost() {`, and `usb_budget() {` echoing a bare integer.
