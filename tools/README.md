# Device tooling

Scripts for work that happens on or to a device, rather than inside one of the
four deliverables. Nothing here is part of a build or an install package; these
are operator tools.

| Path          | What it does                                                          |
| ------------- | --------------------------------------------------------------------- |
| `build/`      | App-only cross-compile toolchain: `NanoKVM-Server` without MaixCDK.    |
| `slots/`      | A/B root filesystems: patch the initramfs, build and install a slot.   |
| `slots/device/S00awatchdog` | Reverts the slot marker if a board under test never becomes reachable. |
| `zram/`       | Build `zram.ko`/`zsmalloc.ko` for the stock kernel, and enable them.   |

## Seven things that cost real time

**The initramfs `PATH` is `/`, and only twelve applets are symlinked.** busybox
inside the initramfs carries 402 applets, but `losetup` is not one of the names
that exists on disk. Calling it fails with `rc=127` even though the applet is
compiled in — "the applet is present" and "this script can invoke it" are
different questions. Reach anything outside the twelve as `/busybox <applet>`.
`slots/test-commands.sh` checks this and is worth running after any init edit.

**`mknod` fails on a bind-mounted host directory.** The initramfs contains
`dev/console` as a character device. Unpacking and repacking it on a mount from
a Windows or macOS host silently loses that node and produces an image that
will not boot. Do the unpack/repack on the container's own filesystem and copy
only the finished image out.

**`/boot` is a 16MB FAT partition with about 4.5MB free.** A second copy of
`boot.sd` does not fit, so an install cannot stage alongside the original.
`dd conv=notrunc` writes in place and extends the file if the new image is
longer, which means the partition never holds a truncated or absent boot image.
The constraint is free space for any growth, not that the image must not grow.

**A CRLF in a device script is the other `rc=127`.** `core.autocrlf=true` is the
default on a Windows checkout, so a shell script leaves the repository with LF
and arrives in the working tree with CRLF. busybox then reads the carriage
return as part of the command name, and every line fails. The script exits 127
for a reason that has nothing to do with a missing applet. At boot the only
symptom is a port that does not answer. `.gitattributes` pins `eol=lf` for the
paths that run on the device; check with `tr -cd '\r' < file | wc -c` before
believing a script that "should work".

**A slow link with clean error counters is the cable.** An `eth0` that needs
minutes to report carrier looks like a driver fault, and the kernel says
`Cannot get clk_500m_eth!` at every probe to encourage the idea. That message is
benign. With `rx_errors`, `rx_crc_errors` and `tx_errors` all at zero there is no
fault above the physical layer to find: a link that never trains produces no
frames to count. Replace the cable before instrumenting anything. Measured on
this board: 1266s to carrier before, 7s after.

**A green check is worth what its failure mode is worth.** Several checks here
pass trivially unless deliberately provoked: a symlink test that follows into a
dangling target, a scenario harness that stubs a shell function while the code
calls an absolute path. `slots/test-mutation.sh` exists because of this — it
breaks the init on purpose and fails if the checks do not notice.

**A boot that answers ping and nothing else needs the card out.** `/boot/slot`
selects the root filesystem and lives on a FAT partition, so changing it needs a
shell on the board. A slot that mounts and then starts no listener gives you
neither. `slots/device/S00awatchdog` reverts the marker if the board proves
unreachable, and an instrumented `rcS` records which script failed. Install both
in a slot before trying it, not after.

## Recovering a board that will not boot

The stock initramfs has a mass-storage recovery mode that predates any of this:

- `touch /boot/rec`, then reboot, or
- hold the User Key while powering on.

Either exposes the whole SD card to a USB host, so `/boot/slot` can be deleted
or `boot.sd` restored from another machine without opening anything. This only
works if `boot.sd` itself still loads; a corrupt one needs the card in a reader.
Keep a copy of the stock image before replacing it.
