#!/bin/sh
# Build a slot B root filesystem as an image on the slot A filesystem.
# Runs ON the NanoKVM.
#
#   make-slot-image.sh [image-path] [size-MB]
#
# The image is sparse: nominally the requested size, occupying only what it
# holds. It lives on p2 rather than /data because /data is exFAT, which has no
# journal and cannot host a swap file either.
#
# /swapfile is deliberately excluded. A swap file inside the image would route
# swap writeback through the loop device, which itself needs memory to make
# progress. Give the slot zram instead, or point it at a swap file on p2.
set -e

IMG=${1:-/slotb.img}
SIZE_MB=${2:-3072}
MNT=/mnt/slotb

echo "===== pre-flight ====="
avail_kb=$(df -k / | awk 'NR==2{print $4}')
echo "  free on p2    : $((avail_kb / 1024)) MB"
echo "  rootfs in use : $(df -k / | awk 'NR==2{print int($3/1024)}') MB"
[ "$avail_kb" -gt 2097152 ] || { echo "need at least 2G free on p2"; exit 1; }
[ -e "$IMG" ] && { echo "$IMG already exists - refusing to clobber"; exit 1; }
for c in rsync mkfs.ext4 losetup tune2fs; do
    command -v "$c" >/dev/null || { echo "missing: $c"; exit 1; }
done
echo "  checks passed"

echo
echo "===== create sparse image ====="
dd if=/dev/zero of="$IMG" bs=1M count=0 seek="$SIZE_MB"
ls -l "$IMG"

echo
echo "===== format ====="
mkfs.ext4 -F -L slotb -m 0 -O ^has_journal,^64bit "$IMG" 2>&1 | tail -3
tune2fs -O has_journal "$IMG" 2>&1 | tail -1

echo
echo "===== mount ====="
mkdir -p "$MNT"
LOOP=$(losetup -f)
losetup "$LOOP" "$IMG"
mount "$LOOP" "$MNT"
echo "  $LOOP -> $MNT"

echo
echo "===== populate ====="
# -x stops at filesystem boundaries, which already excludes /proc, /sys, /dev,
# /tmp, /run, /data and the loop mount itself. -A is omitted because this rsync
# build reports ACLs unsupported and refuses the flag outright.
rsync -aHXx --numeric-ids \
    --exclude="$IMG" \
    --exclude=/swapfile \
    --exclude=/lost+found \
    / "$MNT/" 2>&1 | tail -3

echo
echo "===== slot markers ====="
mkdir -p "$MNT/mnt/slota"
echo b > "$MNT/etc/slot"
[ -f /etc/slot ] || echo a > /etc/slot
echo "  this slot: $(cat /etc/slot)    new slot: $(cat "$MNT/etc/slot")"

echo
echo "===== sanity ====="
for f in /sbin/init /etc/inittab /etc/init.d /kvmapp/server/NanoKVM-Server /mnt/system/ko; do
    [ -e "$MNT$f" ] && echo "  present: $f" || echo "  MISSING: $f"
done
echo "  files here: $(find / -xdev -type f 2>/dev/null | wc -l)"
echo "  files there: $(find "$MNT" -xdev -type f 2>/dev/null | wc -l)"

echo
echo "===== unmount ====="
sync
umount "$MNT"
losetup -d "$LOOP"
echo "  image occupies $(du -sh "$IMG" | cut -f1), free on p2 now $(df -h / | awk 'NR==2{print $4}')"

echo
echo "  Boot it with:  echo loop:$IMG > /boot/slot && reboot"
echo "  Undo with:     rm /boot/slot && reboot"
