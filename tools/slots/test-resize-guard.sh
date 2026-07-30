#!/bin/sh
# Exercise the S01fs resize guard taken straight out of the script that ships,
# so the test cannot drift from it.
#
#   test-resize-guard.sh [path-to-S01fs]
#
# Not destructive: parted is stubbed and no device is touched.
FS=${1:-$(dirname "$0")/../../kvmapp/system/init.d/S01fs}
[ -f "$FS" ] || { echo "usage: test-resize-guard.sh <S01fs>"; exit 1; }

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

sed -n '/^# --- resize guard ---$/,/^# --- end resize guard ---$/p'         "$FS" > "$WORK/guard.sh"
sed -n '/^# --- partition probes ---$/,/^# --- end partition probes ---$/p' "$FS" > "$WORK/probes.sh"
[ -s "$WORK/guard.sh" ]  || { echo "could not extract the resize guard block"; exit 1; }
[ -s "$WORK/probes.sh" ] || { echo "could not extract the partition probes block"; exit 1; }

fails=0
note() { printf '  %-60s %s\n' "$1" "$2"; [ "$2" = FAIL ] && fails=$((fails + 1)); return 0; }

echo "===== when is the resize worth doing? ====="
# The stock script resizes on every boot. It is a no-op once p2 is already
# 8192MB, and it cannot succeed at all once p3 exists, because p3 starts
# immediately above p2. Either way it rewrites the partition table for nothing.
guard_case() {
    desc="$1"; p3="$2"; end="$3"; target="$4"; want="$5"
    got=$(P3="$p3" END="$end" TARGET="$target" WORK="$WORK" sh -c '
        part3_exists() { [ "$P3" = yes ]; }
        p2_end_mb()    { echo "$END"; }
        target_mb()    { echo "$TARGET"; }
        . "$WORK/guard.sh"
        needs_resize && echo resize || echo skip
    ')
    [ "$got" = "$want" ] && note "$desc -> $got" OK || note "$desc -> $got, want $want" FAIL
}

guard_case "p3 exists, p2 short: p2 cannot grow anyway" yes 4000 8192 skip
guard_case "p3 exists, p2 already full"                 yes 8192 8192 skip
guard_case "no p3, p2 already at the target"            no  8192 8192 skip
guard_case "no p3, p2 short: this is the one real case" no  4000 8192 resize
guard_case "no p3, small card already full"             no  7000 7000 skip
guard_case "no p3, small card still short"              no  3000 7000 resize

# If the table cannot be read the guard must not silently skip a resize that a
# fresh card needs. Behaving like the stock script is the safe direction.
guard_case "partition table unreadable: try, as the stock script does" no "" 8192 resize

echo
echo "===== reading the partition table ====="
# parted -m output for this board: line 2 is the disk, then one line per part.
BIG='BYT;
/dev/mmcblk0:31914MB:sd/mmc:512:512:msdos:SD SD32G:;
1:0.02MB:16.8MB:16.8MB:fat32::lba;
2:16.8MB:8192MB:8175MB:ext4::;
3:8193MB:31914MB:23721MB:::;'

SMALL='BYT;
/dev/mmcblk0:3965MB:sd/mmc:512:512:msdos:SD SD04G:;
1:0.02MB:16.8MB:16.8MB:fat32::lba;
2:16.8MB:3965MB:3948MB:ext4::;'

probe_case() {
    desc="$1"; table="$2"; fn="$3"; want="$4"
    got=$(TABLE="$table" WORK="$WORK" sh -c "
        parted() { echo \"\$TABLE\"; }
        . \"\$WORK/probes.sh\"
        $fn
    ")
    [ "$got" = "$want" ] && note "$desc -> $got" OK || note "$desc -> $got, want $want" FAIL
}

probe_case "32G card: p2 ends at 8192MB"        "$BIG"   p2_end_mb 8192
probe_case "32G card: target is 8192MB"         "$BIG"   target_mb 8192
probe_case "4G card: p2 ends at 3965MB"         "$SMALL" p2_end_mb 3965
probe_case "4G card: target is the whole card"  "$SMALL" target_mb 3965

echo
echo "===== the script still parses ====="
sh -n "$FS" && note "S01fs is valid shell" OK || note "S01fs does not parse" FAIL
grep -q 'mount -t vfat /dev/mmcblk0p1 /boot' "$FS" \
    && note "still mounts /boot" OK || note "/boot mount is gone" FAIL
grep -q 'mount /dev/mmcblk0p3 /data' "$FS" \
    && note "still mounts /data" OK || note "/data mount is gone" FAIL
grep -q 'kvm.disk0' "$FS" \
    && note "the mkfs guard is untouched" OK || note "the mkfs guard is gone" FAIL

echo
if [ "$fails" -eq 0 ]; then
    echo "===== all resize guard cases pass ====="
else
    echo "===== $fails case(s) failed ====="
    exit 1
fi
