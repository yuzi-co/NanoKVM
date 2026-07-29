SLOTA=/dev/mmcblk0p2

# The console trace from set -x goes to the serial port, which is not reachable
# on a deployed board. These land in dmesg instead, so a failed slot can be
# diagnosed over the network from the system that fell back.
slotlog() {
	echo "nanokvm-slot: $*" > /dev/kmsg
}

# One mount attempt, with the single e2fsck retry this script has always done.
mount_fs() {
	mount -o rw "$1" /realroot && return 0
	slotlog "mount $1 failed rc=$?, running e2fsck"
	e2fsck -fvp "$1"
	slotlog "e2fsck $1 rc=$?"
	mount -o rw "$1" /realroot
}

# Slot B is an image on the slot A filesystem, so slot A is mounted first to
# reach it and then moved into the new root. The loop device holds the backing
# file open; leaving that mount in the initramfs would strand slot A unmounted
# at shutdown and dirty it on every boot.
mount_image() {
	mkdir -p /slota

	mount -o rw "${SLOTA}" /slota
	rc="$?"
	slotlog "step mount-slota rc=${rc}"
	[ "${rc}" -eq 0 ] || return 1

	if [ ! -f "/slota/$1" ]
	then
		slotlog "step image-missing /slota/$1"
		umount /slota
		return 1
	fi
	slotlog "step image-found /slota/$1"

	# PATH is "/" and the initramfs only symlinks a dozen applets; losetup is
	# not one of them, so it has to be reached through busybox by name.
	/busybox losetup /dev/loop0 "/slota/$1"
	rc="$?"
	slotlog "step losetup rc=${rc}"
	if [ "${rc}" -ne 0 ]
	then
		umount /slota
		return 1
	fi

	mount_fs /dev/loop0
	rc="$?"
	slotlog "step mount-loop rc=${rc}"
	if [ "${rc}" -ne 0 ]
	then
		/busybox losetup -d /dev/loop0
		umount /slota
		return 1
	fi

	mkdir -p /realroot/mnt/slota
	mount -o move /slota /realroot/mnt/slota
	slotlog "step move rc=$?"
	return 0
}

bootdev=
case "${slotspec}" in
	a)
		mount_fs "${SLOTA}" && bootdev="${SLOTA}"
		;;
	loop:*)
		mount_image "${slotspec#loop:}" && bootdev="${slotspec}"
		;;
	*)
		if [ -b "${slotspec}" ]
		then
			mount_fs "${slotspec}" && bootdev="${slotspec}"
		fi
		;;
esac

# Anything that is not plain slot A gets one fallback to slot A before giving up.
if [ -z "${bootdev}" ] && [ "${slotspec}" != "a" ]
then
	slotlog "${slotspec} failed, falling back to ${SLOTA}"
	umount /realroot 2>/dev/null
	/busybox losetup -d /dev/loop0 2>/dev/null
	umount /slota 2>/dev/null
	mount_fs "${SLOTA}" && bootdev="${SLOTA}"
fi

if [ -z "${bootdev}" ]
then
	msc
fi

echo "nanokvm-slot: booted ${bootdev}" > /dev/kmsg

