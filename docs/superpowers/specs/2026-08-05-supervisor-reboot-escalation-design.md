# Supervisor reboot escalation

**Date:** 2026-08-05
**Status:** approved, not implemented
**Scope:** `tools/service/S98supervise` and `tools/service/test-supervise.sh` only

## Why this exists

On 2026-08-04 the board at 10.0.0.222 stopped answering. `NanoKVM-Server` died in
under one second, every time, for twenty-two minutes. `S98supervise` restarted it
twenty-three times. It would have continued indefinitely.

The cause was an exhausted ION carveout. `libkvm` asked for 291KB, the allocation
failed, the library returned `NULL` without checking it, and the process took
signal 11 before it bound port 80. The carveout does not come back when the
process exits: `rmmod soph_vpss` answers `Resource temporarily unavailable` with
zero processes running, because the leaked allocations hold the module. No
userspace action frees it. Only a reboot does, and that reboot is clean — the
board came back in about ten seconds with HID, the ACM console and the UAC1 card
all present.

`S98supervise` cannot escape this. Its own comment states the rule it follows:

> The response is to restart the server, not to reboot the board.

That rule is correct for a hung server and wrong for this. The supervisor needs a
second verdict for the case where restarting is a guaranteed-failure loop.

## The ION programme, and where this sits

ION work decomposes into four sub-projects. Each gets its own spec, plan and
hardware acceptance. The order below is driven by one constraint — whether the
change needs a `libkvm` rebuild — not by size.

| # | Sub-project | Needs libkvm rebuild | Status |
| - | ----------- | -------------------- | ------ |
| a | **Supervisor reboot escalation** — this spec | no | approved |
| b | Carveout erosion made visible: sampler, API, UI | no | not started |
| c | `libkvm` survives a failed allocation; `chack_ion` parses properly | yes | not started |
| d | The capture teardown leak itself | yes | not started |

The letters follow the execution order, not the order the sub-projects were
first described.

Sub-project (a) comes first because it needs no rebuild and it is the only one
that converts "dead until a person intervenes" into "back in twenty seconds".
(b) comes next because it is the instrument (d) needs: "did the carveout stop
eroding" is a measurement, not an opinion. (c) proves the rebuild path on a small
change before (d) uses it.

(d) is the only real cure. (a) only makes the disease survivable. (a) costs an
afternoon and (d) costs many, so (a) goes first.

## Goal

When restarting the server cannot work, reboot the board — and never turn that
into a reboot loop.

## Non-goals

- Detecting ION specifically. The escalation is blind to cause.
- Any change to `libkvm`, the Go server, or the web UI.
- Any change to `S95nanokvm`, `S99vidiag` or `S00awatchdog`.
- Fixing the leak. That is sub-project (d).

## Decisions taken, and why

### The trigger is blind, not ION-specific

The escalation counts failures. It never reads `dmesg` to decide.

Three reasons. First, this repository has already been burned by trusting kernel
output: `dwc2 ... EPs: 8` is wrong for the USB endpoint budget, which is nine, and
finding that out cost a hardware measurement campaign. Second, the ring buffer
holds about eight crashes and rolls within ten minutes, so the signature
disappears exactly as the fault gets worse — a signature-gated guard would weaken
the longer the fault runs. Third, a reboot is the right cure for any cause a fresh
boot clears: a leaked carveout, a wedged driver, a stuck VB pool. Naming ION would
narrow the fix to the one instance that happened to be caught.

The signature still gets captured, as evidence, after the decision is made. See
"Evidence".

### The guard against a reboot loop is uptime, not a counter

A supervisor that reboots can turn one dead server into an unreachable board.
Today's failure at least answers SSH forever; a reboot loop does not, and the card
has to come out.

The guard is a floor on `/proc/uptime`. The board reboots only if it has been up
longer than `REBOOT_FLOOR`.

This works because it follows the physics. A board that runs thirty hours, erodes
its carveout and then crash-loops has high uptime, so it reboots and gets a fresh
carveout. A board whose fault returns immediately after that reboot crash-loops at
low uptime, so it does not reboot again — it stays up and reachable for a person
to work on, which is exactly what happens today. A leak that refills faster than
the floor is a leak a reboot cannot cure, and the script correctly refuses to try.

It also needs no persistent state, which keeps the script's existing property:

> Telling a crash from a deliberate stop needs no new state.

`/proc/uptime` is already read by `now()`.

The rejected alternatives were: reboot once per boot (weaker — a board that eroded
once will erode again in thirty hours, and the second time it just sits dead); a
reboot budget in `/data` (more state to get wrong, and it writes to the SD card);
and no guard at all (one bad SD read and the board is gone).

### Both failure paths escalate

`cure_hung` currently discards `wait_gone`'s answer:

```sh
cure_hung() {
    force_kill      # killall -9
    wait_gone       # returns 1 if the process never left
    full_restart
}
```

A process in uninterruptible sleep does not die from `SIGKILL`. A reader of
`/proc/cvitek/vb` blocks forever in D state; that is measured on this board. So
the one hang that most needs a reboot is exactly the hang `cure_hung` silently
cannot cure, and nothing looks at the return value that would say so.

Crash loops and unrecoverable hangs are the same fault — the cure did not work —
so they share one decision function rather than growing two state machines that
drift apart.

### A short run is under 30 seconds, not 5

The incident's own signature is a process that dies in under one second, and the
first draft of this design used a five-second threshold. It cannot fire.

`watch_loop` sleeps `INTERVAL` (5s) between checks, and `started` is reset
immediately after each start. The `ran` value on the `restart` branch is therefore
quantised to the poll and is never below about five seconds. `[ "$ran" -lt 5 ]`
is an assertion that can never be true.

The five-second figure describes the process's own lifetime, seen from `dmesg`.
A five-second poller cannot resolve it. Different instrument, different number.

Thirty seconds sits clear above the poll quantisation and clear below the sixty
seconds `delay_after_run` already uses to mean "the fault was transient".

### The floor has about twice the margin it needs

Five short runs cost 5, 10, 20, 40 and 60 seconds of backoff — 135 seconds — plus
the runs themselves, at most 30 seconds each. Worst case is about 4.8 minutes from
the first crash. Boot takes about 20 seconds. So a board that crash-loops straight
out of boot reaches the escalation at roughly 5.1 minutes of uptime.

A floor of 10 minutes blocks that case with about 2× margin. It is blocked
firmly, not narrowly.

## Design

### Where the change lives

One file changes: `tools/service/S98supervise`. One test file changes:
`tools/service/test-supervise.sh`.

This script is fork tooling. It exists in `tools/service/` and is installed to
`/etc/init.d/S98supervise` on the device. It is **not** part of `kvmapp/`, so the
usual rule about installing an init script to both `/etc/init.d` and
`/kvmapp/system/init.d` does not apply here. `/etc/init.d` is the only target.

### The decision

The escalation lives in its own extractable block, matching the `# --- decide ---`
pattern the file already uses, so `test-supervise.sh` can pull it out and the test
cannot drift from what ships.

```sh
# --- escalate ---
# The supervisor's cures assume the fault is in the process. Some faults are not:
# an exhausted ION carveout is leaked in the kernel modules and no userspace
# action frees it, so restarting is a guaranteed-failure loop - 23 attempts over
# 22 minutes, observed 2026-08-04. A reboot is the only cure.
#
# The guard against turning that into a reboot loop is uptime, not a counter.
# A leak that refills faster than the floor is a leak a reboot cannot cure, so
# this refuses to try: after one reboot the board comes up, the loop resumes at
# low uptime, and no second reboot happens. The board stays reachable over ssh
# for a person to work on, which is what happens today anyway.
#
# Every input is passed in. Nothing here reads a clock, a file or a process.
should_reboot() {
    verdict=$1; short_runs=$2; failed_cures=$3; up=$4

    if [ "$up" -lt "${REBOOT_FLOOR:-600}" ]; then
        echo no
        return
    fi

    case "$verdict" in
        restart)
            if [ "$short_runs" -ge "${CRASH_LOOP_N:-5}" ]; then
                echo yes
                return
            fi
            ;;
        hung)
            if [ "$failed_cures" -ge "${HANG_CURES_K:-2}" ]; then
                echo yes
                return
            fi
            ;;
    esac

    echo no
}
# --- end escalate ---
```

`stopped` and `healthy` fall through to `no`. A deliberate stop is never escalated:
the operator asked for it.

### How `watch_loop` feeds it

Two counters are added to `watch_loop`, both initialised to 0 beside `delay`:
`short_runs` and `failed_cures`. A third, `cures`, counts cures attempted.

The `healthy` branch resets all three. A server that comes back and serves has
cleared the fault, whatever it was.

The `restart` branch already computes `ran`:

```sh
restart)
    ran=$(( $(now) - started ))

    if [ "$ran" -lt "${SHORT_RUN:-30}" ]; then
        short_runs=$(( short_runs + 1 ))
    else
        short_runs=0
    fi

    log "NanoKVM-Server is gone after ${ran}s, restarting in ${delay}s"

    if [ "$(should_reboot restart "$short_runs" 0 "$(now)")" = yes ]; then
        escalate "crash loop: $short_runs runs shorter than ${SHORT_RUN:-30}s"
    fi

    ... unchanged from here ...
```

The `hung` branch counts cures that did not restore service. A hung verdict that
arrives after a cure proves that cure failed:

```sh
hung)
    stuck=$(unhealthy_for)
    log "NanoKVM-Server is up but has not answered for ${stuck}s, killing and restarting it"

    [ "$cures" -gt 0 ] && failed_cures=$(( failed_cures + 1 ))

    if cure_hung; then
        cures=$(( cures + 1 ))
    else
        # killall -9 cannot clear uninterruptible sleep. No later cure does
        # better, so this counts as having exhausted them.
        log "the process did not leave after SIGKILL"
        failed_cures=${HANG_CURES_K:-2}
    fi

    if [ "$(should_reboot hung 0 "$failed_cures" "$(now)")" = yes ]; then
        escalate "hung: $failed_cures cures did not restore service"
    fi

    sleep 30
    LAST_OK=$(now)
    started=$(now)
    ;;
```

`cure_hung` gains a return value — `wait_gone`'s — which it currently discards:

```sh
cure_hung() {
    force_kill
    wait_gone || { full_restart; return 1; }
    full_restart
}
```

The restart still happens either way. A process that will not die is reported, not
abandoned.

### Escalating

```sh
escalate() {
    if [ "${SUPERVISE_NO_REBOOT:-0}" = 1 ]; then
        log "would reboot ($1), but SUPERVISE_NO_REBOOT is set"
        return 0
    fi

    log "rebooting: $1"
    capture_bounded "$1"
    sync
    reboot

    # reboot returns immediately. Stop deciding while the board goes down, or
    # the loop keeps counting against a system that is already leaving.
    sleep 120
}
```

### Configuration

Every threshold takes an environment override, matching the file's existing style
exactly: a short internal name assigned from a `SUPERVISE_*` variable at the top
of the file, with the default repeated at the point of use. `HANG_AFTER` already
works this way. Repeating the default is deliberate — a test that extracts a
block must measure the value that ships, and a block that depends on an
assignment made a hundred lines above cannot be extracted.

```sh
REBOOT_FLOOR=${SUPERVISE_REBOOT_FLOOR:-600}
SHORT_RUN=${SUPERVISE_SHORT_RUN:-30}
CRASH_LOOP_N=${SUPERVISE_CRASH_LOOP_N:-5}
HANG_CURES_K=${SUPERVISE_HANG_CURES:-2}
```

| Variable | Default | Meaning |
| -------- | ------- | ------- |
| `SUPERVISE_REBOOT_FLOOR` | 600 | Seconds of uptime below which the board never reboots |
| `SUPERVISE_SHORT_RUN` | 30 | A run shorter than this counts toward a crash loop |
| `SUPERVISE_CRASH_LOOP_N` | 5 | Consecutive short runs that trigger a reboot |
| `SUPERVISE_HANG_CURES` | 2 | Failed cures that trigger a reboot |
| `SUPERVISE_NO_REBOOT` | 0 | Set to 1 to log the decision and not act on it |

`SUPERVISE_NO_REBOOT` exists for the hardware test and for a board an operator
wants to leave in its failed state for investigation.

### Evidence

`/tmp` does not survive a reboot, and `dmesg` holds about eight crashes and rolls
within ten minutes. Evidence not captured before the reboot is gone. That is the
whole lesson of `/data/kvm-diag/crash-20260804/`.

```sh
capture_evidence() {   # $1 = reason
    dir="/data/kvm-diag/reboot-$(date '+%Y%m%d-%H%M%S')"
    mkdir -p "$dir" || return 0

    echo "$1"                                                > "$dir/reason"
    dmesg                                                    > "$dir/dmesg"         2>/dev/null
    tail -c 65536 "$SERVER_LOG"                              > "$dir/server.log"    2>/dev/null
    tail -c 16384 "$LOG"                                     > "$dir/supervise.log" 2>/dev/null
    cat /proc/uptime /proc/meminfo                           > "$dir/proc"          2>/dev/null
    cat /sys/kernel/debug/ion/cvi_carveout_heap_dump/summary > "$dir/ion"           2>/dev/null

    prune_reboot_dirs
    sync
}
```

Two rules constrain this, and both come from measured failures on this board.

**Never read `/proc/cvitek/vb`.** It blocks forever in uninterruptible sleep and
the reader cannot be killed. A capture that wedges there means the board never
reboots, and the guard becomes the fault.

**The capture must not be able to block the reboot.** It runs detached with a
bounded wait. Evidence is worth having; uptime outranks it.

```sh
capture_bounded() {
    capture_evidence "$1" &
    pid=$!

    i=0
    while [ "$i" -lt 10 ]; do
        kill -0 "$pid" 2>/dev/null || return 0
        i=$(( i + 1 ))
        sleep 1
    done

    kill -9 "$pid" 2>/dev/null
    return 0
}
```

Three reboot directories are kept. `/data` is on the SD card, and a board that
reboots daily must not fill it. busybox `head` is not assumed to accept a negative
count, so the count is computed:

```sh
prune_reboot_dirs() {
    cd /data/kvm-diag 2>/dev/null || return 0

    n=$(ls -d reboot-* 2>/dev/null | wc -l)
    [ "$n" -le 3 ] && return 0

    ls -d reboot-* 2>/dev/null | sort | head -n $(( n - 3 )) | while read -r d
    do
        rm -rf "$d"
    done
}
```

## Failure modes

| Failure | Effect | Handling |
| ------- | ------ | -------- |
| Floor logic wrong | Reboot loop, board unreachable, card must come out | The floor case is a mandatory test, and hardware test 2 proves it on the device |
| `capture_evidence` hangs | Board never reboots | `capture_bounded` kills it after 10s and reboots anyway |
| `/data` not mounted | No evidence | `mkdir -p` fails, `capture_evidence` returns 0, the reboot proceeds |
| `date` unavailable | No evidence directory | Same path as above; the reboot proceeds |
| Reboot lands on a slot without this script | No supervision on that slot | `S00awatchdog` is the layer below: a boot with no doors open rolls the slot back |
| Fault returns immediately after the reboot | No second reboot | By design. The board stays up and reachable at low uptime |
| Operator stops the server | Nothing | `stopped` never escalates |

## Testing

### Unit

`test-supervise.sh` already extracts `decide`, `backoff` and `cure` with `sed` and
refuses to run when an extraction comes back empty. `escalate` is added the same
way. Cases are table-driven, in the style of `decide_case`:

| verdict | short_runs | failed_cures | uptime | want |
| ------- | ---------- | ------------ | ------ | ---- |
| restart | 5 | 0 | 3600 | yes |
| restart | 5 | 0 | 599 | no |
| restart | 9 | 0 | 0 | no |
| restart | 4 | 0 | 3600 | no |
| restart | 0 | 0 | 3600 | no |
| hung | 0 | 2 | 3600 | yes |
| hung | 0 | 2 | 599 | no |
| hung | 0 | 1 | 3600 | no |
| stopped | 99 | 99 | 3600 | no |
| healthy | 99 | 99 | 3600 | no |

The counter transitions are tested separately, extracted from `watch_loop`:
a run of 29s increments `short_runs`, a run of 30s resets it to 0, and a
`healthy` verdict resets both counters and `cures`.

### Mutation

Every threshold and every comparison operator in the shipped `escalate` block is
flipped in turn, and each mutation must break at least one case. This is not
optional. Every task on the preceding branch shipped at least one assertion that
could not fail, and every one was caught only in review.

The floor case is the most important test in the file. It is the single check
standing between this feature and a bricked board.

### Shell

The suite runs on busybox `ash` on the device, not only on the workstation. This
is already the convention for `test-watchdog.sh`, and `ash` is not the shell these
tests are written in.

### Hardware

Run on the device, with the real binary saved first so the board can be restored.

1. **The escalation fires.** Stage a stub at `/tmp/server/NanoKVM-Server` that
   exits immediately. Watch `short_runs` climb in `/data/supervise.log`, and watch
   the reboot happen. Confirm `/data/kvm-diag/reboot-*` holds the evidence, and
   that `dmesg` and the ION summary are in it.
2. **The floor blocks it.** Repeat immediately after boot, inside the 10-minute
   floor. The board must not reboot. This proves the safety property; it is not
   assumed.
3. **`SUPERVISE_NO_REBOOT=1` disables it.** The log records the decision and the
   board stays up.
4. **A healthy board is untouched.** Leave the supervisor running normally for at
   least one hour with the real binary. No reboot, no new log lines beyond what it
   writes today.

## Deployment and rollback

Install to `/etc/init.d/S98supervise`, mode 755, LF line endings. Then
`/etc/init.d/S98supervise restart`.

Rollback is putting the previous file back and restarting the supervisor. It takes
about ten seconds and needs no reboot.

`S00awatchdog` sits underneath as a second net. The two do not conflict: different
layer, different trigger. This script acts on a server that will not stay up; the
watchdog acts on a boot that opens no doors at all.
