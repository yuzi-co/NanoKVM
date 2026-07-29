# --- slot selection ---
# /boot/slot selects the root filesystem:
#   absent, empty or "a"  /dev/mmcblk0p2, which is what this script did before
#                         slots existed
#   a block device path   that device
#   loop:<path>           an image file at <path> on the slot A filesystem,
#                         mounted through a loop device
# Anything unrecognised, and any slot that will not mount, falls back to slot A,
# so a bad marker cannot strand the board.
# Read here because /boot is unmounted immediately below.
slotspec=a
if [ -e /boot/slot ]
then
	read slotspec < /boot/slot
fi
if [ -z "${slotspec}" ]
then
	slotspec=a
fi
echo "nanokvm-slot: marker=${slotspec}" > /dev/kmsg
# --- end slot selection ---

