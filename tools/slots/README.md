# A/B root filesystems

The stock `/init` hardcodes `mount -o rw /dev/mmcblk0p2 /realroot` and ignores
`root=` from the kernel command line entirely. Because the initramfs picks the
root, switching slots needs no bootloader change at all: no u-boot, no
`fip.bin`, no second `boot.sd`, no writable u-boot environment. The whole
mechanism is a shell script and a marker file on a FAT partition.

## What the patch adds

`/boot/slot` selects the root filesystem:

| marker              | result                                              |
| ------------------- | --------------------------------------------------- |
| absent, empty, `a`  | `/dev/mmcblk0p2` — byte-for-byte the stock behaviour |
| a block device path | that device                                          |
| `loop:<path>`       | an image at `<path>` on the slot A filesystem        |

Anything unrecognised, and any slot that will not mount, falls back to slot A.
Each step logs to `/dev/kmsg`, so a board that fell back can be diagnosed over
the network with `dmesg | grep nanokvm-slot` — the `set -x` trace goes to the
serial console, which a deployed board does not have.

For a loop slot, slot A stays mounted (it backs the loop device) and is moved
into the new root at `/mnt/slota` so it unmounts cleanly at shutdown.

## Building and installing

```shell
# In a throwaway container with u-boot-tools, cpio, device-tree-compiler.
# The work directory must be on the container's own filesystem: repacking the
# initramfs needs mknod, which bind mounts from Windows and macOS refuse.
tools/slots/repack-boot.sh /path/to/stock/boot.sd /tmp/slotbuild
```

`repack-boot.sh` refuses to emit an image unless the kernel and device tree come
back byte-identical, only `/init` differs, `dev/console` and all twelve applet
symlinks survive, every command resolves under `PATH=/`, and the dispatch
behaves correctly across twelve scenarios.

Two builds from identical inputs do not produce identical bytes: `mkimage`
stamps the FIT with the current time. Compare the verification output, not
sha256 across builds. `SOURCE_DATE_EPOCH` would make it reproducible but has
not been booted on hardware here, so it is not set.

Then on the device:

```shell
tools/slots/device/install-boot.sh /data/boot.sd.new <sha256>
reboot                                    # must still come up on slot A
tools/slots/device/make-slot-image.sh     # build slot B
echo loop:/slotb.img > /boot/slot && reboot
```

Verify with `cat /etc/slot` and `dmesg | grep nanokvm-slot`. Booting is not
enough on its own: a stock initramfs would also boot, so check that the trace
lines are present — their absence means the patch did not take.

## Swap

A slot that boots from a loop image must not put its swap inside that image:
swap writeback would go through the loop device, which needs memory to make
progress. `make-slot-image.sh` excludes `/swapfile` for this reason, so a fresh
slot boots with no swap at all. Give it `tools/zram`, or `swapon` a file on p2
through `/mnt/slota`.

## Recovery

`rm /boot/slot && reboot` returns to slot A. If the board will not boot far
enough to run that, see the recovery section in `tools/README.md` — the stock
initramfs can expose the SD card as USB mass storage, which is enough to delete
the marker or restore `/data/boot.sd.orig`.

The fallback only catches a slot that will not *mount*. A slot that mounts and
then fails in userland will boot into a broken system, and recovering that needs
the marker removed by one of the routes above.
