#!/bin/sh
# Install a boot image on the device. Runs ON the NanoKVM.
#
#   install-boot.sh <new-boot.sd> [expected-sha256]
#
# dd conv=notrunc writes in place and extends the file if the new image is
# longer, so /boot never holds a truncated or absent boot image. /boot is a
# 16MB FAT partition with roughly 4.5MB free, which is not enough to stage a
# second copy of an 11.5MB image, so writing in place is the only safe option.
set -e

NEW=${1:?usage: install-boot.sh <new-boot.sd> [expected-sha256]}
EXPECT=$2
BACKUP=/data/boot.sd.orig

[ -f "$NEW" ] || { echo "no such file: $NEW"; exit 1; }

NEWSZ=$(wc -c < "$NEW")
CURSZ=$(wc -c < /boot/boot.sd)
GROWTH=$((NEWSZ - CURSZ))
FREE_KB=$(df -k /boot | awk 'NR==2{print $4}')

echo "== pre-flight"
if [ -n "$EXPECT" ]; then
    got=$(sha256sum "$NEW" | cut -d' ' -f1)
    [ "$got" = "$EXPECT" ] || { echo "staged hash mismatch:"; echo "  got  $got"; echo "  want $EXPECT"; exit 1; }
    echo "  staged hash matches"
fi

# Keep a stock image to fall back to. Only ever written once, so a later run
# cannot overwrite the original with an already-modified one.
if [ ! -f "$BACKUP" ]; then
    cp /boot/boot.sd "$BACKUP"
    sync
    echo "  saved stock image to $BACKUP"
else
    echo "  stock backup already present: $BACKUP"
fi

echo "  current $CURSZ bytes, new $NEWSZ bytes (growth $GROWTH), free ${FREE_KB}KB"
[ "$GROWTH" -lt $((FREE_KB * 1024)) ] || { echo "growth exceeds free space on /boot"; exit 1; }

echo
echo "== write"
dd if="$NEW" of=/boot/boot.sd conv=notrunc bs=64k
sync

echo
echo "== verify installed bytes"
installed=$(head -c "$NEWSZ" /boot/boot.sd | sha256sum | cut -d' ' -f1)
expected=$(sha256sum "$NEW" | cut -d' ' -f1)
if [ "$installed" = "$expected" ]; then
    echo "  OK $installed"
else
    echo "  MISMATCH"
    echo "    on disk $installed"
    echo "    source  $expected"
    exit 1
fi

printf '  FIT header: '
head -c 8 /boot/boot.sd | od -An -tx1
printf '  totalsize should be %08x\n' "$NEWSZ"
echo
echo "  slot marker: $(cat /boot/slot 2>/dev/null || echo '(absent - will boot slot A)')"
