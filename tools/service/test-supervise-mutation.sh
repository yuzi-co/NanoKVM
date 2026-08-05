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

echo "== the floor, which is the only thing between this and a board that must be opened"
mutate "the floor is removed"            's/REBOOT_FLOOR:-600/REBOOT_FLOOR:-0/'
mutate "the floor comparison is inverted" 's/"\$up" -lt/"\$up" -ge/'
mutate "the floor is off by one"          's/"\$up" -lt/"\$up" -le/'

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
echo "== the counters"
mutate "a short run is five seconds again" 's/SHORT_RUN:-30/SHORT_RUN:-5/'
mutate "the run length is off by one"      's/"\$1" -lt "\${SHORT_RUN/"\$1" -le "\${SHORT_RUN/'
mutate "a short run does not accumulate"   's/echo \$(( \$2 + 1 ))/echo 1/'
mutate "the first hang counts as a failure" 's/"\$1" -gt 0/"\$1" -ge 0/'

echo
if [ "$fails" -eq 0 ]; then
    echo "===== every mutation was caught ====="
else
    echo "===== $fails mutation(s) survived - those cases are vacuous ====="
    exit 1
fi
