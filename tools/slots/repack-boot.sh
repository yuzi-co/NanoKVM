#!/bin/bash
# Build a slot-aware boot.sd from a stock one, and verify it before it is
# allowed anywhere near a device.
#
#   repack-boot.sh <stock-boot.sd> [output-dir]
#
# Needs: u-boot-tools, cpio, device-tree-compiler, and the ability to mknod.
# The work directory must be on the container's own filesystem, not a bind
# mount from a Windows or macOS host: the initramfs contains dev/console, and
# mknod is refused on those mounts, which would silently drop the node and
# produce an image that cannot boot.
set -e

HERE=$(cd "$(dirname "$0")" && pwd)
ORIG=${1:?usage: repack-boot.sh <stock-boot.sd> [output-dir]}
OUT=${2:-/tmp/slotbuild}

[ -f "$ORIG" ] || { echo "no such file: $ORIG"; exit 1; }
rm -rf "$OUT"; mkdir -p "$OUT"

echo "############ 1. extract original sub-images"
dumpimage -T flat_dt -p 0 -o "$OUT/kernel.orig"  "$ORIG" >/dev/null
dumpimage -T flat_dt -p 1 -o "$OUT/ramdisk.orig" "$ORIG" >/dev/null
dumpimage -T flat_dt -p 2 -o "$OUT/fdt.orig"     "$ORIG" >/dev/null
for f in kernel ramdisk fdt; do
    printf '  %-8s %10s bytes  sha256 %s\n' "$f" "$(stat -c %s "$OUT/$f.orig")" \
        "$(sha256sum "$OUT/$f.orig" | cut -c1-16)"
done

echo
echo "############ 2. unpack the initramfs"
mkdir -p "$OUT/tree.orig"
( cd "$OUT/tree.orig" && gzip -dc "$OUT/ramdisk.orig" | cpio -idm --quiet )
echo "  device nodes preserved:"
find "$OUT/tree.orig" \( -type c -o -type b \) -exec ls -l {} \;
if [ -z "$(find "$OUT/tree.orig" -type c)" ]; then
    echo "  NO CHARACTER DEVICES FOUND - mknod was refused, this image will not boot"
    exit 1
fi

echo
echo "############ 3. patch /init"
cp -a "$OUT/tree.orig" "$OUT/tree.new"
INIT="$OUT/tree.new/init"

L_UMOUNT=$(grep -n '^umount /dev/mmcblk0p1$'                "$INIT" | head -1 | cut -d: -f1)
L_MOUNT=$( grep -n '^mount -o rw /dev/mmcblk0p2 /realroot$' "$INIT" | head -1 | cut -d: -f1)
L_PROC=$(  grep -n '^mount -t proc proc /realroot/proc$'    "$INIT" | head -1 | cut -d: -f1)
echo "  anchors: umount=$L_UMOUNT mount=$L_MOUNT proc=$L_PROC"
[ -n "$L_UMOUNT" ] && [ -n "$L_MOUNT" ] && [ -n "$L_PROC" ] || { echo "ANCHOR NOT FOUND"; exit 1; }
[ "$L_UMOUNT" -lt "$L_MOUNT" ] && [ "$L_MOUNT" -lt "$L_PROC" ] || { echo "ANCHORS OUT OF ORDER"; exit 1; }

{
    sed -n "1,$((L_UMOUNT - 1))p"          "$INIT"
    cat "$HERE/init-slot-selection.inc"
    sed -n "${L_UMOUNT},$((L_MOUNT - 1))p" "$INIT"
    cat "$HERE/init-mount-dispatch.inc"
    sed -n "${L_PROC},\$p"                 "$INIT"
} > "$OUT/init.new"
mv "$OUT/init.new" "$INIT"
chmod 775 "$INIT"
touch -r "$OUT/tree.orig/init" "$INIT"

echo "  ---------- diff ----------"
diff -u "$OUT/tree.orig/init" "$INIT" || true
echo "  ---------- end ----------"
sh -n "$INIT" && echo "  sh -n: OK"

echo
echo "############ 4. repack initramfs"
( cd "$OUT/tree.new" && find . | LC_ALL=C sort | cpio -o -H newc --quiet ) > "$OUT/ramdisk.new.cpio"
gzip -9 -n -c "$OUT/ramdisk.new.cpio" > "$OUT/ramdisk.new"
printf '  ramdisk old %s -> new %s bytes\n' \
    "$(stat -c %s "$OUT/ramdisk.orig")" "$(stat -c %s "$OUT/ramdisk.new")"

echo
echo "############ 5. build the FIT"
sed -e "s#@KERNEL@#$OUT/kernel.orig#" -e "s#@RAMDISK@#$OUT/ramdisk.new#" \
    -e "s#@FDT@#$OUT/fdt.orig#" "$HERE/boot.its.in" > "$OUT/boot.its"
mkimage -f "$OUT/boot.its" "$OUT/boot.sd.new" >/dev/null
echo "  built: $(stat -c %s "$OUT/boot.sd.new") bytes (original $(stat -c %s "$ORIG"))"

echo
echo "############ 6. VERIFY"
fail=0
note() { printf '  %-58s %s\n' "$1" "$2"; [ "$2" = "FAIL" ] && fail=1; return 0; }

dumpimage -T flat_dt -p 0 -o "$OUT/kernel.chk"  "$OUT/boot.sd.new" >/dev/null
dumpimage -T flat_dt -p 1 -o "$OUT/ramdisk.chk" "$OUT/boot.sd.new" >/dev/null
dumpimage -T flat_dt -p 2 -o "$OUT/fdt.chk"     "$OUT/boot.sd.new" >/dev/null

cmp -s "$OUT/kernel.orig"  "$OUT/kernel.chk"  && note "kernel byte-identical to original" OK      || note "kernel byte-identical to original" FAIL
cmp -s "$OUT/fdt.orig"     "$OUT/fdt.chk"     && note "device tree byte-identical to original" OK || note "device tree byte-identical to original" FAIL
cmp -s "$OUT/ramdisk.new"  "$OUT/ramdisk.chk" && note "ramdisk survives the FIT round-trip" OK    || note "ramdisk survives the FIT round-trip" FAIL

mkdir -p "$OUT/tree.chk"
( cd "$OUT/tree.chk" && gzip -dc "$OUT/ramdisk.chk" | cpio -idm --quiet )

inventory() { ( cd "$1" && find . -printf '%y %m %8s %p -> %l\n' | LC_ALL=C sort ); }
hashes()    { ( cd "$1" && find . -type f -printf '%p\n' | LC_ALL=C sort | xargs sha256sum ); }
inventory "$OUT/tree.orig" > "$OUT/inv.orig"; inventory "$OUT/tree.chk" > "$OUT/inv.chk"
hashes    "$OUT/tree.orig" > "$OUT/sha.orig"; hashes    "$OUT/tree.chk" > "$OUT/sha.chk"

# Trimmed explicitly: tr leaves a trailing space, and relying on word splitting
# to remove it makes the comparison depend on the value being unquoted.
sha_changed=$(diff "$OUT/sha.orig" "$OUT/sha.chk" | grep -E '^[<>]' | awk '{print $3}' | sort -u | tr '\n' ' ' | sed 's/ *$//')
[ "$sha_changed" = "./init" ] \
    && note "only ./init content differs (sha256 over every file)" OK \
    || note "content changed in: ${sha_changed:-nothing}" FAIL

inv_total=$(diff "$OUT/inv.orig" "$OUT/inv.chk" | grep -cE '^[<>]')
inv_init=$(diff  "$OUT/inv.orig" "$OUT/inv.chk" | grep -cE '^[<>].*[^a-z]init')
[ "$inv_total" -eq "$inv_init" ] \
    && note "inventory differs only on ./init (modes, links, nodes intact)" OK \
    || note "inventory differs beyond ./init ($inv_total lines)" FAIL

if [ -c "$OUT/tree.chk/dev/console" ] \
   && [ "$(stat -c %t "$OUT/tree.chk/dev/console")" = "5" ] \
   && [ "$(stat -c %T "$OUT/tree.chk/dev/console")" = "1" ]; then
    note "dev/console present as char 5,1" OK
else
    note "dev/console missing or wrong device numbers" FAIL
fi

links=$(find "$OUT/tree.chk" -maxdepth 1 -type l | wc -l)
[ "$links" -eq 12 ] && note "all 12 busybox symlinks intact" OK || note "symlink count is $links, expected 12" FAIL

echo "  --- slot dispatch scenarios ---"
if SLOTTEST_SANDBOX=1 sh "$HERE/test-dispatch.sh" "$OUT/tree.chk/init"; then
    note "slot dispatch behaves correctly in every scenario" OK
else
    note "slot dispatch scenarios failed" FAIL
fi

if sh "$HERE/test-commands.sh" "$OUT/tree.chk/init" "$OUT/tree.chk"; then
    note "every command in init resolves with PATH=/" OK
else
    note "init calls something unreachable in the initramfs" FAIL
fi

fitstruct() { fdtdump "$1" 2>/dev/null | grep -v -E '^\s+data = |timestamp|value = |^// '; }
fitstruct "$ORIG" > "$OUT/struct.orig"; fitstruct "$OUT/boot.sd.new" > "$OUT/struct.new"
if diff -q "$OUT/struct.orig" "$OUT/struct.new" >/dev/null; then
    note "FIT structure identical (ignoring data/hashes/timestamp)" OK
else
    note "FIT structure differs" FAIL
    diff -u "$OUT/struct.orig" "$OUT/struct.new" | head -40
fi

# The installer writes with dd conv=notrunc, which extends rather than
# truncates, so /boot always holds a boot image. What matters is that any
# growth fits the free space on a 16MB FAT partition; the installer checks the
# real figure on the device.
delta=$(( $(stat -c %s "$OUT/boot.sd.new") - $(stat -c %s "$ORIG") ))
[ "$delta" -le 1048576 ] && note "size delta ${delta} bytes, installer confirms free space" OK \
                         || note "image grew by ${delta} bytes, too much for a 16MB /boot" FAIL

echo
if [ "$fail" -eq 0 ]; then
    echo "  ===== ALL CHECKS PASSED ====="
    echo "  image: $OUT/boot.sd.new"
    echo "  sha256: $(sha256sum "$OUT/boot.sd.new" | cut -d' ' -f1)"
else
    echo "  ===== VERIFICATION FAILED - DO NOT INSTALL ====="
    exit 1
fi
