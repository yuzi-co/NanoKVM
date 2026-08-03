#!/bin/sh
# Check that S95nanokvm waits for the old server to exit before it starts a new
# one.
#
#   test-restart-waits.sh [path-to-S95nanokvm]
#
# killall returns as soon as the signal is sent. The server catches SIGTERM and
# tears the capture pipeline down first, and that teardown reaches libkvm
# through cgo, where it can block. The old process is therefore still running,
# and still owns the VI pipeline, after killall returns.
#
# The next server then builds the media stack while the old one still holds it,
# and its channel enable reports ENOMEM. The old process keeps answering on the
# old build, so the restart looks like it worked.
#
# The wait has to come after the staged copies are removed, not before.
# S98supervise reads a staged binary with no process as a crash and starts a
# server itself, and the wait is long enough for it to do that. Removing
# /tmp/server first is how this script says the stop was deliberate.
#
# This runs the shipped function against stub processes and checks what it does,
# so it fails if the function stops waiting or stops forcing.
S95=${1:-$(dirname "$0")/../../kvmapp/system/init.d/S95nanokvm}
[ -f "$S95" ] || { echo "usage: test-restart-waits.sh <S95nanokvm>"; exit 1; }

fails=0
note() { printf '  %-64s %s\n' "$1" "$2"; [ "$2" = FAIL ] && fails=$((fails + 1)); return 0; }

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

echo "===== the wait ends, whether the server leaves or not ====="

# Run the shipped text rather than a copy of it.
sed -n '/^wait_for_stop() {/,/^}/p' "$S95" > "$work/func.sh"

if [ ! -s "$work/func.sh" ]; then
    note "the script defines wait_for_stop" FAIL
    echo
    echo "$fails case(s) FAILED"
    exit "$fails"
fi
note "the script defines wait_for_stop" OK

# The harness replaces killall, pidof and sleep. Each records what it was asked
# to do, and sleep does not really sleep, so a ten second timeout costs nothing.
cat > "$work/harness.sh" <<'HARNESS'
killall() {
    printf '%s\n' "killall $*" >> "$LOGFILE"
}

# The stub process is alive for EXIT_AFTER polls and gone after that. A value
# larger than the timeout is a process that never leaves.
pidof() {
    polls=$(cat "$WORK/polls" 2>/dev/null || echo 0)
    [ "$polls" -lt "$EXIT_AFTER" ]
}

sleep() {
    polls=$(cat "$WORK/polls" 2>/dev/null || echo 0)
    echo $((polls + 1)) > "$WORK/polls"
}
HARNESS

# run_case <exit_after> <timeout>
run_case() {
    : > "$work/log"
    echo 0 > "$work/polls"
    (
        LOGFILE=$work/log
        WORK=$work
        EXIT_AFTER=$1
        STOP_TIMEOUT=$2
        export LOGFILE WORK EXIT_AFTER STOP_TIMEOUT
        . "$work/harness.sh"
        . "$work/func.sh"
        wait_for_stop
    ) > "$work/out" 2>&1
}

# --- a server that stops when it is asked ---
run_case 2 10

if grep -q -- '-9' "$work/log"; then
    note "a server that stops is not forced" FAIL
    sed 's/^/    /' "$work/log"
else
    note "a server that stops is not forced" OK
fi

polls=$(cat "$work/polls")
if [ "$polls" -ge 2 ]; then
    note "it waits for the process to be gone ($polls poll(s))" OK
else
    note "it returns after $polls poll(s), so it does not wait" FAIL
fi

# --- a server whose teardown blocks ---
run_case 999 10

if grep -q -- '^killall -9 NanoKVM-Server$' "$work/log"; then
    note "a server that will not stop is forced" OK
else
    note "a server that will not stop is never forced, so it survives" FAIL
    sed 's/^/    /' "$work/log"
fi

polls=$(cat "$work/polls")
if [ "$polls" -le 12 ]; then
    note "it gives up after the timeout ($polls poll(s))" OK
else
    note "it waited $polls poll(s) for a timeout of 10, so the wait is unbounded" FAIL
fi

echo
echo "===== the arms call it in the right place ====="

# Line numbers within each arm. The order is the whole point of the fix, so
# check it rather than only checking that the call is present.
line_of() { printf '%s\n' "$2" | grep -n -- "$1" | head -1 | cut -d: -f1; }

for arm in restart stop; do
    body=$(sed -n "/^  $arm)/,/^   ;;/p" "$S95")

    kill_at=$(line_of '^ *killall NanoKVM-Server$' "$body")
    rm_at=$(line_of '^ *rm -r /tmp' "$body")
    wait_at=$(line_of '^ *wait_for_stop$' "$body")

    if [ -z "$wait_at" ]; then
        note "$arm) waits for the server to exit" FAIL
        continue
    fi

    if [ -n "$kill_at" ] && [ "$kill_at" -lt "$wait_at" ]; then
        note "$arm) asks the server to stop before it waits" OK
    else
        note "$arm) waits without having asked the server to stop" FAIL
    fi

    if [ -n "$rm_at" ] && [ "$rm_at" -lt "$wait_at" ]; then
        note "$arm) removes the staged copies before it waits" OK
    else
        note "$arm) waits with a binary still staged, so S98supervise may start one" FAIL
    fi
done

# The one that matters: nothing is started until the old server has gone.
body=$(sed -n '/^  restart)/,/^   ;;/p' "$S95")
wait_at=$(line_of '^ *wait_for_stop$' "$body")
start_at=$(line_of '^ *cp -r /kvmapp' "$body")

if [ -n "$wait_at" ] && [ -n "$start_at" ] && [ "$wait_at" -lt "$start_at" ]; then
    note "restart) stages the new server only after the old one is gone" OK
else
    note "restart) stages the new server while the old one may still run" FAIL
fi

echo
if [ "$fails" -eq 0 ]; then
    echo "all cases passed"
else
    echo "$fails case(s) FAILED"
fi
exit "$fails"
