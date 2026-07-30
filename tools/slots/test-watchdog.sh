#!/bin/sh
# Exercise the watchdog decision logic taken straight out of the script that
# ships, so the test cannot drift from it.
#
#   test-watchdog.sh [path-to-S00awatchdog]
#
# Not destructive: the revert cases run against a temporary directory standing
# in for /boot.
WD=${1:-$(dirname "$0")/device/S00awatchdog}
[ -f "$WD" ] || { echo "usage: test-watchdog.sh <S00awatchdog>"; exit 1; }

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

sed -n '/^# --- health check ---$/,/^# --- end health check ---$/p'   "$WD" > "$WORK/health.sh"
sed -n '/^# --- revert target ---$/,/^# --- end revert target ---$/p' "$WD" > "$WORK/revert.sh"
[ -s "$WORK/health.sh" ] || { echo "could not extract the health block"; exit 1; }
[ -s "$WORK/revert.sh" ] || { echo "could not extract the revert block"; exit 1; }

fails=0
note() { printf '  %-58s %s\n' "$1" "$2"; [ "$2" = FAIL ] && fails=$((fails + 1)); return 0; }

echo "===== is there a way in? ====="
# The watchdog exists to answer one question: can this board still be reached?
# Either door is enough, so it must not revert while one of them is open. Both
# doors need the network, so a board with no address is unreachable however
# healthy its services are.
health_case() {
    got=$(NET=$1 WEB=$2 SSH=$3 WORK="$WORK" sh -c '
        net_up() { [ "$NET" = up ]; }
        web_up() { [ "$WEB" = up ]; }
        ssh_up() { [ "$SSH" = up ]; }
        . "$WORK/health.sh"
        healthy && echo healthy || echo revert
    ')
    if [ "$got" = "$4" ]; then
        note "net=$1 web=$2 ssh=$3 -> $got" OK
    else
        note "net=$1 web=$2 ssh=$3 -> $got, want $4" FAIL
    fi
}

health_case up   up   up   healthy
health_case up   up   down healthy
health_case up   down up   healthy
health_case up   down down revert
health_case down up   up   revert
health_case down down down revert

echo
echo "===== which slot does it fall back to? ====="
# A slot that boots once and breaks later would otherwise be restored onto
# itself, and the board would reboot for ever. Falling through to slot A ends
# that: slot A is the stock root filesystem and always boots.
revert_case() {
    desc="$1"; slot="$2"; good="$3"; want="$4"
    B="$WORK/boot"; rm -rf "$B"; mkdir -p "$B"
    [ "$slot" = NONE ] || printf '%s\n' "$slot" > "$B/slot"
    [ "$good" = NONE ] || printf '%s\n' "$good" > "$B/slot.good"
    printf 'current.img\n' > "$B/ver"
    [ "$good" = NONE ] || printf 'good.img\n' > "$B/ver.good"

    BOOT="$B" WORK="$WORK" sh -c '. "$WORK/revert.sh"; revert' > /dev/null 2>&1

    if [ -f "$B/slot" ]; then got=$(cat "$B/slot"); else got=NONE; fi
    if [ "$got" = "$want" ]; then
        note "$desc -> $got" OK
    else
        note "$desc -> $got, want $want" FAIL
    fi
}

revert_case "a different known-good slot is restored" \
            "loop:/new.img" "loop:/old.img" "loop:/old.img"
revert_case "known-good equals the failing slot, so slot A" \
            "loop:/new.img" "loop:/new.img" "NONE"
revert_case "no known-good recorded, so slot A" \
            "loop:/new.img" "NONE" "NONE"
revert_case "already on slot A, nothing to undo" \
            "NONE" "NONE" "NONE"

echo
echo "===== the version follows the slot ====="
B="$WORK/boot"; rm -rf "$B"; mkdir -p "$B"
printf 'loop:/new.img\n' > "$B/slot";  printf 'loop:/old.img\n' > "$B/slot.good"
printf 'new.img\n'       > "$B/ver";   printf 'old.img\n'       > "$B/ver.good"
BOOT="$B" WORK="$WORK" sh -c '. "$WORK/revert.sh"; revert' > /dev/null 2>&1
[ "$(cat "$B/ver")" = "old.img" ] \
    && note "restoring the slot also restores /boot/ver" OK \
    || note "/boot/ver is now $(cat "$B/ver"), want old.img" FAIL

echo
if [ "$fails" -eq 0 ]; then
    echo "===== all watchdog cases pass ====="
else
    echo "===== $fails case(s) failed ====="
    exit 1
fi
