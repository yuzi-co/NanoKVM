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

The supervisor starts the server itself, rather than through `S95nanokvm`,
because a full restart copies 36MB back into tmpfs for nothing. It therefore
carries the same redirection: the server's output goes to
`/tmp/nanokvm-server.log`, which is where `S99vidiag` reads it. Without that, the
first crash ends the record of the capture pipeline, and it ends it silently -
the file stays where it is, and nothing looks wrong. The supervisor appends,
because what a dead server said last is the most useful part of the file.

Measured on hardware after the change: `kill -9` left the log at 10876 bytes, the
supervisor started the replacement 5 seconds later, and the log reached 21982
bytes. Before the change it stayed at 10876.

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

## The RTOS messages at boot are expected

Every boot prints this at about 2.9 seconds:

```
name=1900000.rtos_cmdqu
RTOS_CMDQU_SEND_WAIT timeout
communicate with rtos fail
```

It reads like a fault and is not one. The SG2002 has two RISC-V cores: a C906B
that runs Linux, and a C906L meant for an RTOS. This image carries no firmware
for the C906L, so nothing answers the mailbox. The first-stage loader says so
itself - `fip.bin` contains the string `No C906L image.`, next to the
`blcp_2nd_runaddr` and `blcp_2nd_size` messages that belong to the same
little-core payload slot.

`soph_rtos_cmdqu.ko` probes anyway, sends one command, waits, and gives up after
210ms. The only costs are that 210ms and the alarming wording.

**Do not use it to explain an intermittent fault.** It fails on every boot, so it
cannot account for anything that happens on some boots and not others - which is
how it was wrongly blamed for the VI init failure in `kvm_mmf.cpp`.

### What is actually reserved

The device tree reserves nothing for the RTOS core. Checked on the board:

| what | value |
| --- | --- |
| DRAM the kernel is given | `80000000-8fdfffff`, 254MB |
| `reserved-memory/ion` size | `0x04b00000`, 75MB |
| `mmode_resv0@80000000` | 256KB, the OpenSBI monitor |
| any `rtos` reserved-memory node | none |
| `MemTotal` | 158MB |

So the 75MB carveout is not shared with a second operating system. It is all the
video pipeline's, and ION accounts for the whole of it.

### There is headroom in the carveout, and taking it is not safe yet

`/sys/kernel/debug/ion/cvi_carveout_heap_dump/summary` after a day of use at
1920x1080:

```
carveout heap size:78643200 bytes, used:41254912 bytes usage rate:53%,
memory usage peak 52301824 bytes
```

Peak 50MB of 75MB. About 25MB has never been touched, and on a 158MB system that
is worth having.

Getting it means shrinking the ION size in the device tree, and the device tree
lives in the boot image on the shared `/boot` partition - not in either A/B slot.
A bad one takes out both slots at once, and this enclosure exposes no recovery
control, so the only way back is the SD card in a reader. Measure a real peak
first, across a reboot, a resolution change and every video mode, and treat the
change as one that needs the case open if it goes wrong.

## The video pipeline's errors now survive a reboot

The capture pipeline fails to start sometimes, and the failure repairs itself on
the next attempt. Every earlier attempt to explain it worked from no error code
at all, for two reasons:

- `/var/log` is a symlink to `/tmp`. Each reboot destroys the log.
- The server sent its own standard output to `/dev/null`, so every message from
  `libkvm` was discarded as it was written.

`S99vidiag` copies the useful lines to `/data/kvm-diag/vi-errors.log`. `/data` is
a separate partition, so the record survives a reboot.

| what | value |
| --- | --- |
| log | `/data/kvm-diag/vi-errors.log`, one older generation kept |
| sources | `/var/log/messages`, and `/tmp/nanokvm-server.log` |
| rotates at | 256KB |
| stops after | 200 lines for each source in one boot, and says so in the log |
| empties the server log at | 128KB |
| control | `/etc/init.d/S99vidiag` with `start`, `stop` or `restart` |
| tests | `tools/vidiag/test-vidiag.sh`, `tools/vidiag/test-restart-space.sh` |

### The two sources report different halves of the failure

The middleware logs through syslog. Those lines carry the failing function and
the error code of the driver. They do not carry the value that `libkvm` sees.
`libkvm` prints that value with `printf`, and it never reaches syslog: the
library imports `printf`, `puts` and `fwrite`, and it does not import `syslog`.

`S95nanokvm` therefore sends the server's output to `/tmp/nanokvm-server.log`,
and the reader follows that file as well as the syslog.

### A redirection alone captures one line

musl decides how to buffer stdout at the first write. If stdout is not a
terminal, musl releases that first line and then buffers in full. Each message
after it waits for the 1024-byte buffer to fill, or for the process to exit. The
server does not exit, so a failure that prints 65 bytes stays in the buffer while
the board runs.

`tools/vidiag/buftest.c` measures this on the device. It prints 12 messages, one
each second, to a file:

| buffering | after 5 seconds | after the program exits |
| --- | --- | --- |
| default | 1 message | 12 messages |
| `setvbuf(stdout, NULL, _IOLBF, 0)` | 5 messages | 12 messages |

The server asks for line buffering at startup. See
`server/common/libkvm_stdout.go`. The Go runtime writes with `write(2)` and does
not use C stdio, so the call changes the output of the C libraries only.

### What the filter keeps

The script keeps every `[VPSS-ERR]`, `[VI-ERR]`, `[SYS-ERR]` and `[VENC-ERR]`
line, every `base_mod_jobs` line from the kernel, and every named `CVI_` call
from the server's log. It is deliberately not a list of the calls we expect to
fail: nobody knows which call fails, and a list of guesses would drop the one
line that matters.

It removes the per-frame errors first. `CVI_VPSS_GetChnFrame` fails once a second
for as long as a viewer is connected to a target whose display is asleep. Those
lines were 695 of the 700 lines in one 12-minute window of the raw syslog, and
they rotate every other line out of it. Recording them would also make a hot file
on the boot medium.

The reader empties the server's log at 128KB. tmpfs holds 80MB, and a server
restart copies 36MB of `/kvmapp/server` into it. `libkvm` prints on some error
paths once per frame, so a pipeline that fails in a loop fills tmpfs. That fault
is worse than the one under investigation.

The reader starts with a sweep of each source, because this script runs last in
the boot order and the drivers load first. The sweep repeats if you restart the
reader by hand, so it writes a header first. A repeated line under that header is
the reader reading the file again, not the fault happening again.

### The trim stops when the reader stops, so the restart order matters

The trim above runs inside the reader's loop. If the reader stops, nothing
empties the file, and `S95nanokvm` must not depend on it.

`S95nanokvm` used to empty the log after it copied `/kvmapp/server` into tmpfs.
That order leaves no margin. Removing `/tmp/server` returns exactly the space
that copying it back needs, so the copy succeeds only while nothing else writes
to `/tmp`.

`tools/vidiag/spacetest.sh` replays the restart case on a tmpfs of the device's
size, with the device's own directory sizes. Run it under Docker:

```shell
docker run --rm --tmpfs /t:rw,size=80892k -v "$PWD/tools/vidiag:/s:ro" \
    busybox sh /s/spacetest.sh /t
```

| order | during the copy | the binary the case starts |
| --- | --- | --- |
| empty after the copies | nothing else writes | 36236K of 36236K |
| empty after the copies | syslogd writes 256KB | 35980K of 36236K |
| empty before the copies | syslogd writes 256KB | 36236K of 36236K |

The second row is the case the device meets. `/var/log` is a symlink to `/tmp`,
so syslogd writes there for the whole restart, and the same driver errors flood
it. `cp` then reports `No space left on device`, the case starts a binary that is
256KB short, and the KVM stays down until an operator logs in.

`S95nanokvm` now empties the log before the first copy. The flood's space returns
before anything asks for it, and the margin is 42MB instead of zero.
`tools/vidiag/test-restart-space.sh` checks that order in both cases.

### What the first sweep found, and what it did not

The reader recorded two events. Both are server restarts that the operator
started: the times match `deploy-restart.log` and `deploy-solib.sh` on the
device. Each event has the same shape:

```
[VI-ERR] cvi_vi.c:101:CHECK_VI_CTX_NULL_PTR(): Call SetDevAttr first
[VI-ERR] lt6911_sensor_ctl.c:136:lt6911_write_register(): I2C_WRITE error!
[VI-ERR] lt6911_sensor_ctl.c:85:lt6911_read_register(): I2C_WRITE error!
[VI-ERR] lt6911_sensor_ctl.c:214:lt6911_probe(): read sensor id error.
[VI-ERR] lt6911_sensor_ctl.c:222:lt6911_probe(): Sensor ID Mismatch! Use the wrong sensor??
```

The driver tries three times, fails to read the chip identifier, and reports a
mismatch. Capture then works: the same boot delivers 1920x1080 at 28fps.

The teardown that runs before each attempt fails as well:

```
[LOG-ERR] sample_common_vi.c:714:SAMPLE_COMM_VI_StopViChn(): CVI_VI_DisableChn failed with 0xc00e8007!
[LOG-ERR] sample_common_vi.c:948:SAMPLE_COMM_VI_StopSingleViPipe(): CVI_VI_StopPipe failed with 0xc00e8007!
[LOG-ERR] sample_common_vi.c:606:SAMPLE_COMM_VI_StopDev(): CVI_VI_DisableDev failed with 0xc00e8007!
```

The `CVI_` rule found these. The filter has no rule for the `[LOG-ERR]` tag, so
a keep-list built from the tags alone would have dropped them.

**This is the signature of a start that succeeds. It is not the fault.** An
earlier revision of this file offered these lines as the first evidence upstream
of the VI init failure. That reading was wrong. The record holds no `[VPSS-ERR]`
from `CreateGrp`, `ResetGrp` or `StartGrp`, so the intermittent failure did not
happen while the reader ran. Do not write these lines into a code comment as an
explanation - that mistake has already been made once here with the RTOS
messages.

### The summary line carries no error code. Read the line above it.

`mmf_vi_init` prints `_mmf_vpss_init_new failed. s32Ret: 0x%x !` when the
pipeline does not start. That value is always `0xffffffff`.
`_mmf_vpss_init_new` returns `CVI_FAILURE` from each of its three failure paths,
so the compiler folds the constant into the call. Disassemble the shipped
library, and the instruction before the `printf` is `li a1,-1`:

```shell
docker run --rm -v "$PWD/server/dl_lib:/w:ro" ubuntu:24.04 sh -c \
  'apt-get update -qq && apt-get install -y -qq binutils-riscv64-linux-gnu \
   && riscv64-linux-gnu-objdump -d /w/libkvm_mmf.so | sed -n "/_Z11mmf_vi_initv/,/ret/p"'
```

The driver's own code arrives one line earlier, from the `SAMPLE_PRT` calls
inside `_mmf_vpss_init_new`:

```
CVI_VPSS_CreateGrp(grp:0) retry(0xc0068003)!
CVI_VPSS_ResetGrp(grp:0) failed with 0xc0068003!3
CVI_VPSS_StartGrp failed with 0xc0068003
```

The filter keeps all three through its `CVI_VPSS_` rule. That rule is what makes
the record useful, because the line everybody quotes is the empty one.

The codes read as `(module << 16) | (level << 13) | errid`, with level 4 for an
error. The module numbers are not obvious, so take them from the binary rather
than from a guess. The `ResetGrp` message passes `CVI_ERR_VPSS_ILLEGAL_PARAM` as
a literal, and the disassembly shows `0xc0068003`. VPSS is therefore **6**, and
`errid` 3 is an illegal parameter. VENC is 3 and VI is 14, both read the same way
from `CVI_VENC_CreateChn ... 0xc0038007` and `CVI_VI_DisableChn ... 0xc00e8007`.
The `errid` 7 in those two agrees with the syslog line beside them, `Call
SetDevAttr first`, which reports a call made before its configuration.

One recorded line reads `_mmf_vpss_init_new failed. s32Ret: 0xc0078003 !`. **Do
not use it.** No build of this source can print anything but `0xffffffff` there,
and the library on the device has not changed since before that line was
written. The most likely source is a test build staged under `/tmp` during a
deploy, which the next reboot removed.

### The leaked VPSS group is real, and it is the repair

`_mmf_vpss_init_new` creates the group, and it returns `CVI_FAILURE` from the
`ResetGrp` and `StartGrp` paths without destroying it. `mmf_vi_init` then returns
early and leaves `vi_is_inited` false, so `mmf_vi_deinit` skips the group as
well. The group stays.

That looks like the fault, and it is the opposite. The next `mmf_vi_init` calls
`CVI_VPSS_CreateGrp`, the driver answers that the group exists, and the code
retries: it calls `CVI_VPSS_DestroyGrp`, then it creates the group again. **This
is the self-repair that the investigation keeps describing.** Destroying the
group on the error paths would tidy the code. It would not stop the failure,
because the failure is over by the time the group leaks.

The open question is unchanged, and it sits one step earlier: why `ResetGrp` or
`StartGrp` refuses. The reader now records that answer when it happens.
