# Device tooling

Scripts for work that happens on or to a device, rather than inside one of the
four deliverables. Nothing here is part of a build or an install package; these
are operator tools.

| Path          | What it does                                                          |
| ------------- | --------------------------------------------------------------------- |
| `build/`      | App-only cross-compile toolchain: `NanoKVM-Server` without MaixCDK.    |
| `slots/`      | A/B root filesystems: patch the initramfs, build and install a slot.   |
| `zram/`       | Build `zram.ko`/`zsmalloc.ko` for the stock kernel, and enable them.   |

## Four things that cost real time

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

**A green check is worth what its failure mode is worth.** Several checks here
pass trivially unless deliberately provoked: a symlink test that follows into a
dangling target, a scenario harness that stubs a shell function while the code
calls an absolute path. `slots/test-mutation.sh` exists because of this — it
breaks the init on purpose and fails if the checks do not notice.

## Recovering a board that will not boot

The stock initramfs has a mass-storage recovery mode that predates any of this:

- `touch /boot/rec`, then reboot, or
- hold the User Key while powering on.

Either exposes the whole SD card to a USB host, so `/boot/slot` can be deleted
or `boot.sd` restored from another machine without opening anything. This only
works if `boot.sd` itself still loads; a corrupt one needs the card in a reader.
Keep a copy of the stock image before replacing it.
