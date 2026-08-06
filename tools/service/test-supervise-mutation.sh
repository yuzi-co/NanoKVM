#!/bin/sh
# Prove test-supervise.sh is not vacuous.
#
#   test-supervise-mutation.sh [path-to-S98supervise] [path-to-S95nanokvm]
#
# Each mutation below is a defect a person could plausibly write. The suite has
# to fail on every one of them. A suite that passes a mutated script is a suite
# that would have passed the defect.
#
# Mutations keep the script parsable on purpose. A mutant that fails `sh -n`
# would be caught for the wrong reason and would prove nothing about the cases.
HERE=$(cd "$(dirname "$0")" && pwd)
SV=${1:-$HERE/S98supervise}
S95=${2:-$HERE/../../kvmapp/system/init.d/S95nanokvm}
[ -f "$SV" ] || { echo "usage: test-supervise-mutation.sh <S98supervise>"; exit 1; }

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
fails=0

echo "== the shipped script"
if sh "$HERE/test-supervise.sh" "$SV" "$S95" > "$WORK/clean.out" 2>&1; then
    echo "   passes  <- expected"
else
    echo "   the shipped script does not pass its own suite:"
    sed 's/^/     /' "$WORK/clean.out"
    exit 1
fi

echo
mutate() {
    desc="$1"; expr="$2"
    sed "$expr" "$SV" > "$WORK/mutant"

    if ! sh -n "$WORK/mutant" 2>/dev/null; then
        echo "   BROKEN MUTATION (does not parse): $desc"
        fails=$((fails + 1))
        return 0
    fi

    if cmp -s "$SV" "$WORK/mutant"; then
        echo "   MUTATION CHANGED NOTHING: $desc"
        fails=$((fails + 1))
        return 0
    fi

    if sh "$HERE/test-supervise.sh" "$WORK/mutant" "$S95" > /dev/null 2>&1; then
        echo "   NOT CAUGHT: $desc"
        fails=$((fails + 1))
    else
        echo "   caught: $desc"
    fi
}

echo "== the latch, which is the only thing between this and a reboot cycle"
# The floor sets the period of a cycle and does not prevent one: the counters do
# not survive a reboot, so a fault present from boot escalates again as soon as
# uptime passes the floor. Only "this server answered at least once since this
# boot" tells a board that broke from a board that never worked.
mutate "the served-ever latch is inverted" 's/"\$served_ever" != yes/"\$served_ever" = yes/'
mutate "the latch can never fire"          's/\[ "\$served_ever" != yes \]/[ 1 -eq 2 ]/'
# should_clear must not reach the latch. Clearing it with the counters restores
# the cycle exactly, and no unit case can see that - only the count of the
# places that assign it.
mutate "the latch is cleared with the counters" 's/^            cures=0$/            served_ever=no/'

echo
echo "== the probe's third answer, which is what keeps the latch honest"
# serving fails open so that a broken probe never kills a working KVM. That
# answer must not also be the answer the latch reads: on a board without curl
# the latch would set on the first poll, a fault present from boot would
# accumulate, and the board would reboot every REBOOT_FLOOR seconds forever.
# Measured against the loop with this defect present: reboot at uptime 620.
mutate "a missing curl reports the server as answering" 's/|| return 2$/|| return 0/'
# The other direction is worse than the bug it would fix. action reading "could
# not probe" as "not serving" reports hung on every poll, and the supervisor
# then kills and restarts a healthy KVM once a minute.
mutate "a probe that could not run is read as a hang" 's/"\$answered" -ne 1/"\$answered" -eq 0/'
mutate "the latch takes a probe that could not run"   's/"\$answered" -eq 0/"\$answered" -ne 1/'

echo
echo "== the floor, which sets how often a reboot cycle could turn"
# The next expression also rewrites the assignment at the top of the file,
# because SUPERVISE_REBOOT_FLOOR:-600 contains the pattern as a substring. It is
# harmless - that line sits outside every extracted block, and the mutant is
# transient - but an anchored expression written here will not do what its
# author expects.
mutate "the floor is removed"            's/REBOOT_FLOOR:-600/REBOOT_FLOOR:-0/'
mutate "the floor comparison is inverted" 's/"\$up" -lt/"\$up" -ge/'
mutate "the floor is off by one"          's/"\$up" -lt/"\$up" -le/'

echo
echo "== failing closed on input the comparisons cannot use"
# A test that errors is a test that is false, so an unparseable value skips the
# floor rather than blocking on it. Every one of these mutations puts a value
# back into a comparison that cannot evaluate it.
mutate "an empty value is not rejected"   "s/''|/'x'|/"
mutate "letters count as numbers"         's/\[!0-9\]/[!0-9a-z]/'
mutate "the floor's own value is unchecked" 's/^               "\${REBOOT_FLOOR:-600}" /               /'
# A digit filter is not a range check, and the failure mode is the same one:
# busybox `[` errors on a value it cannot hold, so the floor skips itself.
mutate "the length bound is too wide to matter" 's/-le 10 \]/-le 99 ]/'

echo
echo "== the crash-loop threshold"
mutate "a single short run is a loop"     's/CRASH_LOOP_N:-5/CRASH_LOOP_N:-1/'
mutate "the threshold is unreachable"     's/CRASH_LOOP_N:-5/CRASH_LOOP_N:-99/'
mutate "the loop count is off by one"     's/"\$short_runs" -ge/"\$short_runs" -gt/'

echo
echo "== the hang threshold"
mutate "one failed cure is enough"        's/HANG_CURES_K:-2/HANG_CURES_K:-1/'
mutate "the cure count is off by one"     's/"\$failed_cures" -ge/"\$failed_cures" -gt/'

echo
echo "== what the board says it did, which is all anyone has afterwards"
mutate "the no-reboot switch is inverted" 's/"\${NO_REBOOT:-0}" = 1/"${NO_REBOOT:-0}" = 0/'
mutate "the two hang reasons collapse into one" \
    's/hung: the process did not leave after SIGKILL/hung: 2 cures did not restore service/'

echo
echo "== the counters"
# Same substring trap as the floor above: SUPERVISE_SHORT_RUN:-30 ends with this
# pattern, so the next expression rewrites the top-of-file assignment too.
mutate "a short run is five seconds again" 's/SHORT_RUN:-30/SHORT_RUN:-5/'
mutate "the run length is off by one"      's/"\$1" -lt "\${SHORT_RUN/"\$1" -le "\${SHORT_RUN/'
mutate "a short run does not accumulate"   's/echo \$(( \$2 + 1 ))/echo 1/'
mutate "the first hang counts as a failure" 's/"\$1" -gt 0/"\$1" -ge 0/'

echo
echo "== clearing the counters"
# should_clear exists because action() reports healthy for a process that is up
# and not answering yet, and the hang branch resets LAST_OK after every cure -
# so clearing on the verdict name alone would wipe the counters before the
# counted hang escalation could ever reach its threshold.
mutate "any healthy verdict clears the counters" 's/if \[ "\$2" = yes \]/if true/'
# The other direction on the arm that now carries the whole decision. A single
# s/// cannot reach across the arm and its body, so this matches the fallthrough
# label and pulls the next line into the pattern space with N before
# substituting - portable to busybox sed as well as GNU. The eight leading
# spaces keep it off the two other `*)` arms in the file, which sit at four.
mutate "a verdict other than healthy clears" '/^        \*)/{N;s/echo no/echo yes/}'

echo
if [ "$fails" -eq 0 ]; then
    echo "===== every mutation was caught ====="
else
    echo "===== $fails mutation(s) survived - those cases are vacuous ====="
    exit 1
fi
