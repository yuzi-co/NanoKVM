#!/bin/sh
# Check that S03usbdev can give the managed host a serial console, and that it
# does not do so unless it is asked.
#
#   test-acm-console.sh [path-to-S03usbdev]
#
# This board has no recovery path that does not need a person. There is no
# serial console on the SoC, the Cube case does not bring out the User Key, and
# the initramfs mass-storage mode needs a working shell - which is the thing
# that has failed. See "Recovering a board that will not boot" in tools/README.md.
#
# An ACM function closes most of that gap using hardware that is already
# connected. Everything else is in place already: the kernel has
# CONFIG_USB_F_ACM and CONFIG_USB_CONFIGFS_ACM, /etc/inittab already respawns a
# getty on ttyGS0, and /etc/ttyGS0_handler.sh already exists. The only missing
# piece is the gadget function, so /dev/ttyGS0 never appears and that getty
# respawns against nothing.
#
# It is off by default and on no board changes behaviour until someone creates
# /boot/usb.acm. That is deliberate: the console hands whoever controls the
# managed machine a login prompt on the KVM, which is a real trade and has to be
# a decision rather than a default.
#
# It also has to be added last. configfs numbers interfaces in the order the
# links are made, so a function inserted before the HID ones would renumber
# them under a host that is already using them.
S03=${1:-$(dirname "$0")/../../kvmapp/system/init.d/S03usbdev}
[ -f "$S03" ] || { echo "usage: test-acm-console.sh <S03usbdev>"; exit 1; }

fails=0
note() { printf '  %-64s %s\n' "$1" "$2"; [ "$2" = FAIL ] && fails=$((fails + 1)); return 0; }

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# run_start builds a fake configfs, runs the shipped start_usb_dev against it,
# and leaves the result in $work/sys/kernel/config/usb_gadget/g0.
# $1 is "on" to create the /boot/usb.acm flag first.
run_start() {
    rm -rf "$work/sys" "$work/boot" "$work/proc" "$work/etc"
    # configfs creates these three when g0 appears. A plain directory does not,
    # so make them here and let the script's own "mkdir g0" fail harmlessly -
    # the script sets no -e, exactly as it runs on the device.
    mkdir -p "$work/sys/kernel/config/usb_gadget/g0/strings" \
             "$work/sys/kernel/config/usb_gadget/g0/configs" \
             "$work/sys/kernel/config/usb_gadget/g0/functions" \
             "$work/sys/class/udc/4340000.usb" \
             "$work/sys/class/cvi-base" \
             "$work/boot" "$work/proc/cviusb" "$work/etc"
    : > "$work/etc/profile"
    echo "uid 0123456789ABCDEF_0123456789ABCDEF" > "$work/sys/class/cvi-base/base_uid"
    [ "$1" = on ] && : > "$work/boot/usb.acm"

    # configfs creates a directory's children as soon as the directory exists,
    # so "mkdir configs/c.1/strings/0x409" works there and not here. Make mkdir
    # behave the same rather than let a real failure hide in that noise.
    echo 'mkdir() { /bin/mkdir -p "$@"; }' > "$work/func.sh"
    sed -n '/^start_usb_dev()/,/^}/p' "$S03" \
        | sed "s|/sys/|$work/sys/|g; s|/boot/|$work/boot/|g; s|/proc/|$work/proc/|g; s|\. /etc/profile|:|" \
        >> "$work/func.sh"
    echo 'start_usb_dev' >> "$work/func.sh"
    sh "$work/func.sh" > "$work/out" 2>&1
    G=$work/sys/kernel/config/usb_gadget/g0
}

echo "===== off unless asked ====="

run_start off
if ls "$G/functions" 2>/dev/null | grep -q '^acm'; then
    note "no ACM function without /boot/usb.acm" FAIL
else
    note "no ACM function without /boot/usb.acm" OK
fi
if ls "$G/configs/c.1" 2>/dev/null | grep -q '^acm'; then
    note "no ACM in the config without /boot/usb.acm" FAIL
else
    note "no ACM in the config without /boot/usb.acm" OK
fi
# The rest of the gadget must be untouched by the change.
for f in hid.GS0 hid.GS1 hid.GS2; do
    [ -e "$G/functions/$f" ] && note "$f still built" OK || note "$f is missing" FAIL
done

echo
echo "===== on when asked ====="

run_start on
if [ -d "$G/functions/acm.GS0" ]; then
    note "the ACM function is created" OK
else
    note "the ACM function is missing, so /dev/ttyGS0 never appears" FAIL
    sed 's/^/    /' "$work/out" | tail -5
fi
if [ -L "$G/configs/c.1/acm.GS0" ] || [ -e "$G/configs/c.1/acm.GS0" ]; then
    note "the ACM function is linked into the config" OK
else
    note "the ACM function is not in the config, so the host never sees it" FAIL
fi
for f in hid.GS0 hid.GS1 hid.GS2; do
    [ -e "$G/functions/$f" ] && note "$f still built with ACM on" OK || note "$f is missing" FAIL
done

echo
echo "===== added last, so no interface is renumbered ====="

# A structural check on the shipped text. configfs assigns interface numbers in
# link order, so an ACM link made before the HID links would move the keyboard
# and the mice under a host that is already bound to them.
body=$(sed -n '/^start_usb_dev()/,/^}/p' "$S03")
line_of() { printf '%s\n' "$body" | grep -n -- "$1" | tail -1 | cut -d: -f1; }

acm_at=$(line_of '^ *ln -s functions/acm')
hid_at=$(line_of '^ *ln -s functions/hid\.GS2')
udc_at=$(line_of 'class/udc/.*> *UDC')

if [ -z "$acm_at" ]; then
    note "start_usb_dev links an ACM function" FAIL
else
    note "start_usb_dev links an ACM function" OK
    if [ -n "$hid_at" ] && [ "$acm_at" -gt "$hid_at" ]; then
        note "ACM is linked after the HID functions" OK
    else
        note "ACM is linked before the HID functions, renumbering them" FAIL
    fi
    if [ -n "$udc_at" ] && [ "$acm_at" -lt "$udc_at" ]; then
        note "ACM is linked before the gadget binds to the UDC" OK
    else
        note "ACM is linked after the bind, so it never takes effect" FAIL
    fi
fi

echo
if [ "$fails" -eq 0 ]; then
    echo "all cases passed"
else
    echo "$fails case(s) FAILED"
fi
exit "$fails"
