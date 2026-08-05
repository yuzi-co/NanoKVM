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
echo "===== usb_hid_cost, unstubbed ====="
# Every case above stubs usb_hid_cost so the arithmetic can pick HID's cost
# directly. That leaves the real function itself untested: a mutation that
# charges HID nothing would let console+disk+network+audio (9) through the
# budget on top of the 3 endpoints HID actually uses, the gadget would ask
# for 12 against 9, and the bind would fail with all HID gone.
hid_cost_case() {
    desc="$1"; marker="$2"; want="$3"
    got=$(WORK="$WORK" MARKER="$marker" sh -c '
        . "$WORK/budget.sh"
        BOOT="$WORK/boot-hidcost-$$"; mkdir -p "$BOOT"
        [ -n "$MARKER" ] && : > "$BOOT/$MARKER"
        usb_marker() { [ -e "$BOOT/$1" ]; }
        usb_hid_cost
    ')
    [ "$got" = "$want" ] && note "$desc -> $got" OK || note "$desc -> $got, want $want" FAIL
}
hid_cost_case "hid built by default"       ""            3
hid_cost_case "disable_hid marker present" disable_hid   0

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
echo "===== usb_enabled reads the marker each function actually ships with ====="
# Only the network pair was exercised above. A typo in one of the other three
# markers (usb.uac -> usb.uac1, usb.disk0 -> usb.disk, usb.acm -> usb.acm2)
# would make usb_enabled silently under-report what is on, usb_resolve would
# then approve an over-budget set because it never sees the function it
# missed, and the gadget would refuse to bind.
enabled_n=0
enabled_case() {
    marker="$1"; want="$2"
    enabled_n=$((enabled_n + 1))
    got=$(WORK="$WORK" N="$enabled_n" MARKER="$marker" sh -c '
        . "$WORK/budget.sh"
        BOOT="$WORK/boot-enabled-$N"; mkdir -p "$BOOT"
        : > "$BOOT/$MARKER"
        usb_marker() { [ -e "$BOOT/$1" ]; }
        usb_enabled
    ')
    got=$(echo $got)
    [ "$got" = "$want" ] && note "$marker enables [$want]" OK || note "$marker gave [$got], want [$want]" FAIL
}
enabled_case usb.acm   console
enabled_case usb.disk0 disk
enabled_case usb.uac   audio

echo
echo "===== the script still parses ====="
sh -n "$SV" && note "S03usbdev is valid shell" OK || note "S03usbdev does not parse" FAIL

# A wiring check. Every case above tests the block in isolation, so the block
# could be correct and never called.
grep -q 'usb_keep=\$(usb_resolve ' "$SV" \
    && note "start_usb_dev resolves the enabled set" OK \
    || note "the budget block is never called" FAIL

# Each function must be gated on usb_kept, not reconstructed from a marker
# test that the resolve step never touches. Anchored to the exact indentation
# and the exact end of the line, so a commented-out gate - which a plain
# substring grep would still count as present - reports as missing.
gate_case() {
    desc="$1"; pattern="$2"
    grep -qE "$pattern" "$SV" \
        && note "$desc is gated on usb_kept" OK \
        || note "$desc still tests its marker directly" FAIL
}
gate_case "the console"        '^    if usb_kept console$'
gate_case "the disk"           '^    if usb_kept disk$'
gate_case "ncm"                '^    if usb_kept network && \[ -e /boot/usb\.ncm \]$'
gate_case "rndis0"             '^        if usb_kept network && \[ -e /boot/usb\.rndis0 \]$'
gate_case "the os_desc block"  '^    if usb_kept network$'
gate_case "audio"              '^    if usb_kept audio$'

# The one gate that must never appear. HID is the one function with no
# exception - a "usb_kept hid" here would always be false, because hid never
# appears in usb_keep_order, and every boot would come up without a keyboard.
hid_block=$(sed -n '/^    if \[ ! -e \/boot\/disable_hid \]$/,/^    fi$/p' "$SV")
if [ -n "$hid_block" ] && ! printf '%s\n' "$hid_block" | grep -q usb_kept; then
    note "HID is gated only on its own marker, never on usb_kept" OK
else
    note "HID's gate changed, or now depends on usb_kept" FAIL
fi

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
