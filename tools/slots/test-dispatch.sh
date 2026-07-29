#!/bin/sh
# Exercise the slot logic taken straight out of a patched init, so the test
# cannot drift from what ships.
#
#   SLOTTEST_SANDBOX=1 test-dispatch.sh <init>
#
# DESTRUCTIVE: creates /boot, /slota, /realroot and /busybox at the root of the
# filesystem it runs on. Run it in a throwaway container, never on a real
# system, which is what the sandbox variable is there to make you think about.
#
# Stubs log to a file rather than a shell variable because /busybox is invoked
# by absolute path and therefore runs as a separate process, out of reach of
# shell function stubs. Modelling that matters: it is exactly the property that
# let a "command not found" reach a real boot.
INIT="$1"
[ -f "$INIT" ] || { echo "usage: SLOTTEST_SANDBOX=1 test-dispatch.sh <init>"; exit 1; }
[ "$SLOTTEST_SANDBOX" = "1" ] || { echo "refusing to run: set SLOTTEST_SANDBOX=1 in a throwaway container"; exit 1; }

sed -n '/^# --- slot selection ---$/,/^# --- end slot selection ---$/p' "$INIT" > /tmp/blk_slot.sh
sed -n '/^SLOTA=\/dev\/mmcblk0p2$/,/^echo "nanokvm-slot: booted /p'          "$INIT" > /tmp/blk_mount.sh
[ -s /tmp/blk_slot.sh ]  || { echo "could not extract slot block";  exit 1; }
[ -s /tmp/blk_mount.sh ] || { echo "could not extract mount block"; exit 1; }

mkdir -p /boot /slota /realroot
rm -f /tmp/fakeblk; mknod /tmp/fakeblk b 179 4

cat > /busybox <<'BB'
#!/bin/sh
echo "busybox($*)" >> "$CALLLOG"
case "$1" in
losetup)
	shift
	[ "$1" = "-d" ] && exit 0
	for d in $MOUNT_FAIL; do [ "$d" = "losetup" ] && exit 1; done
	exit 0
	;;
esac
exit 0
BB
chmod +x /busybox

fails=0
note() { printf '  %-56s %s\n' "$1" "$2"; [ "$2" = "FAIL" ] && fails=$((fails + 1)); return 0; }

# $1 marker (NONE for absent)  $2 image present (y/n)  $3 what fails
# $4 expected bootdev          $5 substring required in the call log
scenario() {
    marker="$1"; img="$2"; failfor="$3"; want="$4"; wantcall="$5"

    rm -f /boot/slot; [ "$marker" = "NONE" ] || printf '%s\n' "$marker" > /boot/slot
    rm -f /slota/slotb.img; [ "$img" = "y" ] && : > /slota/slotb.img
    CALLLOG=/tmp/calls; : > "$CALLLOG"

    got=$(MOUNT_FAIL="$failfor" CALLLOG="$CALLLOG" sh -c '
        mount()   { echo "mount($3)" >> "$CALLLOG"
                    for d in $MOUNT_FAIL; do [ "$3" = "$d" ] && return 1; done
                    [ "$2" = "move" ] && echo "move" >> "$CALLLOG"; return 0; }
        umount()  { echo "umount($1)" >> "$CALLLOG"; return 0; }
        e2fsck()  { echo "e2fsck($2)" >> "$CALLLOG"; return 0; }
        msc()     { echo "MSC" >> "$CALLLOG"; }
        slotlog() { return 0; }
        . /tmp/blk_slot.sh  >/dev/null 2>&1
        . /tmp/blk_mount.sh >/dev/null 2>&1
        echo "${bootdev}"
    ' 2>/dev/null | tail -1)

    calls=$(tr '\n' ' ' < "$CALLLOG")

    if [ "$got" != "$want" ]; then
        note "marker=$marker img=$img -> '$got' (want '$want')" FAIL
        echo "        calls: $calls"
        return
    fi
    case " $calls " in
        *"$wantcall"*) note "marker=$marker img=$img -> ${got:-<none>}" OK ;;
        *) note "marker=$marker -> $got but call log lacks '$wantcall'" FAIL
           echo "        calls: $calls" ;;
    esac
}

echo "  --- the path that must stay identical to stock ---"
scenario NONE n ""  "/dev/mmcblk0p2" "mount(/dev/mmcblk0p2)"
scenario a    n ""  "/dev/mmcblk0p2" "mount(/dev/mmcblk0p2)"

echo "  --- stock fallback when the rootfs will not mount ---"
scenario NONE n "/dev/mmcblk0p2" "" "MSC"

echo "  --- loop slot ---"
scenario "loop:/slotb.img" y ""           "loop:/slotb.img" "move"
scenario "loop:/slotb.img" y ""           "loop:/slotb.img" "busybox(losetup /dev/loop0"
scenario "loop:/slotb.img" n ""           "/dev/mmcblk0p2"  "umount(/slota)"
scenario "loop:/slotb.img" y "losetup"    "/dev/mmcblk0p2"  "umount(/slota)"
scenario "loop:/slotb.img" y "/dev/loop0" "/dev/mmcblk0p2"  "busybox(losetup -d"

echo "  --- explicit block device slot ---"
scenario "/tmp/fakeblk" n ""              "/tmp/fakeblk"   "mount(/tmp/fakeblk)"
scenario "/tmp/fakeblk" n "/tmp/fakeblk"  "/dev/mmcblk0p2" "umount(/realroot)"

echo "  --- garbage markers must not select anything ---"
scenario "garbage"   n "" "/dev/mmcblk0p2" "mount(/dev/mmcblk0p2)"
scenario "/dev/nope" n "" "/dev/mmcblk0p2" "mount(/dev/mmcblk0p2)"

echo
[ "$fails" -eq 0 ] && echo "  slot logic: ALL SCENARIOS PASSED" \
                   || { echo "  slot logic: $fails FAILED"; exit 1; }
