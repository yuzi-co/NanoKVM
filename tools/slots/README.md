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

## Replacing a slot while running from it

A live slot cannot be rebuilt in place: the loop device holds its backing file
open. Build a second image alongside it instead, and switch the marker. The old
image stays as a rollback that is one marker edit away, ahead of slot A.

```shell
# From the running loop slot. The image path is on the host filesystem;
# the marker is relative to the slot A root, which the script prints for you.
make-slot-image.sh /mnt/slota/slotb-<date>.img

# Record the slot you are leaving. The watchdog reads this file, and the slot
# you are on now is the one that is known to work.
cp /boot/slot /boot/slot.good
cp /boot/ver  /boot/ver.good

echo loop:/slotb-<date>.img > /boot/slot && reboot
```

Populating from a loop slot copies that slot, not slot A, so whatever the
running system has — zram, a newer server binary — carries over. `rsync -x`
stops at filesystem boundaries, and `/mnt/slota` is one, so slot A is not
swept in. Stage anything new into the image while it is still mounted rather
than into the running system: until the marker moves, a bad build has touched
nothing that is currently booting.

## Watchdog

The fallback in `/init` only catches a slot that will not mount. A slot that
mounts and then starts no listener is worse: the board answers ping, and the
marker that selects it is on a FAT partition that needs a shell to change. The
card must come out.

`device/S00awatchdog` removes that failure mode. Install it in the slot you are
trying, not in the slot you trust:

```shell
cp tools/slots/device/S00awatchdog /etc/init.d/S00awatchdog   # inside the image
chmod 755 /etc/init.d/S00awatchdog
```

The name sorts before `S00kmod`, so the watchdog arms before any other script
can fail. It then waits up to 300 seconds for one question to become true: can
this board be reached? Either door counts.

| probe    | how                                           |
| -------- | --------------------------------------------- |
| network  | `eth0` has carrier and an IPv4 address         |
| web      | `curl` gets 2xx, 3xx or 401 from loopback      |
| ssh      | `pidof sshd`                                   |

The rule is `network AND (web OR ssh)`. One door is enough, because one door is
a way back in. A KVM that answers ssh but serves nothing is broken, and a person
can repair it. Only the loss of both doors needs a reboot.

Probe a listener, not a process. `pidof sshd` alone accepts a board whose server
died, which is one of the two faults this script was written after.

If no door opens, the watchdog restores `/boot/slot.good` and reboots. It never
writes that file. Recording the *running* slot as the fallback sounds helpful and
is not: after one healthy boot the fallback names the slot itself, and every
later failure then lands on slot A however many good slots exist. The switch
records the file, because the slot it replaces is the slot that was working.

Two cases fall through to slot A, and both matter:

- No `/boot/slot.good` exists. Nothing better is known.
- `/boot/slot.good` names the slot that is failing now. A slot can boot, and
  break later. To restore it onto itself would reboot the board for ever.

Decisions go to `/watchdog.log` in the image, because `/var/log` is a symlink
into tmpfs and a boot that never finishes leaves nothing there.

Ask for the verdict by hand at any time:

```shell
/etc/init.d/S00awatchdog check          # reachable (web=up, ssh=up)
```

`test-watchdog.sh` extracts the decision logic from the script itself, so the
test cannot drift from what ships. Run it on the board as well as on a
workstation: busybox `ash` is not the shell the tests were written in.

```shell
sh tools/slots/test-watchdog.sh                       # against the repository copy
sh /tmp/test-watchdog.sh /etc/init.d/S00awatchdog     # against what is installed
```

Remove the script once a slot is trusted, or leave it: a slot that is reachable
pays only one `curl` to loopback, ten seconds after boot.

## Instrumenting a boot that fails silently

`rcS` reports nothing, and every service writes its log to tmpfs, so a boot that
completes with no listeners leaves no evidence. Replace `rcS` in the image with a
copy that records each script and its exit status to `/bootlog`:

```sh
echo "$(cut -d. -f1 /proc/uptime)s  start  $i" >> /bootlog; sync
... run the script as before ...
echo "$(cut -d. -f1 /proc/uptime)s  done   $i (rc=$?)" >> /bootlog; sync
```

The `sync` matters. Without it the log is lost with the page cache when the
board is power-cycled, which is how a stalled board is usually ended.

This is what turned a silent failure into two named faults: `S03usbdev` and
`S95nanokvm` both exited 127, and `S50sshd` exited 0 having started nothing.

## Fresh upstream rootfs: two traps

A slot built from a released image is not the same as a slot copied from a
running board. Two differences bite.

**Copying identity over a factory rootfs never deletes.** `cp -a /etc/kvm/.` adds
files and overwrites files. A file that the factory has and the running board
does not will survive. Release 1.4.3 ships `/etc/kvm/ssh_stop`, which tells
`S50sshd` to start nothing and exit 0. The result is a board with no ssh, and a
script that reported success. Compare the two trees for files that exist only in
the new one, and decide about each:

```shell
( cd /mnt/new && find etc -type f | sort ) > /tmp/new
( cd /        && find etc -type f | sort ) > /tmp/run
awk 'NR==FNR{a[$0];next} !($0 in a)' /tmp/run /tmp/new
```

**A first boot may reformat the SD card.** `S01fs` runs `mkfs.exfat` on
`/dev/mmcblk0p3` when `/boot/usb.disk0` exists and `/etc/kvm.disk0` does not. A
factory rootfs has no `/etc/kvm.disk0`. `/data` is `p3`. Create the marker in the
image before the first boot:

```shell
touch /mnt/new/etc/kvm.disk0
```

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
