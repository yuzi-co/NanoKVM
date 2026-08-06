# ION Carveout Visibility — Design

**Status:** approved 2026-08-07
**Sub-project:** (b) of the four-part ION programme
**Branch:** `feat/ion-visibility`

## Why this exists

A failed ION allocation kills the server in under a second. `libkvm` does not check the
allocation result, so an exhausted carveout is a segfault and not an error. Only a reboot
recovers the board. Sub-project (a) made that survivable: `S98supervise` now reboots when
restarting cannot work. This sub-project makes the condition **visible before it happens**.

Nothing here rebuilds `libkvm`. The two sub-projects that do — (c) survive a failed
allocation, (d) fix the leak itself — stay out of scope.

## What we measured, 2026-08-06

The earlier theory was wrong, and the measurements replaced it. This section records what the
device does, because every threshold below depends on it.

### The leak is the server restart, not the capture cycle

`vi-errors.log` carries `CVI_VI_DisableChn failed with 0xc00e8007` and `CVI_VI_StopPipe
failed`. An earlier note read those as a failing capture teardown. They are not. All 146 lines
land within two seconds of a boot. They come from the `try release vio` and `try release venc`
calls that the init path makes before `mmf_add_vi_channel`. They fail because nothing was
running yet. They are benign.

Capture is never torn down on this build. Six hours of uptime, one browser session, five
screenshots and zero viewers left `alloc_mem` unchanged. `hdmi_idle.go` is not on this branch.
`/etc/kvm/hdmi_idle_timeout` on the device is a leftover from an older binary.

Process death frees nothing. One measured restart:

| moment | `alloc_mem` |
| --- | --- |
| before the restart, capture warm | 42,942,464 |
| after the restart, capture not started | 49,459,200 |

The new process allocated `ISP_SHARED_BUFFER_0` (294,912) and `VbPool4` (6,221,824). That is
6,516,736 bytes, and it is exactly the increase. Everything the dead process held stayed
allocated: `VbPool0`, `VbPool2`, `VbPool3`, `jpeg_ion`, four `VENC_1_*` buffers and
`VCODEC_H264_FW_Buffer`. The buffers belong to the `soph_*` drivers, not to the process file
descriptors, which is why `rmmod soph_vpss` reports the module busy with no process running.

The failure is therefore self-amplifying. A crash causes a restart. The restart orphans the
working set. Less carveout makes the next crash arrive sooner. This also inverts the obvious
mitigation: **fewer restarts is the prevention, and an early reboot is protective.**

### The working set is cumulative over delivery paths

| state | `alloc_mem` | what appeared |
| --- | --- | --- |
| fresh boot, capture never started | 19,050,496 | `VbPool0`, `VbPool1`, `ISP_SHARED_BUFFER_0` |
| after screenshots only | 31,600,640 | `jpeg_ion`, `VENC_1_*`, `VCODEC_H264_FW_Buffer` |
| after a browser stream as well | 42,942,464 | `VbPool2`, `VbPool3` |
| after one server restart | 49,459,200 | a second `ISP_SHARED_BUFFER_0`, `VbPool4` |

A board that only serves screenshots needs less than a board that also streams H264. A fixed
reserve constant would be wrong in both directions. The design measures the requirement instead.

### `peak` is writable and resettable

`/sys/kernel/debug/ion/cvi_carveout_heap_dump/peak` accepts a write. Measured: `echo 0 > peak`
returned exit 0 and did not block, `peak` read back as 0, and the next allocation set it to
31,600,640 to match `alloc_mem`. This gives a measured requirement in place of a constant.

## Architecture

Three parts, each usable alone.

1. **`server/service/ion/`** reads the counters and computes the derived fields.
2. **`GET /api/vm/ion`** publishes them.
3. **`S98supervise`** writes one line to its own log at each restart it performs.

No new process. No new boot script. No new file on `/data`. The supervisor is the only
component that outlives the server, so it is the only place that can record what the carveout
held when a restart happened.

### Source of truth

`/sys/kernel/debug/ion/cvi_carveout_heap_dump/`:

| file | contents |
| --- | --- |
| `total_mem` | carveout size, an integer |
| `alloc_mem` | bytes allocated, an integer |
| `peak` | high-water mark of `alloc_mem`, an integer, writable |
| `summary` | the per-buffer `Details` block with names and `phy_addr` |

Do not read `/proc/cvitek/vb`. That file blocks the reader forever in uninterruptible sleep.

### Derived fields

- `free` = `total_mem` - `alloc_mem`.
- `generations` = the count of `ISP_SHARED_BUFFER_0` entries in `Details`. One entry means one
  live server process. Two or more mean that many processes have died holding memory.

  **This rests on one measurement.** A fresh boot showed one entry, and one restart made two.
  We have not confirmed that the count is exactly one for each server process, and we have not
  confirmed which process allocates it — `kvm_system` also runs at boot. Hardware acceptance
  must take the count through two restarts, not one. If the relationship does not hold, report
  `generations` as unknown rather than report a number we cannot defend.
- `reserve` = `peak` - `allocAtStart`, where the server records `allocAtStart` at startup and
  then writes 0 to `peak`. This is the largest growth this process has ever caused on this
  board, for the delivery paths this board actually uses.
- `verdict` compares `free` against `reserve`.

The design reports `generations` and not "orphaned bytes". The duplicate names prove that older
generations exist. They do not let us attribute bytes to them reliably. `generations` is the
truthful form of the signal, and it is the one that tracks restarts.

### Verdict

| verdict | condition | what the operator does |
| --- | --- | --- |
| `ok` | `free >= 2 x reserve` | nothing |
| `warn` | `reserve <= free < 2 x reserve` | reboot when convenient |
| `critical` | `free < reserve` | reboot now |
| `unavailable` | the counters cannot be read | nothing, and the UI shows nothing |

`warn` is not "there is room for one more session". There is only ever one capture session. It
means **one restart away from `critical`**. A restart orphans the whole working set and
allocates a second copy, so the cost of a restart is a full generation and not a difference.

`critical` on its own is too late to be useful. At `critical` the KVM looks healthy until
someone opens the stream, and opening the stream is what kills the server. `warn` is the state
an operator can act on.

## Components

### `server/service/ion/parse.go`

Pure functions. Text in, struct out. No filesystem access in this file, so the whole parser is
testable off-device under `novision`.

- `ParseCounter(string) (uint64, error)` — one integer file.
- `ParseSummary(string) (Summary, error)` — the `Details` block into buffer entries.
- `CountGenerations(Summary) int` — `ISP_SHARED_BUFFER_0` entries.
- `Verdict(free, reserve uint64) string` — the table above.

### `server/service/ion/ion.go`

The only file that touches the filesystem. It holds `allocAtStart`, resets `peak` once at
startup, and reads the counters when asked. The root directory is a variable so tests can point
it at a fixture directory.

The reset is optional. If the write fails, the service records that and falls back to the
configured floor for `reserve`. A firmware that refuses the write must still get a working
endpoint.

### `server/service/vm/ion.go`, `proto/vm.go`, `router/vm.go`

The usual thin layer. `GET /api/vm/ion` behind `middleware.CheckToken`, the response through
`proto.OkRspWithData`.

```json
{ "total": 78643200, "used": 31600640, "free": 47042560,
  "usageRate": 40, "generations": 1, "reserve": 12550144, "verdict": "ok" }
```

All byte counts are integers. `usageRate` is a whole percent, rounded down, so that it agrees
with the `usage rate:40%` line that `summary` prints. `verdict` is one of `ok`, `warn`,
`critical` or `unavailable`.

### Configuration

`server.yaml` gains one optional block. The floor applies only before the first capture, when
`peak` has nothing to report yet.

```yaml
ion:
    reserveFloor: 25165824   # 24MB, used until this process has captured once
```

### Web UI

Two surfaces with different jobs.

**A row in Settings.** Usage and, when `generations` is greater than 1, the sentence "N server
restarts are holding video memory". Always present. It never shouts.

**A badge that exists only when something is wrong.** It follows the `capture-status` pattern,
which renders nothing while healthy. Amber for `warn`, red for `critical`, each with the one
action that helps. `unavailable` renders nothing.

**The fetch must land before the stream starts.** At `critical`, starting the stream is the
event that kills the server. A warning that appears after the crash has no value. The desktop
page therefore reads `/api/vm/ion` on mount, before it opens the stream.

**At `critical` the UI holds the stream until the operator confirms.** Every other function of
the KVM keeps working — power control, HID, settings, the reboot button — because the
allocation that fails is the video allocation. Holding the stream keeps every other way of
rescuing the board. The operator can still continue, and the dialog says what happens if they do.

Add every new string to `src/i18n/locales/en.ts`.

### `S98supervise`

One line, appended to the existing log at each restart the supervisor performs:

```
2026-08-07 09:14:22 ion 31600640/78643200 40% gen=2
```

Two `cat` calls and one `grep -c`. It goes in the `act` block, guarded the way
`capture_evidence` is guarded. **It must never delay a restart and never fail one.** The script
stays fork tooling: install it to `/etc/init.d` only, never to `/kvmapp/system/init.d`.

## Error handling

The rule is that a diagnostic must not become a fault.

| condition | behaviour |
| --- | --- |
| a counter file is missing | `verdict: "unavailable"`, HTTP 200, UI silent |
| `summary` does not parse | counters still reported, `generations: 0`, `verdict` still computed |
| the write to `peak` fails | `reserve` falls back to `reserveFloor`, and the payload says so |
| the read blocks | it cannot: these files are integers in debugfs, and `/proc/cvitek/vb` is never read |

## Testing

- **Parser tests off-device**, under `novision`, against fixtures of all four measured states.
  The 49,459,200 fixture with two `ISP_SHARED_BUFFER_0` entries at `8bef4000` and `8dbf4000` is
  the only one that proves `generations`, and we have it because we caused it deliberately.
- **Verdict as a table**, with explicit cases at `free == reserve` and `free == 2 x reserve`.
  An error at those boundaries gives either a KVM that cries wolf or one that reports `ok` while
  it is doomed.
- **Shell tests** in `tools/service/test-supervise.sh` for the new line, with the wiring check
  anchored to the call site and not to the function name. New cases in
  `test-supervise-mutation.sh`. Every mutation must be caught.
- **Hardware acceptance:** read the endpoint on the live board; force one restart; confirm
  `generations` goes from 1 to 2 and that the reported bytes agree with `alloc_mem`; reboot;
  confirm `generations` returns to 1.

## Global constraints

- Do not write runtime state under `/kvmapp`.
- `S98supervise` installs to `/etc/init.d` only.
- Init scripts keep LF line endings.
- `go vet -tags novision ./...` and `go test -tags novision ./...` must pass.
- `CGO_ENABLED=0 GOOS=linux GOARCH=riscv64 go build -tags novision ./...` must pass.
- New UI strings go into `en.ts` at minimum.
- Docs follow ASD-STE100, and the `_ZH` and `_JA` variants stay in step.

## Out of scope

- **Sub-project (c):** make `libkvm` survive a failed allocation, and correct the `chack_ion`
  parse. Needs a rebuild.
- **Sub-project (d):** fix the leak. The measurements point at a specific shape — the init path
  should destroy the VB pools that already exist, instead of calling `try release vio` and
  `try release venc` against a context it does not own. Needs a rebuild.
- **Pre-emptive reboot by the supervisor.** This spec gives an operator the number. It does not
  let the board act on it. That decision belongs in its own sub-project, and it should use the
  erosion data this one produces.
- **The audio restart storm.** `arecord` fails with `pcm_read: I/O error` about ten seconds
  after a viewer opens audio, and the retry counter resets from 4/8 back to 1/8, so the give-up
  limit is unreachable and the loop runs forever. It is unrelated to ION. It is recorded here
  only so it is not lost.

## Leads not taken

Every `/proc/cvitek/*` entry is mode `-rw-r--r--`, including `sys`, `vpss`, `venc` and `vi`.
A pool-teardown command there would be prevention with no rebuild. We did not probe them:
`/proc/cvitek/vb` is their sibling, and reading it hangs the reader in uninterruptible sleep.
The proper fix belongs in sub-project (d), where a rebuild can be tested, rather than in an
undocumented write that the memory safety of the KVM would then depend on.
