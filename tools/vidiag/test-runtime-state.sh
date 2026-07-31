#!/bin/sh
# Check that S95nanokvm keeps the video pipeline's runtime state off the SD card.
#
#   test-runtime-state.sh [path-to-S95nanokvm]
#
# /kvmapp is the boot medium. Two files under /kvmapp/kvm are rewritten while
# the board runs, by processes rather than by a person:
#
#   now_fps  the measured frame rate, rewritten for as long as a stream runs
#   state    HDMI presence, rewritten by libkvm on every change, from inside
#            its frame read, with a shell, while it holds the capture mutex
#
# Neither value survives a reboot with any meaning, so both belong in tmpfs with
# a symlink left behind. The readers keep the path they already use, and nothing
# has to be rebuilt for it.
#
# This runs the shipped function against a fake root and checks the result, so
# it fails if the function stops doing what its comment claims.
S95=${1:-$(dirname "$0")/../../kvmapp/system/init.d/S95nanokvm}
[ -f "$S95" ] || { echo "usage: test-runtime-state.sh <S95nanokvm>"; exit 1; }

fails=0
note() { printf '  %-64s %s\n' "$1" "$2"; [ "$2" = FAIL ] && fails=$((fails + 1)); return 0; }

# The names the function is expected to handle. Adding one to the script
# without adding it here leaves it untested, which is the point of the list.
NAMES="now_fps state"

echo "===== every runtime file is redirected to tmpfs ====="

# Extract the two functions and run them against a temporary root, so the test
# exercises the shipped text rather than a copy of it.
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

sed -n '/^link_runtime_file() {/,/^}/p;/^prepare_runtime_state() {/,/^}/p' "$S95" > "$work/funcs.sh"

if [ ! -s "$work/funcs.sh" ]; then
    note "the script still defines prepare_runtime_state" FAIL
    echo
    echo "$fails case(s) FAILED"
    exit "$fails"
fi

mkdir -p "$work/tmp" "$work/kvmapp/kvm"

# Windows checkouts run this through MSYS, where "ln -s" copies the file instead
# of linking it. That is the harness, not the script, so say so rather than
# reporting a failure the device would never have. Run it under Linux - the
# device runs busybox, and so does the container - for a real answer:
#
#   docker run --rm -v "$PWD:/repo" -w /repo busybox sh tools/vidiag/test-runtime-state.sh
symlinks_work=yes
ln -s "$work/tmp" "$work/linkprobe" 2>/dev/null
[ -L "$work/linkprobe" ] || symlinks_work=no
rm -f "$work/linkprobe"

# A board that has been running the old layout has a regular file in place, and
# it has to be converted rather than left alone.
for name in $NAMES; do
    echo stale > "$work/kvmapp/kvm/$name"
done

sed "s|/tmp/kvm|$work/tmp/kvm|g; s|/kvmapp/kvm|$work/kvmapp/kvm|g" "$work/funcs.sh" > "$work/run.sh"
echo 'prepare_runtime_state' >> "$work/run.sh"

if ! sh "$work/run.sh" 2>"$work/err"; then
    note "prepare_runtime_state runs without error" FAIL
    sed 's/^/    /' "$work/err"
else
    note "prepare_runtime_state runs without error" OK
fi

for name in $NAMES; do
    if [ "$symlinks_work" = no ]; then
        note "$name is a symlink (this filesystem has no symlinks)" SKIP
    elif [ -L "$work/kvmapp/kvm/$name" ]; then
        note "$name is a symlink, not a file on the boot medium" OK
    else
        note "$name is still a regular file on the boot medium" FAIL
    fi

    if [ -e "$work/tmp/kvm/$name" ]; then
        note "$name has a target in tmpfs before anything reads it" OK
    else
        note "$name has no target, so a reader gets nothing" FAIL
    fi
done

echo
echo "===== running it twice changes nothing ====="
# start and restart both call this, and a restart must not break a working link.
before=$(ls -l "$work/kvmapp/kvm" | sort)
sh "$work/run.sh" 2>/dev/null
after=$(ls -l "$work/kvmapp/kvm" | sort)

[ "$before" = "$after" ] && note "a second run leaves the links alone" OK \
                         || note "a second run changed the links" FAIL

echo
echo "===== the value is preserved across a restart ====="
# The link target must not be reset by a restart: the server reads state to tell
# a sleeping target from an awake one, and a restart is not new information.
echo 1 > "$work/tmp/kvm/state"
sh "$work/run.sh" 2>/dev/null
[ "$(cat "$work/tmp/kvm/state")" = "1" ] \
    && note "an existing value survives prepare_runtime_state" OK \
    || note "prepare_runtime_state overwrote a live value" FAIL

echo
echo "===== the script still parses ====="
if sh -n "$S95" 2>/dev/null; then
    note "sh -n accepts the script" OK
else
    note "sh -n rejects the script" FAIL
fi

echo
if [ "$fails" -eq 0 ]; then
    echo "all cases passed"
else
    echo "$fails case(s) FAILED"
fi
exit "$fails"
