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

### Two guards stand against a reboot loop, and they do different jobs

A supervisor that reboots can turn one dead server into an unreachable board.
Today's failure at least answers SSH forever; a reboot loop does not, and the card
has to come out.

**The latch prevents a cycle.** `should_reboot` refuses unless the probe has
answered at least once since this boot. The latch requires a positive answer from
the health probe. `serving` fails open by design — a probe that cannot run must
never kill a working KVM — so it reports three states rather than two: the server
answered, the server did not answer, and the probe could not run. Only the first
sets the latch. A latch derived from "the probe did not say no" records a missing
`curl` as an answer, and a board without `curl` then gets the reboot cycle this
latch exists to prevent. Everything that decides whether to act still treats "the
probe could not run" as serving, so no restart behaviour depends on `curl` being
present. **Without `curl` the escalation is inert by design**: the latch never
sets, no reboot is possible, and the supervisor behaves as it did before it could
reboot at all. Hardware acceptance stops if `curl` is missing.

A reboot cures a board that worked and
then broke. It is never a cure for a server that has not answered once, and this
board carries several faults with exactly that shape: `proto: https` with an
unreadable certificate panics `server/main.go` on every start, a build that
skipped `patchelf` will not start, and `S95nanokvm` warns that a copy which runs
out of space leaves a truncated binary and starts it anyway. All of them leave
`/tmp/server/NanoKVM-Server` staged and executable, so the verdict is `restart`
rather than `stopped`, and none of them changes across a reboot.

**The floor sets the period.** A floor on `/proc/uptime` alone cannot prevent a
cycle: the counters do not survive a reboot, so a fault present from boot rebuilds
them and escalates again as soon as uptime crosses the floor — every ten and a
half minutes, for as long as the board has power. What the floor does is keep a
board that serves briefly after each reboot from cycling fast, and it follows the
physics: a leak that refills faster than the floor is a leak a reboot cannot cure,
so the script refuses to try.

The board reboots only if `/proc/uptime` is at or above `REBOOT_FLOOR`. Exactly
600 reboots; the comparison is `-lt` and the suite pins both directions.

Both guards fail closed. A value the comparisons cannot use — an empty `up`
because `cut` could not fork, a typo in `SUPERVISE_REBOOT_FLOOR` — means `no`.
`[ "" -lt 600 ]` is an error rather than a false comparison, so a guard that does
not validate its inputs skips itself exactly when the system is least healthy.

Neither guard needs persistent state, which keeps the script's existing property:

> Telling a crash from a deliberate stop needs no new state.

`/proc/uptime` is already read by `now()`, and the latch is a shell variable that
lives as long as the loop does.

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

`ran` does not measure how long a run lasted. `watch_loop` resets `started` on
every `healthy` poll, so `ran` is the time since the last observation and is about
`INTERVAL` in every case that matters. `SHORT_RUN` therefore never discriminates
inside the real loop, and the margin has to be derived from the poll and the
backoff rather than from the threshold.

The slowest crash loop is a server that lives just under `HANG_AFTER` each time
without ever answering. Every poll in that window reports `healthy` and resets
`started`, so the run still counts as short when the process is finally gone. Four
full cycles separate the first short run from the fifth, and each costs about 55
seconds of process life plus one poll plus the growing backoff of 5, 10, 20 and 40
seconds: 65 + 70 + 80 + 100, about 310 seconds. Boot takes about 20 seconds, so
the worst case reaches the escalation at roughly 5.5 minutes of uptime.

A floor of 10 minutes blocks that case with about 2× margin. The fault this
feature exists for kills the server in under a second, so `ran` quantises to the
5-second poll and the fifth short run arrives about 100 seconds in — blocked with
about six times the margin. Both are blocked firmly, not narrowly.

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
# The latch is what prevents a reboot cycle, and the floor is what sets its
# period. Both are described above.
#
# Every input is passed in. Nothing here reads a clock, a file or a process.
should_reboot() {
    verdict=$1; short_runs=$2; failed_cures=$3; up=$4; served_ever=$5

    if [ "$served_ever" != yes ]; then
        echo no
        return
    fi

    # Fail closed. `[ "" -lt 600 ]` is an error, so the `if` is false and the
    # floor skips itself on exactly the input that means something went wrong.
    for num in "$up" "$short_runs" "$failed_cures" \
               "${REBOOT_FLOOR:-600}" "${CRASH_LOOP_N:-5}" "${HANG_CURES_K:-2}"
    do
        case "$num" in ''|*[!0-9]*) echo no; return ;; esac
    done

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
`short_runs` and `failed_cures`. A third, `cures`, counts cures attempted. A
fourth variable, `served_ever`, is a latch rather than a counter: it starts at
`no`, a poll where `serving` reported the server answered sets it to `yes`, and
nothing ever clears it. `serving` reporting that it could not run does not set
it, although that poll still counts as serving everywhere else.

An answered probe clears all three counters. The verdict name alone is not enough
— `action` also reports `healthy` for a process that is up and not answering yet
— so a pure `should_clear` takes both the verdict and whether the probe answered.
`stopped` does not clear them: the supervisor's own cure removes `/tmp/server` for
about half a minute, which reports `stopped`, and clearing there wipes the cure
counters on exactly the slow SD card the fault arrives with. An operator who stops
the server and brings it back produces an answering `healthy` poll, which clears
them through the arm that is safe.

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
| Fault returns immediately after the reboot | No second reboot | The server has not answered since this boot, so the latch is unset and the decision is `no`. The board stays up and reachable |
| Fault lets the server answer once, then returns | At most one reboot per boot that served | The latch is set again, so the floor is what limits the rate. This is the case the floor exists for |
| `/proc/uptime` unreadable, or a threshold misspelt | No reboot | Every value the comparisons touch is validated, and anything that is not a plain non-negative integer means `no` |
| Operator stops the server | Nothing | `stopped` never escalates |
| `curl` missing | The whole escalation is inert | `serving` reports "could not probe", which counts as serving everywhere but the latch. No reboot is possible, and the supervisor behaves as it did before this feature. Hardware acceptance stops at step 1 |

## Testing

### Unit

`test-supervise.sh` already extracts `decide`, `backoff` and `cure` with `sed` and
refuses to run when an extraction comes back empty. `escalate` is added the same
way. Cases are table-driven, in the style of `decide_case`:

| verdict | short_runs | failed_cures | uptime | served_ever | want |
| ------- | ---------- | ------------ | ------ | ----------- | ---- |
| restart | 5 | 0 | 3600 | yes | yes |
| restart | 5 | 0 | 599 | yes | no |
| restart | 9 | 0 | 0 | yes | no |
| restart | 4 | 0 | 3600 | yes | no |
| restart | 0 | 0 | 3600 | yes | no |
| hung | 0 | 2 | 3600 | yes | yes |
| hung | 0 | 2 | 599 | yes | no |
| hung | 0 | 1 | 3600 | yes | no |
| stopped | 99 | 99 | 3600 | yes | no |
| healthy | 99 | 99 | 3600 | yes | no |
| restart | 9 | 0 | 3600 | no | no |
| hung | 0 | 9 | 3600 | no | no |
| hung | 0 | 2 | *(empty)* | yes | no |
| restart | 5 | 0 | `abc` | yes | no |

The two `served_ever` rows are the ones that would have caught the reboot cycle,
and the last two are the ones that would have caught the guard failing open.

The counter transitions are tested separately as pure functions: a run of 29s
increments `short_runs`, a run of 30s resets it to 0, an answered `healthy` poll
clears the counters, and every other verdict — `stopped` included — leaves them
alone.

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

The latch changes how a stub is staged. A stub put in place before the supervisor
starts produces no escalation at all, because the probe never answered. Start the
supervisor against the healthy server, let it answer one poll, and stage the stub
after that — which is also how the 2026-08-04 fault arrived.

1. **`curl` exists. This is a hard stop.** Without it `serving` reports "could
   not probe" on every poll, `action` can never return `hung`, and the latch
   never sets — so the whole escalation is inert. Do not deploy this feature to
   a board without `curl`; there is nothing for the acceptance run to accept.
2. **The hang path runs end to end.** Point `SUPERVISE_URL` at a port nothing
   listens on, against the real healthy server. This is the only test that
   reaches `should_clear` across the real `S95nanokvm` re-stage window.
3. **The escalation fires, with the shipped defaults.** Stage the stub while the
   supervisor runs and the server has answered. Watch `short_runs` climb in
   `/data/supervise.log`, and watch the reboot happen. Confirm
   `/data/kvm-diag/reboot-*` holds the evidence, and that `dmesg` and the ION
   summary are in it.
4. **The floor blocks the same run.** Repeat inside the 10-minute floor with
   `SUPERVISE_NO_REBOOT=1`, changing nothing but the floor. The `would reboot`
   line from the zero-floor control is the positive control, and its absence here
   is the proof. This is the safety property; it is not assumed.
5. **`SUPERVISE_NO_REBOOT=1` disables it.** The log records the decision and the
   board stays up.
6. **A healthy board is untouched.** Leave the supervisor running normally for at
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
