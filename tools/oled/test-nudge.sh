#!/bin/sh
# Exercise the nudge logic taken straight out of the script that ships, so the
# test cannot drift from it.
#
#   test-nudge.sh [path-to-S97oled-nudge]
#
# Not destructive: no I2C write happens here. The probe is stubbed.
ND=${1:-$(dirname "$0")/S97oled-nudge}
[ -f "$ND" ] || { echo "usage: test-nudge.sh <S97oled-nudge>"; exit 1; }

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

sed -n '/^# --- offset sequence ---$/,/^# --- end offset sequence ---$/p' "$ND" > "$WORK/seq.sh"
sed -n '/^# --- device detection ---$/,/^# --- end device detection ---$/p' "$ND" > "$WORK/find.sh"
[ -s "$WORK/seq.sh" ]  || { echo "could not extract the offset sequence block"; exit 1; }
[ -s "$WORK/find.sh" ] || { echo "could not extract the detection block"; exit 1; }

fails=0
note() { printf '  %-58s %s\n' "$1" "$2"; [ "$2" = FAIL ] && fails=$((fails + 1)); return 0; }

echo "===== the offset walks and never jumps ====="
# A saw-tooth would snap the whole screen back in one step, which reads as a
# glitch. A triangle returns the same way it went out, so every move is one row.
got=$(WORK="$WORK" sh -c '. "$WORK/seq.sh"; s=0; while [ $s -lt 14 ]; do printf "%s " "$(triangle $s 3)"; s=$((s+1)); done')
want="0 1 2 3 2 1 0 1 2 3 2 1 0 1 "
[ "$got" = "$want" ] && note "max=3 walks 0 1 2 3 2 1 and repeats" OK \
                     || note "got [$got] want [$want]" FAIL

got=$(WORK="$WORK" sh -c '. "$WORK/seq.sh"; s=0; while [ $s -lt 6 ]; do printf "%s " "$(triangle $s 1)"; s=$((s+1)); done')
want="0 1 0 1 0 1 "
[ "$got" = "$want" ] && note "max=1 alternates 0 1" OK || note "got [$got] want [$want]" FAIL

# max=0 is how an operator disables the movement without removing the script.
got=$(WORK="$WORK" sh -c '. "$WORK/seq.sh"; s=0; while [ $s -lt 4 ]; do printf "%s " "$(triangle $s 0)"; s=$((s+1)); done')
[ "$got" = "0 0 0 0 " ] && note "max=0 never moves" OK || note "max=0 gave [$got]" FAIL

echo
echo "===== the offset stays inside what the panel accepts ====="
# Display offset is a 6 bit field: 0x00 to 0x3F. A value above it would set
# unrelated bits in the command.
out=$(WORK="$WORK" sh -c '. "$WORK/seq.sh"; s=0; while [ $s -lt 400 ]; do triangle $s 63; s=$((s+1)); done' | sort -n)
lo=$(echo "$out" | head -1); hi=$(echo "$out" | tail -1)
[ "$lo" -ge 0 ] && [ "$hi" -le 63 ] && note "max=63 stays within 0..63 (saw $lo..$hi)" OK \
                                    || note "max=63 produced $lo..$hi" FAIL

# Asking for more than the panel has must be clamped, not wrapped.
got=$(WORK="$WORK" sh -c '. "$WORK/seq.sh"; clamp_max 200')
[ "$got" = "63" ] && note "a max above the field is clamped to 63" OK || note "clamp_max 200 = $got" FAIL
got=$(WORK="$WORK" sh -c '. "$WORK/seq.sh"; clamp_max -5')
[ "$got" = "0" ] && note "a negative max becomes 0" OK || note "clamp_max -5 = $got" FAIL

echo
echo "===== finding the panel ====="
# hw=beta puts the display on bus 5, hw=alpha on bus 1, and the PCIe board
# answers at 0x3c instead of 0x3d. Probe rather than trust one of them.
find_case() {
    desc="$1"; present="$2"; want="$3"
    got=$(PRESENT="$present" WORK="$WORK" sh -c '
        probe() { case " $PRESENT " in *" $1:$2 "*) return 0 ;; esac; return 1; }
        . "$WORK/find.sh"
        find_oled || echo none
    ')
    [ "$got" = "$want" ] && note "$desc -> $got" OK || note "$desc -> $got, want $want" FAIL
}

find_case "beta board, 0x3d on bus 5"  "5:0x3d"          "5 0x3d"
find_case "alpha board, 0x3d on bus 1" "1:0x3d"          "1 0x3d"
find_case "PCIe board, 0x3c on bus 5"  "5:0x3c"          "5 0x3c"
find_case "nothing on any bus"         ""                "none"
find_case "two panels: the first found wins" "1:0x3d 5:0x3d" "1 0x3d"

echo
if [ "$fails" -eq 0 ]; then
    echo "===== all nudge cases pass ====="
else
    echo "===== $fails case(s) failed ====="
    exit 1
fi
