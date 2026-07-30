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
| `oled/`       | Move the status image to spread OLED wear, with no change to `kvm_system`. |
| `service/`    | Restart `NanoKVM-Server` and `kvm_system` if they die. Nothing else does. |
| `deploy/`     | Install a server build and put the old one back if it does not serve.   |

## Ten things that cost real time

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
paths that run on the device, and `test-line-endings.sh` fails if a device script
is either already carrying CR or not covered by the rule. Run it after adding any
script that reaches the device: pinning by directory missed an extensionless
script the same day the rule was written, and the check found it.

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

**A background loop in an init script holds your ssh session open.** `( ... ) &`
inherits the calling shell's stdout, so a child that never exits keeps the file
descriptor open and the ssh command never returns. Started by `init` at boot
this is invisible; started by hand to test it looks like a hung board. Redirect
the subshell: `( ... ) < /dev/null > /dev/null 2>&1 &`. `slots/device/S00awatchdog`
and `oled/S97oled-nudge` both do, and `build/README.md` records the same trap for
`S95nanokvm`, which needs `setsid` because it is the server being backgrounded.

**`./build <project>` compiles the SDK's components, not this repository's.**
`support/sg2002/additional/` holds patched copies of `kvm`, `kvm_mmf` and
`vision`, and `support/sg2002/build update_lib` is what copies them into
`~/MaixCDK/components`. Without that step an edit under `additional/` is simply
not compiled, and the build still reports success - `kvm_system` even prints
`Ignore component kvm_mmf` while doing it. Run `update_lib` first, then confirm
the change reached the compiler:

```shell
./build update_lib                      # note: it exits 1 on success
grep -c pthread ~/MaixCDK/components/kvm_mmf/src/kvm_mmf.cpp
./build kvm_vision                      # builds libkvm.so and libkvm_mmf.so
```

`kvm_vision` is the target that builds the capture library; the project lives in
`kvm_vision_test/`. `kvm_system` does not compile `kvm_mmf` at all.

**The committed `dl_lib` prebuilts are older than the committed sources.** A
build from the current tree produces a `libkvm_mmf.so` that exports one function
more than the shipped copy (`mmf_vi_frame_release`), and a `libkvm.so` 200KB
smaller. So replacing a prebuilt brings in every source change since it was
built, not only yours. Compare exported symbols before swapping one in:

```shell
readelf --dyn-syms -W lib.so | awk 'NR>3 && $5=="GLOBAL" && $7!="UND" {print $8}' | sort -u
```

Getting those `awk` columns wrong prints nothing and diffs clean, which looks
like proof of compatibility and is not. The columns are
`Num Value Size Type Bind Vis Ndx Name`.

**A boot that answers ping and nothing else needs the card out.** `/boot/slot`
selects the root filesystem and lives on a FAT partition, so changing it needs a
shell on the board. A slot that mounts and then starts no listener gives you
neither. `slots/device/S00awatchdog` reverts the marker if the board proves
unreachable, and an instrumented `rcS` records which script failed. Install both
in a slot before trying it, not after.

## Boot time

Measured on a Cube running 1.4.3 from a loop slot, with an `rcS` that records
each script and its exit status to `/bootlog`. Without that log these numbers
cannot be attributed to anything.

| configuration          | `rcS` | eth0 carrier | reboot to ssh | memory used |
| ---------------------- | ----- | ------------ | ------------- | ----------- |
| as the image ships     | 17s   | 21s          | 27s           | 36MB        |
| without `S25wifimod`   | 13s   | 16s          | 17s           | 31MB        |

`S25wifimod` loads `cfg80211`, `aic8800_bsp`, `aic8800_fdrv` and `8733bs`. On a
board with no wifi part the chip never powers on:

```
aicbsp: fail to set AIC_WIFI power state to 1
rwnx_mod_init, set power on fail!
```

Only `aic8800_bsp` stays resident, with no users, and no `wlan0` appears. Those
five seconds also delay `S30eth`, which is why the link trains later. Move the
script aside rather than delete it, because a board with a wifi part needs it:

```shell
mv /etc/init.d/S25wifimod /etc/init.d.S25wifimod.disabled
```

Two smaller costs sit in the same list. `S50ser2net` exits 1 because no serial
configuration exists, and `S01fs` calls `parted` twice to grow p2 to 8192MB on
every boot. `kvmapp/system/init.d/S01fs` now tests the partition table first:
the resize is a no-op once p2 reaches the target, and it cannot succeed at all
once p3 exists, because p3 starts immediately above p2. Both tests read the
table rather than a marker file, because a marker in the root filesystem does
not survive a slot image built from a fresh rootfs.

Do not measure link-up from one boot. On this hardware it is heavy-tailed:
63 samples on a good cable gave a median of 6s and a maximum of 40s, so a
single 21s reading is not evidence of a regression.

## Keeping the server up

Nothing supervises `NanoKVM-Server`. `S95nanokvm` starts it with `&`, and
`inittab` respawns only two gettys, so a segfault leaves the KVM unreachable over
HTTP until a person intervenes. On out-of-band management hardware that is the
worst failure available: the board exists to answer when the machine it controls
does not.

`service/S98supervise` closes that. It is additive - it never starts the services
at boot, so if it is wrong the worst it does is nothing - and it needs no change
to `S95nanokvm`, because a deliberate stop already leaves a signal:
`S95nanokvm stop` removes `/tmp/server` after killing the process.

| `/tmp/server/NanoKVM-Server` | process | verdict |
| ---------------------------- | ------- | ------- |
| present                      | absent  | it died, restart it |
| absent                       | absent  | someone stopped it, leave it alone |

Measured on hardware: `kill -9` on the server, **serving again 8 seconds later**
with no help. A deliberate `stop` was left down for 25 seconds untouched.

The retry delay grows 5, 10, 20, 40 and holds at 60 seconds, and never gives up:
a binary that cannot start must not be retried in a tight loop on a single core,
and must not be abandoned either. A run lasting a minute resets the delay, so a
board that crashes once a day does not creep to the cap and stay there.

`deploy/deploy-server` is the other half. It snapshots, installs, restarts,
probes, and restores the previous binary if the new one does not answer. Two of
its checks are less obvious than they look, and both are there because the first
version got them wrong:

- **Serving is not sufficient.** `S95nanokvm` runs a copy from `/tmp`, so an HTTP
  200 can come from a stale copy while the file just installed is corrupt. That
  happened: `/tmp` is a 79MB tmpfs, a 23MB candidate was staged into it on top of
  24MB of build leftovers, `cp` failed with ENOSPC, and a truncated binary was
  reported as a successful deploy. Stage on `/data`, and require that what is
  running is what was installed.
- **The known-good copy is only replaced while the server answers.** Otherwise
  deploying twice preserves the first broken binary as the fallback.

Both scripts detach with `setsid`. Redirecting a background loop's stdio to
`/dev/null` looks sufficient and is not: measured over ssh, an earlier
`S98supervise start` printed its line and then held the session open until the
client gave up after five minutes.

## Recovering a board that will not boot

Check which of these your enclosure actually gives you before you need one. On
the Cube tested here the case exposes two buttons, and both are ATX passthrough
to the managed host - `/etc/ipmi/chassis_control.sh` drives GPIO 503 and 505 as
outputs, and reads the host power LED on 504. Neither resets the KVM.

Software sees exactly one `gpio-keys` input, which is the LicheeRV Nano's own
User Key. **This case does not expose it.** The small hole in the shell was
examined and holds no switch. So the Cube has no physical control that acts on
the KVM: every recovery below needs either a working shell or the SD card in a
reader.

There is no serial console either. `inittab` respawns a getty on `ttyGS0`, but
`S03usbdev` only creates HID functions, so that device never appears. Adding an
`acm` function would give the managed host a root console over the USB cable
that is already connected - worth considering, and a security decision, because
whoever controls the managed machine would then have root on the KVM.

The stock initramfs has a mass-storage recovery mode that predates any of this:

- `touch /boot/rec`, then reboot, or
- hold the User Key while powering on.

Either exposes the whole SD card to a USB host, so `/boot/slot` can be deleted or
`boot.sd` restored from another machine without opening anything.

Neither is available on this Cube. The marker file needs a working shell, which
is the thing that has failed; the User Key is not brought out to the case. That
leaves opening the enclosure and putting the card in a reader, so the software
that avoids reaching that point carries the whole load:

| what fails | what recovers it | needs a person? |
| --- | --- | --- |
| the server crashes or wedges | `S98supervise` | no |
| a deploy does not serve | `deploy-server` restores the previous binary | no |
| a slot never becomes reachable | `S00awatchdog` reverts the marker and reboots | no |
| the boot path itself | SD card in a reader | **yes, and the case must come off** |

Keep a copy of the stock image before replacing it, and keep the fallback slot
carrying these scripts - a revert that drops them lands on a board with no
recovery at all.
