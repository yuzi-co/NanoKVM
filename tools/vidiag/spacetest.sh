#!/bin/sh
# Replay the restart case on a tmpfs of the device's size, and measure whether
# the server binary survives it.
#
#   docker run --rm --tmpfs /t:rw,size=80892k -v "$PWD/tools/vidiag:/s:ro" \
#       busybox sh /s/spacetest.sh /t
#
# The device holds /tmp on a tmpfs of 80892K, and /kvmapp/server is 36236K. The
# server writes its standard output to /tmp/nanokvm-server.log, and libkvm
# prints on some error paths once per frame, so a capture pipeline that fails in
# a loop fills the free space.
#
# This script fills it, then runs the two possible orders of the restart case:
# the old one, which empties the log after the copies, and the new one, which
# empties it before them. It reports the size of the copied binary each time.
#
# Nothing here touches a device. Everything happens in the tmpfs given as $1.
T=${1:?usage: spacetest.sh <tmpfs mountpoint>}
SRC=/src

# The device's own numbers, in KB, measured with du -sk on 2026-07-31.
SERVER_KB=36236
SYSTEM_KB=328
OTHER_KB=1900

blob() { mkdir -p "$1"; dd if=/dev/zero of="$1/blob" bs=1024 count="$2" 2>/dev/null; }

echo "=== building the source tree outside the tmpfs ==="
rm -rf "$SRC"; mkdir -p "$SRC"
blob "$SRC/server"     "$SERVER_KB"
blob "$SRC/kvm_system" "$SYSTEM_KB"
du -sk "$SRC/server" "$SRC/kvm_system"

# Put the tmpfs into the state the device is in while the server runs, then let
# the log take every byte that is left.
arrange() {
    rm -rf "$T"/* 2>/dev/null
    cp -r "$SRC/server"     "$T/"
    cp -r "$SRC/kvm_system" "$T/"
    blob "$T/other" "$OTHER_KB"
    dd if=/dev/zero of="$T/nanokvm-server.log" bs=1024 2>/dev/null
    echo "  arranged: $(df -k "$T" | tail -1)"
    echo "  log: $(du -sk "$T/nanokvm-server.log" | cut -f1)K"
}

# Report what the restart case would start. A copy that runs out of space
# leaves a short file, and the case starts it without looking.
verdict() {
    got=$(du -sk "$T/server/blob" 2>/dev/null | cut -f1)
    [ -n "$got" ] || got=0
    if [ "$got" = "$SERVER_KB" ]; then
        echo "  RESULT: binary is $got""K of $SERVER_KB""K - the KVM starts"
    else
        echo "  RESULT: binary is $got""K of $SERVER_KB""K - the KVM is down"
    fi
}

echo
echo "=== old order: empty the log after the copies ==="
arrange
rm -r "$T/kvm_system" "$T/server"
cp -r "$SRC/kvm_system" "$T/"
cp -r "$SRC/server" "$T/" 2>&1 | sed 's/^/  cp: /'
: > "$T/nanokvm-server.log"
verdict

echo
echo "=== old order, while syslogd writes ==="
# The old order leaves no margin at all: removing /tmp/server returns exactly
# what copying it back needs. Nothing on the device holds still for that.
# /var/log is a symlink to /tmp, so busybox syslogd writes there for the whole
# restart, and it is being flooded by the same driver errors that flood the
# server's log. 256K is about one second of that.
arrange
rm -r "$T/kvm_system" "$T/server"
cp -r "$SRC/kvm_system" "$T/"
dd if=/dev/zero of="$T/messages" bs=1024 count=256 2>/dev/null
cp -r "$SRC/server" "$T/" 2>&1 | sed 's/^/  cp: /'
: > "$T/nanokvm-server.log"
verdict

echo
echo "=== new order: empty the log before the copies ==="
arrange
: > "$T/nanokvm-server.log"
rm -r "$T/kvm_system" "$T/server"
cp -r "$SRC/kvm_system" "$T/"
cp -r "$SRC/server" "$T/" 2>&1 | sed 's/^/  cp: /'
verdict
