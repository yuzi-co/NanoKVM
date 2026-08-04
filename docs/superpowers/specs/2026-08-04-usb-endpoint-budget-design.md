# USB endpoint budget — design

**Date:** 2026-08-04
**Status:** approved, ready for planning

## Problem

The USB controller on the SG2002 fits nine endpoints of gadget functions. Nothing in the product
knows that. `Settings > Device` offers three switches, `S03usbdev` reads
five `/boot/usb.*` markers, and none of them counts anything. Enabling the fourth function
overruns the controller, the gadget refuses to bind, and **every USB function dies together** —
including the keyboard and both mice, which are the reason the device exists.

Observed on hardware on 2026-08-04. Virtual disk and virtual network were switched on while the
serial console and the speaker were already enabled:

```
configfs-gadget gadget: acm/000000000223f9a0: can't bind, err -19
configfs-gadget 4340000.usb: failed to start g0: -19
```

`/dev/hidg0..2` never appeared. The web UI looked completely normal: all three switches read ON,
the video stream ran, and the API answered. The failure is silent from every surface an operator
looks at.

Two properties make this worse than a single broken feature:

- **The blame is misdirected.** `acm` is linked late, so `acm` reports the error. The function
  that overran the budget is not named anywhere.
- **It survives a reboot.** The markers are files on `/boot`. A board in this state comes back up
  in the same state, so the obvious recovery does not work.

### Endpoint costs

| Function | Endpoints | Marker |
| --- | --- | --- |
| `hid.GS0` keyboard | 1 | absence of `/boot/disable_hid` |
| `hid.GS1` relative mouse | 1 | absence of `/boot/disable_hid` |
| `hid.GS2` absolute pointer | 1 | absence of `/boot/disable_hid` |
| `acm.GS0` serial console | 3 | `/boot/usb.acm` |
| `rndis.usb0` / `ncm.usb0` network | 3 | `/boot/usb.rndis0` / `/boot/usb.ncm` |
| `mass_storage.disk0` disk | 2 | `/boot/usb.disk0` |
| `uac1.usb0` speaker | 1 | `/boot/usb.uac` |

HID takes 3 and leaves 6.

### How the budget was established

The number is measured, not read. `dmesg` reports `dwc2 4340000.usb: EPs: 8, dedicated fifos,
3072 entries in SPRAM`, and **that is not the number that governs** — three separate configurations
totalling nine endpoints bind and work. A guard that trusted the kernel's line would refuse
`acm + network` and `acm + disk + audio` forever, and both are known good.

Six configurations were built on the device on 2026-08-04, each by writing markers and rebooting:

| Set (HID always present) | Total | Result |
| --- | --- | --- |
| `acm + audio` | 7 | binds |
| `acm + network` | 9 | binds |
| `acm + disk + audio` | 9 | binds |
| `disk + network + audio` | 9 | binds |
| `acm + network + audio` | 10 | fails, `-19` |
| `acm + disk + network + audio` | 12 | fails, `-19` |

Nine binds three times, ten fails. The per-function costs above come from the USB descriptors and
are consistent with every one of these points.

Two models were eliminated by measurement. "Total endpoints, budget 8" predicts `acm + network`
fails; it binds. "Only IN endpoints are scarce" predicts `acm + network + audio` binds, because
the speaker's single endpoint is an isochronous OUT; it fails. Total endpoints against a budget
of nine is the simplest model that fits all six.

Three of the four optional functions therefore fit, in two combinations: `acm + disk + audio` or
`disk + network + audio`. The console and the network cannot both be on with anything else added.

## Scope

**In scope**

- A budget rule enforced at boot in `S03usbdev` and at the toggle in the server.
- Priority ordering, applied only at boot.
- Reporting of what is actually active, as distinct from what is enabled.
- A UI that states the budget, the per-function cost, and the reason a switch is unavailable.
- A switch for the serial console, which is the one function that has never had one.

### The serial console becomes togglable

Today `/boot/usb.acm` is created by hand over SSH. It gets a switch beside the other three, for
one reason above all: it consumes three of the nine endpoints, which is a third of the budget and
the largest single claim after HID. A budget display that shows `9 of 9 used` while offering no
way to free the biggest consumer tells the operator their problem and withholds the answer.

**This changes the security posture, and the UI must say so.** `/etc/inittab` respawns a getty on
`ttyGS0`, so an enabled console gives whoever controls the managed host a root login prompt on the
KVM. Creating the marker over SSH was a deliberate speed bump; a switch removes it. The row
therefore carries a warning in the same style as the existing SSH toggle's tip, which already
says "Set a strong password before enabling".

**The unmount must remove only the symlink.** `rmdir` on `functions/acm.GS0` blocks indefinitely,
because the respawning getty holds `/dev/ttyGS0` open, and recovering from that needs a full
teardown of the gadget. This is the same rule the audio function already follows, and it is
asserted by an existing test.

**Out of scope**

- The ION carveout crash and its leak. Separate spec, agreed 2026-08-04.
- Changing which functions exist, their descriptors, or their endpoint costs.
- A user-editable priority order. The order is a constant in the source.
- A user-facing choice about HID itself. `/boot/disable_hid` is read, and HID costs 0 when it is
  set, but nothing in this feature offers to turn HID off.

## Priority

```
HID  >  ACM console  >  virtual disk  >  virtual network  >  speaker
```

HID never yields; it is the product. The serial console ranks next because it is the only way into
a board whose network is gone, which is a state this device reached on 2026-08-04. The speaker
drops first because it is the only entry that costs nothing to lose.

The order is used **only** when the board boots with more enabled than fits. The interactive path
never applies it — see below.

## Two enforcement points

They answer different questions, so they behave differently.

### Boot: `kvmapp/system/init.d/S03usbdev`

`S03usbdev` runs long before the server exists, so the server cannot be the only guard. A board
that reboots with too many markers must still come up with HID.

1. Read the markers and build the enabled set.
2. Offer each enabled function a place, in priority order, keeping the ones that still fit.
3. Link the survivors into `configs/c.1` in that same order.
4. Write one line per function that did not get a place, to the console and to
   `/data/usb-endpoints.log`.

Step 2 offers places rather than giving up the lowest-ranked members until the total comes down.
The two rules agree on which function outranks which and disagree on the result: with everything
enabled, giving up loses the speaker, then the network, and settles on `acm + disk` at eight of
the nine endpoints — while `acm + disk + audio` is exactly nine and keeps one function more.
Offering places never leaves room unused, and it cannot promote a lower-priority function over a
higher one, because the higher one is offered first.

**Dropping never deletes a marker.** The operator's intent stays on disk. When they later turn
something else off, the dropped function returns on its own. A guard that rewrites configuration
behind the operator is a second way to lose settings, and this device already loses enough.

The budget is the constant 9. It is deliberately **not** read from `dmesg`: that line reports 8,
and three measured configurations of nine endpoints bind. Parsing it would install a wrong number
on every board and refuse configurations that work.

Link order already matters for an unrelated reason: configfs numbers interfaces in link order, and
a function inserted ahead of the HID ones renumbers the keyboard and the mice under a host that is
already bound to them. Priority order and safe link order agree here — HID first, everything else
after — so one loop satisfies both.

### Toggle: `server/service/vm/virtual-device.go`

The interactive path refuses. It never drops anything, because a person is present to be told.

`POST /api/vm/device/virtual` returns an error when the requested function would overrun:

```
network needs 3 endpoints, 1 free — turn off the serial console (3) or the virtual disk (2) first
```

Turning a function **off** is always allowed and never checked.

### Reporting what is real

`GetVirtualDevice` currently reports marker presence, which is why a switch reads ON for a function
that is not running. It gains a second fact per device:

- `enabled` — the `/boot` marker exists (unchanged meaning)
- `active` — a symlink for the function exists in `configs/c.1`

`GetVirtualDeviceRsp` grows from three booleans to four objects, plus the budget:

```go
type VirtualDeviceState struct {
    Enabled bool `json:"enabled"`
    Active  bool `json:"active"`
    Cost    int  `json:"cost"`
}

type GetVirtualDeviceRsp struct {
    Console VirtualDeviceState `json:"console"`
    Network VirtualDeviceState `json:"network"`
    Disk    VirtualDeviceState `json:"disk"`
    Audio   VirtualDeviceState `json:"audio"`
    Used    int                `json:"used"`
    Total   int                `json:"total"`
}
```

This is a breaking change to one response shape. Both producer and consumer are in this
repository and ship together, so no compatibility shim is needed. `Media` is dropped from the
response: it is already unused by the frontend.

## One table, two languages

Costs and priority are needed in shell and in Go. Neither can call the other: the shell runs before
the server exists, and the server must not shell out to read a constant.

The table is therefore written twice, and a test parses both files and fails when they disagree.
`tools/service/test-supervise.sh` already uses exactly this technique to keep `SERVER_LOG` identical
across `S98supervise` and `S95nanokvm`, so the pattern is established in this repository rather than
invented here.

## UI

`web/src/pages/desktop/menu/settings/device/virtual-devices.tsx`.

```
USB endpoints                                    9 / 9 used
████████████████████████████████████████

Serial Console          uses 3                    [ on  ]
  Log in to this NanoKVM from the remote host when
  the network is unreachable
  ! Anyone who controls the remote host gets a login
    prompt on this NanoKVM. Set a strong password first.

Virtual Disk            uses 2                    [ on  ]
  Mount SD card on the remote host

Virtual Network         needs 3, 0 free           [ off ]  disabled
  Not enough USB endpoints. Turn something else off first.

Virtual Speaker         uses 1                    [ on  ]
  Play audio from the remote host through USB
```

1. A budget line at the top, always visible. Nothing today tells an operator a limit exists until
   HID disappears.
2. Every row states its cost: `uses N` when on, `needs N, M free` when it does not fit.
3. A switch that cannot fit is rendered disabled, with the reason naming what to turn off. The
   server refuses independently — the UI is a courtesy and the server is the guard.
4. When `enabled && !active`, the switch reads on and carries a warning: the gadget ran out of
   endpoints, and the function starts on the next reboot once something else is turned off. This
   state must not look identical to a working one.
5. The serial console row carries its security warning in both states, on and off. It describes
   what the switch does rather than reporting a fault, and it follows the wording of the existing
   SSH toggle's tip.

Failure text comes from the server rather than being recomputed in the frontend, so the two cannot
disagree about the numbers.

New strings go in `web/src/i18n/locales/en.ts`. The other locales fall back to English; machine
translations that nobody can check are worse than an untranslated string.

## Testing

**Shell** — extend `tools/service/` with a test in the shape of `test-supervise.sh`: extract the
budget block from `S03usbdev` and drive it with marker sets.

- HID alone fits, and nothing is dropped.
- `acm + audio` = 7, nothing dropped.
- `acm + disk + audio` = 9 exactly, nothing dropped.
- `acm + network` = 9 exactly, nothing dropped.
- `acm + disk + network + audio` = 12, drops audio then network, keeps `acm + disk` at 8.
- Every drop is logged.
- No marker file is removed by any case.
- HID is never dropped, at any input.

**Go** — table tests over the budget function, plus the cross-language test that parses the costs
and priority out of `S03usbdev` and asserts they match the Go table.

- A toggle that fits is allowed.
- A toggle that overruns is refused, and the message names a function that would free enough.
- Turning off is always allowed, including from an over-budget state.

**Device** — the case that matters cannot be unit tested: enable everything, reboot, and confirm
`/dev/hidg0..2` exist and `cat /sys/class/udc/*/state` reads `configured`. That is the exact
failure of 2026-08-04, and it is the acceptance test.

## Branching

Cut `feat/usb-endpoint-budget` from `feat/usb-audio`. The budget table has to include `uac1.usb0`,
and the `S03usbdev` block that creates it exists only on that branch. This follows the prerequisite
chains already described in `CLAUDE.md`; the branch lands after the audio work or alongside it.

## Risks

**`S03usbdev` runs at boot and is not easily recoverable.** A bug here can leave a board with no
USB at all. Mitigations: the drop loop only ever removes from a list before any configfs write
happens, HID is never a candidate, and the shell test covers the marker combinations. The known-good
copy stays at `/data/kvm-diag/` as it does for the other init scripts.

**Endpoint costs are measured, not queried.** The kernel does not expose per-function endpoint
counts. If a cost is wrong the guard permits a set that still fails to bind — the same failure as
today, no worse, and the device acceptance test would catch it.

**Two markers select the same function.** `usb.ncm` and `usb.rndis0` are alternatives; `S03usbdev`
prefers NCM. The budget must count the network function once, not twice.

## Alternatives considered

**Enforce only in the server.** Rejected: the server does not exist at boot, and the failure being
fixed is a boot-time failure.

**Enforce only in `S03usbdev`.** Rejected: the toggle would appear to succeed and the operator
would learn what happened after the next reboot.

**Auto-drop at the toggle as well as at boot.** Rejected: a person is present, so telling them is
strictly better than changing something they did not ask about.

**Delete markers when dropping.** Rejected: it destroys the operator's stated intent and makes the
drop permanent, so turning off one function would not bring back the one it displaced.

**Read the budget from the kernel's `dmesg` line.** Rejected after measurement, and this is the
alternative worth recording, because it was the original design and it looked like the responsible
choice. `dwc2` announces `EPs: 8`. Deriving the budget from it is wrong: nine endpoints bind, in
three separate configurations. Taking the number from the one apparently authoritative source
would have shipped a guard that permanently refused `acm + network` and `acm + disk + audio`,
with a comment explaining that it was reading the real value from the hardware.

The lesson is narrow and worth keeping: `EPs: 8` counts something — probably the controller's
device endpoint hardware, before whatever the composite layer does with EP0 and shared
directions — but it does not count what this guard has to count. A measured constant with the
measurements written down beats a derived number nobody checked.
