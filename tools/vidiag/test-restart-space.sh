#!/bin/sh
# Check that S95nanokvm frees the server's log before it needs the space.
#
#   test-restart-space.sh [path-to-S95nanokvm]
#
# The server writes its standard output to /tmp/nanokvm-server.log, and libkvm
# prints on some error paths once per frame. A pipeline that fails in a loop
# therefore writes into tmpfs without a limit. S99vidiag empties the file while
# it reads, but that trim stops when the reader stops, so no other script can
# depend on it.
#
# tmpfs holds 80892K on the device and /kvmapp/server is 36236K. A flood fills
# the free space. The restart case then removes /tmp/server and /tmp/kvm_system,
# which returns 36540K, copies kvm_system back, which takes 328K, and copies the
# server, which needs 36236K. That leaves 24K less than the copy needs. The
# margin is not engineered: it is the difference between two unrelated sizes.
#
# A copy that runs out of space leaves a truncated NanoKVM-Server, and the case
# starts it anyway. The KVM is then down until somebody logs in.
#
# The fix is an order, not a size: empty the log first, and the flood's space is
# back before the first copy asks for it. This script checks that order.
#
# tools/vidiag/spacetest.sh replays the whole sequence on a real tmpfs of the
# device's size. Run that by hand when the numbers change.
S95=${1:-$(dirname "$0")/../../kvmapp/system/init.d/S95nanokvm}
[ -f "$S95" ] || { echo "usage: test-restart-space.sh <S95nanokvm>"; exit 1; }

fails=0
note() { printf '  %-64s %s\n' "$1" "$2"; [ "$2" = FAIL ] && fails=$((fails + 1)); return 0; }

# Report each case label with the line numbers of the two events that matter:
# emptying the log, and the first copy that consumes tmpfs. Reading the shipped
# script keeps the check from drifting away from it.
events=$(awk '
    /^  [a-z*]+\)/            { blk = $1; sub(/\)$/, "", blk) }
    /^[ \t]*: > "\$SERVER_LOG"/ { if (blk != "") print blk, "empty", NR }
    /^[ \t]*cp -r \/kvmapp\// { if (blk != "") print blk, "copy", NR }
' "$S95")

echo "===== the log is emptied before the first copy ====="
# Both cases start the server, and both copy 36MB into tmpfs first. An order
# that is right in one case and wrong in the other still takes the KVM down.
order_case() {
    blk="$1"
    empty=$(echo "$events" | awk -v b="$blk" '$1 == b && $2 == "empty" { print $3; exit }')
    copy=$(echo "$events"  | awk -v b="$blk" '$1 == b && $2 == "copy"  { print $3; exit }')

    if [ -z "$copy" ]; then
        note "$blk copies nothing into tmpfs" SKIP
        return 0
    fi
    if [ -z "$empty" ]; then
        note "$blk never empties the log" FAIL
        return 0
    fi
    [ "$empty" -lt "$copy" ] \
        && note "$blk empties at line $empty, copies at line $copy" OK \
        || note "$blk empties at line $empty, but copies at line $copy" FAIL
}

order_case start
order_case restart

echo
echo "===== the log is named once, and both cases use the name ====="
# A path written out twice drifts. The reader in S99vidiag follows one path, so
# a second spelling here means the collector reads a file nobody writes.
defs=$(grep -c '^SERVER_LOG=' "$S95")
[ "$defs" = 1 ] && note "SERVER_LOG is defined once" OK \
                || note "SERVER_LOG is defined $defs times" FAIL

lit=$(grep -c '/tmp/nanokvm-server\.log' "$S95")
[ "$lit" = 1 ] && note "the path appears only in that definition" OK \
               || note "the path is written out $lit times" FAIL

empties=$(echo "$events" | grep -c ' empty ')
[ "$empties" = 2 ] && note "both cases empty the log" OK \
                   || note "$empties case(s) empty the log, want 2" FAIL

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
