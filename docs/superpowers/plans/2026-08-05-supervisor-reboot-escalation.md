# Supervisor Reboot Escalation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give `S98supervise` a second verdict, so a board whose server cannot be saved by restarting reboots instead of looping forever — without ever turning that into a reboot loop.

**Architecture:** Every decision becomes a pure shell function inside a marker-delimited block that the test suite extracts with `sed`, so the test measures what ships. `should_reboot` decides, `next_short_runs` and `next_failed_cures` count, and `escalate` acts. `watch_loop` is wired to them last, after every function it calls exists.

**Tech Stack:** POSIX shell (busybox `ash` on the device), `sed`-based block extraction, table-driven shell tests, a mutation harness in the style of `tools/slots/test-mutation.sh`.

**Spec:** `docs/superpowers/specs/2026-08-05-supervisor-reboot-escalation-design.md`

## Global Constraints

- **POSIX shell only.** The device runs busybox `ash`. No `[[`, no arrays, no `local`, no `function` keyword, no process substitution, no `${var^^}`. Every test must also run under `ash`, not only under the workstation shell.
- **busybox `head` is not assumed to accept a negative count.** Compute the count and pass a positive one.
- **LF line endings** on every file under `tools/`. `tools/test-line-endings.sh` enforces this.
- **`/etc/init.d/S98supervise` is the only install target.** This script lives in `tools/service/` and is not part of `kvmapp/`, so the rule about installing an init script to both `/etc/init.d` and `/kvmapp/system/init.d` does not apply to it.
- **Never read `/proc/cvitek/vb`.** It blocks forever in uninterruptible sleep and the reader cannot be killed. A capture that wedges there means the board never reboots.
- **Do not write runtime state under `/kvmapp`.** Evidence goes to `/data/kvm-diag/`.
- **Threshold style, copied from the existing file:** a short internal name assigned from a `SUPERVISE_*` variable at the top, with the default repeated at the point of use. `HANG_AFTER=${SUPERVISE_HANG_AFTER:-60}` is the model. The repetition is deliberate: a block that depends on an assignment a hundred lines above cannot be extracted and tested on its own.
- **Exact default values:** `REBOOT_FLOOR` 600, `SHORT_RUN` 30, `CRASH_LOOP_N` 5, `HANG_CURES_K` 2, `NO_REBOOT` 0.
- **Marker lines sit at column 0** and are matched exactly by `sed`: `# --- name ---` and `# --- end name ---`.
- **Every assertion must be able to fail.** The mutation harness is the proof, not an opinion. An assertion that cannot fail is a defect, and reviewers should treat it as one.
- **Comments explain why, not what.** The existing file is written that way; match it.

## File Structure

| File | Responsibility |
| ---- | -------------- |
| `tools/service/S98supervise` | The supervisor. Gains the `escalate`, `count` and `act` blocks, four threshold assignments, three counters in `watch_loop`, and a return value on `cure_hung`. |
| `tools/service/test-supervise.sh` | Extracts each block and exercises it. Gains escalation, counter, action and wiring cases. |
| `tools/service/test-supervise-mutation.sh` | New. Mutates the shipped script and asserts the suite catches every mutation. |
| `tools/README.md` | Documents the escalation, the thresholds, the evidence layout and the hardware procedure. |

---

### Task 1: The escalation decision

**Files:**
- Modify: `tools/service/S98supervise` (threshold assignments after `PIDFILE=/tmp/supervise.pid`; new block after the line `# --- end decide ---`)
- Test: `tools/service/test-supervise.sh`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: `should_reboot <verdict> <short_runs> <failed_cures> <uptime_seconds>` — echoes exactly `yes` or `no` on stdout, one line, and returns 0. `<verdict>` is one of the four strings `action` already emits: `healthy`, `hung`, `stopped`, `restart`. Also produces the shell variables `REBOOT_FLOOR`, `SHORT_RUN`, `CRASH_LOOP_N`, `HANG_CURES_K`, `NO_REBOOT`.

- [ ] **Step 1: Write the failing test**

Add to `tools/service/test-supervise.sh`, immediately after the three existing `sed` extractions near the top of the file (the ones producing `decide.sh`, `backoff.sh` and `cure.sh`):

```sh
sed -n '/^# --- escalate ---$/,/^# --- end escalate ---$/p' "$SV" > "$WORK/escalate.sh"
[ -s "$WORK/escalate.sh" ] || { echo "could not extract the escalate block"; exit 1; }
```

Then add this section immediately before the final `echo "===== the script still parses ====="` section:

```sh
echo
echo "===== when restarting cannot work, reboot ====="
# S98supervise restarts and never reboots, which is right for a hung server and
# wrong for an exhausted ION carveout: the allocation is leaked inside the kernel
# modules, no userspace action frees it, and the server dies again in under a
# second. On 2026-08-04 that produced 23 restarts over 22 minutes into a
# guaranteed failure, and it would have continued indefinitely.
escalate_case() {
    desc="$1"; verdict="$2"; short="$3"; cures="$4"; up="$5"; want="$6"
    got=$(WORK="$WORK" sh -c ". \"\$WORK/escalate.sh\"; should_reboot $verdict $short $cures $up")
    [ "$got" = "$want" ] && note "$desc -> $got" OK || note "$desc -> $got, want $want" FAIL
}

#              desc                                            verdict short cures up   want
escalate_case "five short runs on a board that has been up"    restart 5  0  3600 yes
escalate_case "four short runs is not a loop yet"              restart 4  0  3600 no
escalate_case "no short runs at all"                           restart 0  0  3600 no
escalate_case "two failed cures on a board that has been up"   hung    0  2  3600 yes
escalate_case "one failed cure is not enough"                  hung    0  1  3600 no

# A deliberate stop is the operator's intent, and a healthy server has nothing
# wrong with it. Neither is ever a reason to take the KVM away from someone.
escalate_case "a deliberate stop is never escalated"           stopped 99 99 3600 no
escalate_case "a healthy server is never escalated"            healthy 99 99 3600 no

echo
echo "  --- the floor: the one check between this and a board that must be opened"
# A board that crash-loops out of boot reaches the escalation at roughly five
# minutes of uptime: 135s of backoff plus five runs of at most 30s, on top of a
# 20s boot. The floor sits at ten minutes, so that case is blocked with about
# twice the margin it needs - firmly, not narrowly.
#
# The consequence is deliberate. After one reboot the fault either goes away, or
# it returns at low uptime and no second reboot happens: the board stays up and
# reachable over ssh for a person to work on, which is what happens today anyway.
# A leak that refills faster than the floor is a leak a reboot cannot cure.
escalate_case "crash loop one second under the floor"          restart 5  0  599  no
escalate_case "crash loop straight out of boot"                restart 9  0  0    no
escalate_case "hang one second under the floor"                hung    0  2  599  no
escalate_case "hang straight out of boot"                      hung    0  9  0    no
escalate_case "crash loop exactly at the floor"                restart 5  0  600  yes
escalate_case "hang exactly at the floor"                      hung    0  2  600  yes
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `sh tools/service/test-supervise.sh`
Expected: FAIL — the run stops with `could not extract the escalate block`, because no such block exists yet.

- [ ] **Step 3: Add the threshold assignments**

In `tools/service/S98supervise`, immediately after the line `PIDFILE=/tmp/supervise.pid`, add:

```sh
# Escalation thresholds. Each takes an environment override in the same style as
# INTERVAL and HANG_AFTER above, and each default is repeated at the point of use
# so an extracted block measures the value that ships.
REBOOT_FLOOR=${SUPERVISE_REBOOT_FLOOR:-600}
SHORT_RUN=${SUPERVISE_SHORT_RUN:-30}
CRASH_LOOP_N=${SUPERVISE_CRASH_LOOP_N:-5}
HANG_CURES_K=${SUPERVISE_HANG_CURES:-2}
NO_REBOOT=${SUPERVISE_NO_REBOOT:-0}
```

Extend the `# Environment:` comment block at the top of the file with:

```sh
#   SUPERVISE_REBOOT_FLOOR  uptime below which it never reboots  (default 600)
#   SUPERVISE_SHORT_RUN     a run shorter than this counts       (default 30)
#   SUPERVISE_CRASH_LOOP_N  short runs that trigger a reboot     (default 5)
#   SUPERVISE_HANG_CURES    failed cures that trigger a reboot   (default 2)
#   SUPERVISE_NO_REBOOT     1 logs the decision and does not act (default 0)
```

- [ ] **Step 4: Add the escalation block**

In `tools/service/S98supervise`, immediately after the line `# --- end decide ---`, add:

```sh
# --- escalate ---
# The cures above assume the fault is in the process. Some faults are not: an
# exhausted ION carveout is leaked inside the kernel modules, and `rmmod
# soph_vpss` answers `Resource temporarily unavailable` with zero processes
# running. No userspace action frees it, so restarting is a guaranteed-failure
# loop. Only a reboot clears it, and that reboot is clean - the board came back
# in about ten seconds with HID, the ACM console and the UAC1 card all present.
#
# The trigger is blind. It counts failures and never reads dmesg to decide.
# The ring buffer holds about eight crashes and rolls within ten minutes, so a
# signature-gated guard would get weaker exactly as the fault got worse. A
# reboot is also the right cure for any cause a fresh boot clears - a leaked
# carveout, a wedged driver, a stuck VB pool - and naming one of them would
# narrow the fix to the instance that happened to be caught. The signature is
# still captured afterwards, as evidence rather than as a gate.
#
# The guard against a reboot loop is uptime, not a counter, because that follows
# the physics: a leak that refills faster than the floor is a leak a reboot
# cannot cure, so this refuses to try. It also keeps the property the rest of
# this script has - telling one state from another needs no new state on disk.
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

- [ ] **Step 5: Run the test to verify it passes**

Run: `sh tools/service/test-supervise.sh`
Expected: PASS — `===== all supervisor cases pass =====`, including all thirteen new escalation cases.

- [ ] **Step 6: Run it under busybox ash**

Run:

```shell
docker run --rm -v "$(pwd)/tools:/t:ro" busybox sh /t/service/test-supervise.sh \
    /t/service/S98supervise /t/../kvmapp/system/init.d/S95nanokvm
```

If the bind mount makes that second path awkward, copy the tree instead:

```shell
docker run --rm -v "$(pwd):/r:ro" busybox sh -c \
    'cp -r /r /w && sh /w/tools/service/test-supervise.sh'
```

Expected: PASS. `ash` is not the shell these tests were written in, and the suite has to hold there because that is where it runs on the device.

- [ ] **Step 7: Verify the script still parses and the line endings are right**

Run: `sh -n tools/service/S98supervise && sh tools/test-line-endings.sh`
Expected: both silent or reporting success. Do not use `git grep` or `git diff` to check line endings — `core.autocrlf` is `true` on this workstation and both report carriage returns in files whose stored blobs have none.

- [ ] **Step 8: Commit**

```bash
git add tools/service/S98supervise tools/service/test-supervise.sh
git commit -m "Decide when restarting the server cannot work

S98supervise restarts and never reboots. That is right for a hung server
and wrong for an exhausted ION carveout, where the allocation is leaked
inside the kernel modules and no userspace action frees it.

should_reboot adds the second verdict. It is blind to cause, because the
kernel ring buffer holds about eight crashes and rolls within ten minutes,
so a signature would get weaker exactly as the fault got worse.

The guard against a reboot loop is a floor on uptime rather than a counter.
A leak that refills faster than the floor is a leak a reboot cannot cure,
so this refuses to try and the board stays reachable over ssh."
```

---

### Task 2: Prove the escalation cases can fail

**Files:**
- Create: `tools/service/test-supervise-mutation.sh`

**Interfaces:**
- Consumes: `should_reboot` and the threshold defaults from Task 1; `tools/service/test-supervise.sh` as the suite under test.
- Produces: a runnable harness. No shell functions other tasks call.

Every task on the preceding branch shipped at least one assertion that could not fail, and every one was caught only in review. This harness makes that a mechanical check rather than a hope. It follows `tools/slots/test-mutation.sh`: mutate the shipped file, run the suite against the mutant, and fail if the suite still passes.

- [ ] **Step 1: Write the harness**

Create `tools/service/test-supervise-mutation.sh`:

```sh
#!/bin/sh
# Prove test-supervise.sh is not vacuous.
#
#   test-supervise-mutation.sh [path-to-S98supervise] [path-to-S95nanokvm]
#
# Each mutation below is a defect a person could plausibly write. The suite has
# to fail on every one of them. A suite that passes a mutated script is a suite
# that would have passed the defect.
#
# Mutations keep the script parsable on purpose. A mutant that fails `sh -n`
# would be caught for the wrong reason and would prove nothing about the cases.
HERE=$(cd "$(dirname "$0")" && pwd)
SV=${1:-$HERE/S98supervise}
S95=${2:-$HERE/../../kvmapp/system/init.d/S95nanokvm}
[ -f "$SV" ] || { echo "usage: test-supervise-mutation.sh <S98supervise>"; exit 1; }

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
fails=0

echo "== the shipped script"
if sh "$HERE/test-supervise.sh" "$SV" "$S95" > "$WORK/clean.out" 2>&1; then
    echo "   passes  <- expected"
else
    echo "   the shipped script does not pass its own suite:"
    sed 's/^/     /' "$WORK/clean.out"
    exit 1
fi

echo
mutate() {
    desc="$1"; expr="$2"
    sed "$expr" "$SV" > "$WORK/mutant"

    if ! sh -n "$WORK/mutant" 2>/dev/null; then
        echo "   BROKEN MUTATION (does not parse): $desc"
        fails=$((fails + 1))
        return 0
    fi

    if cmp -s "$SV" "$WORK/mutant"; then
        echo "   MUTATION CHANGED NOTHING: $desc"
        fails=$((fails + 1))
        return 0
    fi

    if sh "$HERE/test-supervise.sh" "$WORK/mutant" "$S95" > /dev/null 2>&1; then
        echo "   NOT CAUGHT: $desc"
        fails=$((fails + 1))
    else
        echo "   caught: $desc"
    fi
}

echo "== the floor, which is the only thing between this and a board that must be opened"
mutate "the floor is removed"            's/REBOOT_FLOOR:-600/REBOOT_FLOOR:-0/'
mutate "the floor comparison is inverted" 's/"\$up" -lt/"\$up" -ge/'
mutate "the floor is off by one"          's/"\$up" -lt/"\$up" -le/'

echo
echo "== the crash-loop threshold"
mutate "a single short run is a loop"     's/CRASH_LOOP_N:-5/CRASH_LOOP_N:-1/'
mutate "the threshold is unreachable"     's/CRASH_LOOP_N:-5/CRASH_LOOP_N:-99/'
mutate "the loop count is off by one"     's/"\$short_runs" -ge/"\$short_runs" -gt/'

echo
echo "== the hang threshold"
mutate "one failed cure is enough"        's/HANG_CURES_K:-2/HANG_CURES_K:-1/'
mutate "the cure count is off by one"     's/"\$failed_cures" -ge/"\$failed_cures" -gt/'

echo
if [ "$fails" -eq 0 ]; then
    echo "===== every mutation was caught ====="
else
    echo "===== $fails mutation(s) survived - those cases are vacuous ====="
    exit 1
fi
```

- [ ] **Step 2: Run it**

Run: `sh tools/service/test-supervise-mutation.sh`
Expected: PASS — `===== every mutation was caught =====`, with eight `caught:` lines.

If any mutation survives, the corresponding case in `test-supervise.sh` cannot fail. Fix the case, not the harness. In particular, `the floor is off by one` is caught only by the `crash loop exactly at the floor` and `hang exactly at the floor` cases from Task 1 — if it survives, those cases are missing or wrong.

- [ ] **Step 3: Run it under busybox ash**

Run:

```shell
docker run --rm -v "$(pwd):/r:ro" busybox sh -c \
    'cp -r /r /w && sh /w/tools/service/test-supervise-mutation.sh'
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add tools/service/test-supervise-mutation.sh
git commit -m "Prove the escalation cases can fail

Eight mutations, each a defect a person could plausibly write, and the
suite has to fail on every one. Three of them attack the uptime floor,
which is the only thing standing between this feature and a board that
has to be opened to recover.

Every task on the preceding branch shipped at least one assertion that
could not fail, and every one was caught only in review. This makes it
a mechanical check."
```

---

### Task 3: The counters

**Files:**
- Modify: `tools/service/S98supervise` (new block after the line `# --- end escalate ---`)
- Modify: `tools/service/test-supervise.sh`
- Modify: `tools/service/test-supervise-mutation.sh`

**Interfaces:**
- Consumes: `SHORT_RUN` from Task 1.
- Produces: `next_short_runs <seconds_the_run_lasted> <current_count>` and `next_failed_cures <cures_attempted> <current_count>`. Both echo one integer and return 0. They are the inputs `watch_loop` will feed to `should_reboot` in Task 5.

The spec said these transitions would be extracted from `watch_loop`. They are pure functions instead, mirroring the shape of `delay_after_run` — which already takes an observation and a current value and returns the next one. `watch_loop` cannot be extracted and tested; these can.

- [ ] **Step 1: Write the failing test**

Add to `tools/service/test-supervise.sh`, beside the other `sed` extractions:

```sh
sed -n '/^# --- count ---$/,/^# --- end count ---$/p' "$SV" > "$WORK/count.sh"
[ -s "$WORK/count.sh" ] || { echo "could not extract the count block"; exit 1; }
```

Add this section immediately after the escalation section from Task 1:

```sh
echo
echo "===== counting the runs that did not last ====="
# The threshold is 30s and not the one second the process actually survives.
# watch_loop sleeps INTERVAL between checks and resets `started` after each
# start, so `ran` is quantised to the poll and never reports below about five
# seconds. A five-second threshold would be an assertion that can never be true.
short_case() {
    desc="$1"; ran="$2"; current="$3"; want="$4"
    got=$(WORK="$WORK" sh -c ". \"\$WORK/count.sh\"; next_short_runs $ran $current")
    [ "$got" = "$want" ] && note "$desc -> $got" OK || note "$desc -> $got, want $want" FAIL
}

short_case "died as soon as it started"        1   0 1
short_case "died at the poll interval"         5   0 1
short_case "just under the threshold"          29  2 3
short_case "exactly at the threshold, so reset" 30 4 0
short_case "a run that lasted, so reset"       300 4 0

echo
echo "===== counting the cures that did not work ====="
# A hung verdict that arrives after a cure proves that cure did not work. The
# first hung verdict of a fault follows no cure at all, so it counts nothing -
# otherwise the very first hang would be one step from a reboot.
cures_case() {
    desc="$1"; cures="$2"; current="$3"; want="$4"
    got=$(WORK="$WORK" sh -c ". \"\$WORK/count.sh\"; next_failed_cures $cures $current")
    [ "$got" = "$want" ] && note "$desc -> $got" OK || note "$desc -> $got, want $want" FAIL
}

cures_case "the first hang, nothing tried yet"  0 0 0
cures_case "hung again after one cure"          1 0 1
cures_case "hung again after two cures"         2 1 2
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `sh tools/service/test-supervise.sh`
Expected: FAIL — the run stops with `could not extract the count block`.

- [ ] **Step 3: Add the count block**

In `tools/service/S98supervise`, immediately after the line `# --- end escalate ---`, add:

```sh
# --- count ---
# Two counters, kept as functions so the test measures what ships. They take the
# same shape as delay_after_run: the observation and the current value in, the
# next value out.
#
# SHORT_RUN is 30s and not the one second the process really survives. watch_loop
# sleeps INTERVAL between checks and resets `started` after each start, so `ran`
# is quantised to the poll and never reports below about five seconds - a
# five-second threshold would be an assertion that can never be true. Thirty sits
# clear above that quantisation and clear below the sixty seconds delay_after_run
# already uses to mean the fault was transient.
next_short_runs() {   # $1 = seconds the run lasted, $2 = current count
    if [ "$1" -lt "${SHORT_RUN:-30}" ]; then
        echo $(( $2 + 1 ))
    else
        echo 0
    fi
}

# A hung verdict that arrives after a cure proves that cure did not work. The
# first hung verdict of a fault follows no cure, so it counts nothing: otherwise
# the first hang a board ever had would be one step away from a reboot.
next_failed_cures() {   # $1 = cures attempted so far, $2 = current count
    if [ "$1" -gt 0 ]; then
        echo $(( $2 + 1 ))
    else
        echo "$2"
    fi
}
# --- end count ---
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `sh tools/service/test-supervise.sh`
Expected: PASS, including the eight new counter cases.

- [ ] **Step 5: Add the counter mutations**

In `tools/service/test-supervise-mutation.sh`, immediately before the final `echo` and summary block, add:

```sh
echo
echo "== the counters"
mutate "a short run is five seconds again" 's/SHORT_RUN:-30/SHORT_RUN:-5/'
mutate "the run length is off by one"      's/"\$1" -lt "\${SHORT_RUN/"\$1" -le "\${SHORT_RUN/'
mutate "a short run does not accumulate"   's/echo \$(( \$2 + 1 ))/echo 1/'
mutate "the first hang counts as a failure" 's/"\$1" -gt 0/"\$1" -ge 0/'
```

Note that `a short run does not accumulate` rewrites both `$(( $2 + 1 ))` occurrences, in `next_short_runs` and in `next_failed_cures`. That is intended: one mutation, two defects, and either one has to be caught.

- [ ] **Step 6: Run the mutation harness**

Run: `sh tools/service/test-supervise-mutation.sh`
Expected: PASS — twelve `caught:` lines now.

- [ ] **Step 7: Run both under busybox ash**

Run:

```shell
docker run --rm -v "$(pwd):/r:ro" busybox sh -c \
    'cp -r /r /w && sh /w/tools/service/test-supervise.sh \
     && sh /w/tools/service/test-supervise-mutation.sh'
```

Expected: both PASS.

- [ ] **Step 8: Commit**

```bash
git add tools/service/S98supervise tools/service/test-supervise.sh tools/service/test-supervise-mutation.sh
git commit -m "Count the runs that did not last and the cures that did not work

Both counters are pure functions shaped like delay_after_run, because
watch_loop cannot be extracted and tested and these can.

The short-run threshold is 30s rather than the one second the process
really survives. watch_loop polls every five seconds and resets `started`
after each start, so `ran` is quantised to the poll: a five-second
threshold would be an assertion that can never be true. Thirty sits above
that quantisation and below the sixty seconds delay_after_run already
treats as a run that lasted."
```

---

### Task 4: Rebooting, and the evidence that must survive it

**Files:**
- Modify: `tools/service/S98supervise` (new block after the line `# --- end cure ---`)
- Modify: `tools/service/test-supervise.sh`

**Interfaces:**
- Consumes: `NO_REBOOT` from Task 1; the existing `log`, `SERVER_LOG` and `LOG` variables.
- Produces: `escalate <reason_string>` — the single entry point `watch_loop` calls in Task 5. Also `capture_bounded <reason>`, `capture_evidence <reason>` and `prune_reboot_dirs`, all returning 0 unconditionally.

- [ ] **Step 1: Write the failing test**

Add to `tools/service/test-supervise.sh`, beside the other `sed` extractions:

```sh
sed -n '/^# --- act ---$/,/^# --- end act ---$/p' "$SV" > "$WORK/act.sh"
[ -s "$WORK/act.sh" ] || { echo "could not extract the act block"; exit 1; }
```

Add this section immediately after the counter section from Task 3:

```sh
echo
echo "===== rebooting, and refusing to ====="
# SUPERVISE_NO_REBOOT exists for the hardware test and for an operator who wants
# to leave a board in its failed state to investigate it.
got=$(WORK="$WORK" sh -c '
    SUPERVISE_NO_REBOOT=1
    NO_REBOOT=1
    log()             { :; }
    capture_bounded() { echo "captured"; }
    sync()            { :; }
    reboot()          { echo "REBOOTED"; }
    sleep()           { :; }
    . "$WORK/act.sh"
    escalate "test"
' | tr '\n' ' ')
[ "$got" = "" ] && note "SUPERVISE_NO_REBOOT=1 neither captures nor reboots" OK \
                || note "SUPERVISE_NO_REBOOT=1 did [$got], want nothing" FAIL

got=$(WORK="$WORK" sh -c '
    NO_REBOOT=0
    log()             { :; }
    capture_bounded() { echo "captured"; }
    sync()            { echo "synced"; }
    reboot()          { echo "REBOOTED"; }
    sleep()           { :; }
    . "$WORK/act.sh"
    escalate "test"
' | tr '\n' ' ')
[ "$got" = "captured synced REBOOTED " ] \
    && note "evidence is captured and synced before the reboot" OK \
    || note "order was [$got], want [captured synced REBOOTED ]" FAIL

echo
echo "  --- the capture must never be able to block the reboot"
# /tmp does not survive a reboot and dmesg rolls within ten minutes, so evidence
# not taken here is gone. But a capture that wedges means the board never
# reboots, and the guard becomes the fault. Uptime outranks evidence.
start=$(date +%s)
WORK="$WORK" sh -c '
    log()              { :; }
    capture_evidence() { sleep 60; }
    . "$WORK/act.sh"
    capture_bounded "test"
' > /dev/null 2>&1
elapsed=$(( $(date +%s) - start ))
[ "$elapsed" -lt 20 ] && note "a wedged capture is abandoned after ~10s (took ${elapsed}s)" OK \
                      || note "a wedged capture held the reboot for ${elapsed}s" FAIL

# A reader of /proc/cvitek/vb blocks forever in uninterruptible sleep and cannot
# be killed. Reading it here would mean the board never reboots at all.
if grep -q '/proc/cvitek/vb' "$SV"; then
    note "the capture reads /proc/cvitek/vb and would wedge the board" FAIL
else
    note "nothing in the script reads /proc/cvitek/vb" OK
fi

echo
echo "  --- /data is on the SD card, so the evidence is capped"
got=$(WORK="$WORK" sh -c '
    d=$(mktemp -d)
    mkdir -p "$d/kvm-diag"
    for s in 01 02 03 04 05; do mkdir -p "$d/kvm-diag/reboot-2026080$s-000000"; done
    . "$WORK/act.sh"
    cd "$d/kvm-diag" || exit 1
    prune_reboot_dirs
    ls -d "$d"/kvm-diag/reboot-* 2>/dev/null | wc -l
    rm -rf "$d"
')
[ "$got" = "3" ] && note "five reboot directories are pruned to 3" OK \
                 || note "pruning left $got directories, want 3" FAIL
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `sh tools/service/test-supervise.sh`
Expected: FAIL — the run stops with `could not extract the act block`.

- [ ] **Step 3: Add the act block**

In `tools/service/S98supervise`, immediately after the line `# --- end cure ---`, add:

```sh
# --- act ---
# /tmp does not survive a reboot, and the kernel ring buffer holds about eight
# crashes and rolls within ten minutes. Evidence not taken here is gone, and the
# 2026-08-04 incident was diagnosable only because a copy was made by hand
# before the board was restarted.
#
# prune_reboot_dirs takes the directory it is standing in, so capture_evidence
# calls it after cd-ing there. busybox head is not assumed to accept a negative
# count, so the count is computed and a positive one is passed.
prune_reboot_dirs() {
    n=$(ls -d reboot-* 2>/dev/null | wc -l)
    [ "$n" -le 3 ] && return 0

    ls -d reboot-* 2>/dev/null | sort | head -n $(( n - 3 )) | while read -r d
    do
        rm -rf "$d"
    done
    return 0
}

# Never read /proc/cvitek/vb here. It blocks forever in uninterruptible sleep
# and the reader cannot be killed, so a capture that touched it would mean the
# board never reboots at all.
capture_evidence() {   # $1 = reason
    dir="/data/kvm-diag/reboot-$(date '+%Y%m%d-%H%M%S')"
    mkdir -p "$dir" || return 0

    echo "$1"                                                > "$dir/reason"
    dmesg                                                    > "$dir/dmesg"         2>/dev/null
    tail -c 65536 "$SERVER_LOG"                              > "$dir/server.log"    2>/dev/null
    tail -c 16384 "$LOG"                                     > "$dir/supervise.log" 2>/dev/null
    cat /proc/uptime /proc/meminfo                           > "$dir/proc"          2>/dev/null
    cat /sys/kernel/debug/ion/cvi_carveout_heap_dump/summary > "$dir/ion"           2>/dev/null

    cd /data/kvm-diag 2>/dev/null && prune_reboot_dirs
    return 0
}

# Evidence is worth having. Uptime outranks it, so the capture runs detached
# with a bounded wait and is abandoned if it does not finish.
capture_bounded() {   # $1 = reason
    capture_evidence "$1" &
    pid=$!

    i=0
    while [ "$i" -lt 10 ]
    do
        kill -0 "$pid" 2>/dev/null || return 0
        i=$(( i + 1 ))
        sleep 1
    done

    log "the evidence capture did not finish, rebooting without it"
    kill -9 "$pid" 2>/dev/null
    return 0
}

escalate() {   # $1 = reason
    if [ "${NO_REBOOT:-0}" = 1 ]; then
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
# --- end act ---
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `sh tools/service/test-supervise.sh`
Expected: PASS, including the five new action cases. The wedged-capture case takes about ten seconds; that is the test doing its job.

- [ ] **Step 5: Run the mutation harness and the ash run**

Run: `sh tools/service/test-supervise-mutation.sh`
Expected: PASS, still twelve `caught:` lines — this task adds no new threshold to mutate.

Run:

```shell
docker run --rm -v "$(pwd):/r:ro" busybox sh -c \
    'cp -r /r /w && sh /w/tools/service/test-supervise.sh'
```

Expected: PASS. This one matters more than usual: `mktemp -d`, `kill -0`, `head -n`, and `ls -d` inside a pipe all behave slightly differently under busybox.

- [ ] **Step 6: Commit**

```bash
git add tools/service/S98supervise tools/service/test-supervise.sh
git commit -m "Reboot, and keep the reason the board did it

/tmp does not survive a reboot and dmesg rolls within ten minutes, so the
evidence has to be taken before the board goes down. It lands in
/data/kvm-diag/reboot-<stamp>/ and three of them are kept, because /data
is on the SD card.

The capture cannot be allowed to block the reboot: it runs detached and is
abandoned after ten seconds. Nothing in it reads /proc/cvitek/vb, which
blocks forever in uninterruptible sleep and would mean the board never
reboots at all.

SUPERVISE_NO_REBOOT logs the decision without acting on it."
```

---

### Task 5: Wire it into the loop

**Files:**
- Modify: `tools/service/S98supervise` (`cure_hung` inside the `cure` block, and `watch_loop`)
- Modify: `tools/service/test-supervise.sh`
- Modify: `tools/README.md`

**Interfaces:**
- Consumes: `should_reboot` (Task 1), `next_short_runs` and `next_failed_cures` (Task 3), `escalate` (Task 4).
- Produces: nothing later tasks call. This is the task that makes the feature reachable.

`cure_hung` was once defined and never called while every case in the suite stayed green — it was testable in isolation and unreachable from the loop. The existing suite has a wiring check for exactly that reason, and this task changes the line that check matches, so the check has to be updated in the same commit.

- [ ] **Step 1: Write the failing test**

In `tools/service/test-supervise.sh`, replace the existing wiring check:

```sh
grep -qE '^[[:space:]]+cure_hung$' "$SV" \
    && note "the hang branch actually calls cure_hung" OK \
    || note "cure_hung is defined but never reached" FAIL
```

with:

```sh
# cure_hung was once defined and never called while every case above stayed
# green: testable in isolation, unreachable from the loop. The same trap applies
# to everything this file extracts, so each new decision gets a wiring check too.
# Like the setsid check these assert a string, so the real evidence is the
# on-device test that crash-loops the server and watches the board come back.
grep -qE '^[[:space:]]+if cure_hung; then$' "$SV" \
    && note "the hang branch actually calls cure_hung" OK \
    || note "cure_hung is defined but never reached" FAIL
grep -qE 'should_reboot restart' "$SV" \
    && note "the restart branch actually asks should_reboot" OK \
    || note "should_reboot is defined but no crash loop reaches it" FAIL
grep -qE 'should_reboot hung' "$SV" \
    && note "the hang branch actually asks should_reboot" OK \
    || note "should_reboot is defined but no hang reaches it" FAIL
grep -qE '^[[:space:]]+escalate ' "$SV" \
    && note "something actually calls escalate" OK \
    || note "escalate is defined and never called" FAIL
grep -qE 'next_short_runs|next_failed_cures' "$SV" \
    && note "the counters are read by the loop" OK \
    || note "the counters are defined and never used" FAIL
```

Then add one case to the existing cure section, immediately after the check that asserts the `kill waited restart` order:

```sh
# killall -9 cannot clear uninterruptible sleep. A reader of /proc/cvitek/vb
# blocks in D state on this board, so the one hang that most needs a reboot is
# exactly the hang cure_hung cannot fix - and the answer used to be discarded.
got=$(WORK="$WORK" sh -c '
    force_kill()   { echo "kill"; }
    wait_gone()    { echo "waited"; return 1; }
    full_restart() { echo "restart"; }
    . "$WORK/cure.sh"
    cure_hung; echo "rc=$?"
' | tr '\n' ' ')
[ "$got" = "kill waited restart rc=1 " ] \
    && note "a process that will not die still restarts, and says so" OK \
    || note "order was [$got], want [kill waited restart rc=1 ]" FAIL
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `sh tools/service/test-supervise.sh`
Expected: FAIL, six cases — the four wiring checks for `should_reboot`, `escalate` and the counters, the changed `cure_hung` grep, and the new `rc=1` case.

- [ ] **Step 3: Give `cure_hung` a return value**

In `tools/service/S98supervise`, inside the `# --- cure ---` block, replace:

```sh
cure_hung() {
    force_kill
    wait_gone
    full_restart
}
```

with:

```sh
cure_hung() {
    force_kill
    wait_gone || { full_restart; return 1; }
    full_restart
}
```

Extend the comment above it with:

```sh
# wait_gone's answer used to be discarded. A process in uninterruptible sleep
# does not die from SIGKILL - a reader of /proc/cvitek/vb blocks in D state on
# this board - so the one hang that most needs a reboot is exactly the hang this
# cannot fix. The restart still happens either way; the caller is told.
```

- [ ] **Step 4: Wire the counters into `watch_loop`**

In `tools/service/S98supervise`, change the head of `watch_loop` from:

```sh
watch_loop() {
    delay=$(first_delay)
    started=$(now)
    LAST_OK=$(now)
```

to:

```sh
watch_loop() {
    delay=$(first_delay)
    started=$(now)
    LAST_OK=$(now)

    # short_runs and failed_cures are what should_reboot judges. cures counts
    # attempts, so a hung verdict can tell "the first hang" from "the cure did
    # not work". A server that comes back and serves clears all three: whatever
    # the fault was, it is over.
    short_runs=0
    failed_cures=0
    cures=0
```

Change the `healthy` branch from:

```sh
            healthy)
                started=$(now)
                ;;
```

to:

```sh
            healthy)
                started=$(now)
                short_runs=0
                failed_cures=0
                cures=0
                ;;
```

Change the `hung` branch from:

```sh
            hung)
                stuck=$(unhealthy_for)
                log "NanoKVM-Server is up but has not answered for ${stuck}s, killing and restarting it"
                cure_hung
                # Give it room to come up before judging again, or a slow start
                # would be read as another hang.
                sleep 30
                LAST_OK=$(now)
                started=$(now)
                ;;
```

to:

```sh
            hung)
                stuck=$(unhealthy_for)
                log "NanoKVM-Server is up but has not answered for ${stuck}s, killing and restarting it"

                failed_cures=$(next_failed_cures "$cures" "$failed_cures")

                if cure_hung; then
                    cures=$(( cures + 1 ))
                else
                    # SIGKILL cannot clear uninterruptible sleep, and no later
                    # cure does better. Go straight to the threshold.
                    log "the process did not leave after SIGKILL"
                    failed_cures=${HANG_CURES_K:-2}
                fi

                if [ "$(should_reboot hung 0 "$failed_cures" "$(now)")" = yes ]; then
                    escalate "hung: $failed_cures cures did not restore service"
                fi

                # Give it room to come up before judging again, or a slow start
                # would be read as another hang.
                sleep 30
                LAST_OK=$(now)
                started=$(now)
                ;;
```

Change the `restart` branch from:

```sh
            restart)
                ran=$(( $(now) - started ))
                log "NanoKVM-Server is gone after ${ran}s, restarting in ${delay}s"
                sleep "$delay"
```

to:

```sh
            restart)
                ran=$(( $(now) - started ))
                short_runs=$(next_short_runs "$ran" "$short_runs")
                log "NanoKVM-Server is gone after ${ran}s, restarting in ${delay}s"

                if [ "$(should_reboot restart "$short_runs" 0 "$(now)")" = yes ]; then
                    escalate "crash loop: $short_runs runs shorter than ${SHORT_RUN:-30}s"
                fi

                sleep "$delay"
```

Leave the rest of the `restart` branch unchanged.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `sh tools/service/test-supervise.sh && sh tools/service/test-supervise-mutation.sh`
Expected: both PASS.

Run: `sh -n tools/service/S98supervise`
Expected: silent.

- [ ] **Step 6: Run under busybox ash**

Run:

```shell
docker run --rm -v "$(pwd):/r:ro" busybox sh -c \
    'cp -r /r /w && sh /w/tools/service/test-supervise.sh \
     && sh /w/tools/service/test-supervise-mutation.sh'
```

Expected: both PASS.

- [ ] **Step 7: Document it**

In `tools/README.md`, find the section that describes `S98supervise` and add the following subsection at its end. If no such section exists, add this as a new `## The supervisor reboots a board restarting cannot save` section immediately after the supervisor's existing text.

````markdown
### When restarting cannot work

The supervisor restarts the server and does not reboot the board. That is right
for a hung server. It is wrong for an exhausted ION carveout: the allocation is
leaked inside the kernel modules, `rmmod soph_vpss` answers `Resource
temporarily unavailable` with zero processes running, and no userspace action
frees it. On 2026-08-04 the supervisor restarted a dead server 23 times over 22
minutes, and it would have continued indefinitely.

The supervisor now reboots the board in two cases:

| Case | Condition |
| ---- | --------- |
| Crash loop | 5 consecutive runs shorter than 30 seconds |
| Unrecoverable hang | 2 cures that did not restore service, or one process that did not die from SIGKILL |

The trigger is blind. It counts failures and never reads `dmesg` to decide,
because the ring buffer holds about eight crashes and rolls within ten minutes:
a signature would get weaker exactly as the fault got worse.

A floor on uptime prevents a reboot loop. The board reboots only if it has been
up for more than 10 minutes. A board that crash-loops out of boot reaches the
escalation at roughly five minutes, so that case is blocked with about twice the
margin it needs. The consequence is deliberate: if the fault returns immediately
after the reboot, no second reboot happens and the board stays reachable over
ssh. A leak that refills faster than the floor is a leak a reboot cannot cure.

| Variable | Default | Meaning |
| -------- | ------- | ------- |
| `SUPERVISE_REBOOT_FLOOR` | 600 | Seconds of uptime below which it never reboots |
| `SUPERVISE_SHORT_RUN` | 30 | A run shorter than this counts toward a crash loop |
| `SUPERVISE_CRASH_LOOP_N` | 5 | Consecutive short runs that trigger a reboot |
| `SUPERVISE_HANG_CURES` | 2 | Failed cures that trigger a reboot |
| `SUPERVISE_NO_REBOOT` | 0 | Set to 1 to log the decision and not act on it |

Before it reboots, the supervisor writes `/data/kvm-diag/reboot-<stamp>/` with
the reason, `dmesg`, the tail of the server log, the tail of its own log,
`/proc/uptime`, `/proc/meminfo` and the ION carveout summary. Three of these
directories are kept. The capture runs detached and is abandoned after ten
seconds, because a capture that wedges would mean the board never reboots.

Nothing in the capture reads `/proc/cvitek/vb`. That file blocks forever in
uninterruptible sleep and the reader cannot be killed.

```shell
sh tools/service/test-supervise.sh            # the decisions
sh tools/service/test-supervise-mutation.sh   # proves those cases can fail
```

Run both on the board as well as on a workstation. busybox `ash` is not the
shell the tests were written in.
````

- [ ] **Step 8: Check the line endings and commit**

Run: `sh tools/test-line-endings.sh`
Expected: success. Do not use `git grep` or `git diff` to check this — `core.autocrlf` is `true` on this workstation and both report carriage returns in files whose stored blobs have none.

```bash
git add tools/service/S98supervise tools/service/test-supervise.sh tools/README.md
git commit -m "Reach the escalation from the loop

Three counters in watch_loop, cleared whenever the server comes back and
serves. The restart branch counts runs that did not last; the hang branch
counts cures that did not restore service.

cure_hung now returns wait_gone's answer instead of discarding it. A
process in uninterruptible sleep does not die from SIGKILL, so the one
hang that most needs a reboot is exactly the hang cure_hung cannot fix.
That case goes straight to the threshold.

The suite's wiring check moves with the line it matches, and gains one
check per new decision. cure_hung was once defined and never called while
every case stayed green, and nothing about that trap is specific to it."
```

---

### Task 6: Hardware acceptance

**Files:**
- Modify: none. This task changes no source.

**Interfaces:**
- Consumes: the whole feature.
- Produces: evidence that it works on the device, recorded in the ledger.

Run this on the real board. The unit tests assert strings and pure functions; only the device shows that the loop reaches them, that `reboot` works from inside a detached `setsid` session, and — most importantly — that the floor and the latch hold.

Back up first. `/etc/init.d/S98supervise` and the real `/tmp/server/NanoKVM-Server` both have to come back.

**Two properties shape every step below, and both were review findings.**

The escalation cannot fire unless the probe has answered at least once since the
supervisor started. `served_ever` is a per-boot latch and the first thing
`should_reboot` checks, because a reboot cures a board that worked and then
broke and never cures a server that has not answered once. So a stub staged
*before* `start` produces no escalation at all: the correct injection is to start
the supervisor against the healthy server, let it answer one poll, and stage the
stub after that. That is also a truer reproduction of 2026-08-04, where the board
served for thirty hours first.

Truncating a running executable fails with `ETXTBSY`, so every stub is written
elsewhere and moved into place. `rename` over a busy binary is allowed.

**The fault injection.** Steps 7, 8 and 9 all use this, and each one is preceded
by a saved copy of the real binary:

```shell
ssh root@10.0.0.222 '
    printf "#!/bin/sh\nexit 1\n" > /tmp/stub && chmod 755 /tmp/stub
    mv /tmp/stub /tmp/server/NanoKVM-Server
    killall -9 NanoKVM-Server
'
```

**The restore between them.** The supervisor starts the real binary again within
its backoff delay, and the poll that answers is what sets the latch for the next
injection:

```shell
ssh root@10.0.0.222 '
    cp /data/NanoKVM-Server.real /tmp/stub && chmod 755 /tmp/stub
    mv /tmp/stub /tmp/server/NanoKVM-Server
'
sleep 30
ssh root@10.0.0.222 '/etc/init.d/S98supervise status'
```

Expected: `answering : yes` before any step stages the stub again.

- [ ] **Step 1: Check the device has what the feature needs**

```shell
ssh root@10.0.0.222 'command -v curl && curl --version | head -1'
```

Expected: a path and a version line.

`serving()` returns 0 when `curl` is missing — never reboot or restart a KVM
because a probe broke. The consequence is that on a board without `curl` every
poll reports the server as answering, `action` can never return `hung`, and the
whole hang half of this feature is inert. The crash half still works. If this
step finds no `curl`, record it and skip steps 5 and 6.

- [ ] **Step 2: Back up and install**

```shell
ssh root@10.0.0.222 'cp /etc/init.d/S98supervise /data/S98supervise.before'
scp tools/service/S98supervise root@10.0.0.222:/etc/init.d/S98supervise
ssh root@10.0.0.222 'chmod 755 /etc/init.d/S98supervise && sh -n /etc/init.d/S98supervise && echo parses'
```

Expected: `parses`.

- [ ] **Step 3: Run the suite on the device**

```shell
scp tools/service/test-supervise.sh tools/service/test-supervise-mutation.sh root@10.0.0.222:/tmp/
ssh root@10.0.0.222 'sh /tmp/test-supervise.sh /etc/init.d/S98supervise /etc/init.d/S95nanokvm'
ssh root@10.0.0.222 'sh /tmp/test-supervise-mutation.sh /etc/init.d/S98supervise /etc/init.d/S95nanokvm'
```

Expected: both PASS against the installed copy, under the device's own `ash`.

- [ ] **Step 4: Save the real binary**

```shell
ssh root@10.0.0.222 'cp /tmp/server/NanoKVM-Server /data/NanoKVM-Server.real && ls -l /data/NanoKVM-Server.real'
```

`/data` survives a reboot, and steps 7 to 9 each reboot the board. Nothing below
may run until this file exists.

- [ ] **Step 5: Drive the hang path end to end, with no stub and no reboot**

Nothing else in this plan produces a `hung` verdict on hardware, and the hang
path carries four things no unit test reaches: `should_clear` across the real
`S95nanokvm` re-stage window, the `cures` counter, `wait_gone`'s return value,
and the 36MB copy that makes the verdict read `stopped` for about half a minute
in the middle of the cure. Two of those have already produced defects.

It needs no stub. Point the probe at a port nothing listens on. `serving` then
always fails while `NanoKVM-Server` is up and healthy, so `action` returns `hung`
and the whole path runs — against a KVM that keeps answering on port 80 the
entire time.

```shell
ssh root@10.0.0.222 '
    /etc/init.d/S98supervise stop
    SUPERVISE_URL=http://127.0.0.1:1/ SUPERVISE_INTERVAL=5 SUPERVISE_NO_REBOOT=1 \
        /etc/init.d/S98supervise start
'
```

Each cure costs about 90 seconds — `HANG_AFTER` is 60 and the hang branch sleeps
30 afterwards. Wait about six minutes, then:

```shell
ssh root@10.0.0.222 'grep -E "has not answered|did not leave|would reboot" /data/supervise.log | tail -10'
```

Expected: three or more `killing and restarting it` lines whose `failed_cures=`
value climbs 0, 1, 2, 3, and **no** `would reboot` line. The climbing count is
the evidence: it can only climb if the counters survived the window where
`S95nanokvm` had removed `/tmp/server` and the verdict was `stopped`. The absent
`would reboot` line is the latch — this probe never answered, so no reboot is
available on this run at any uptime.

Then put the probe back:

```shell
ssh root@10.0.0.222 '/etc/init.d/S98supervise stop; /etc/init.d/S98supervise start; sleep 10; /etc/init.d/S98supervise status'
```

Expected: `answering : yes`.

- [ ] **Step 6: Make the hang path escalate, and then reboot**

The escalation needs one answered poll. A listener that answers exactly once
gives the latch and then leaves, so every later poll fails:

```shell
ssh root@10.0.0.222 'nc --help 2>&1 | grep -q -- "-l" && echo "nc can listen"'
```

If `nc` cannot listen, record this step as not run. Part of it — the counters and
the cure loop — is already proven by step 5, and the decision itself is covered
by the unit suite.

```shell
ssh root@10.0.0.222 '
    /etc/init.d/S98supervise stop
    (printf "HTTP/1.0 200 OK\r\nContent-Length: 0\r\n\r\n" | nc -l -p 8099 >/dev/null 2>&1 &)
    sleep 1
    SUPERVISE_URL=http://127.0.0.1:8099/ SUPERVISE_INTERVAL=5 SUPERVISE_NO_REBOOT=1 \
        /etc/init.d/S98supervise start
'
```

Wait about six minutes, then:

```shell
ssh root@10.0.0.222 'grep -E "has not answered|would reboot" /data/supervise.log | tail -6'
```

Expected: three `killing and restarting it` lines and then `would reboot (hung: 2
cures did not restore service), but SUPERVISE_NO_REBOOT is set`. The escalation
lands on the third hung verdict, because the first follows no cure at all.

Now run the same thing without the switch, and let the board reboot:

```shell
ssh root@10.0.0.222 '
    /etc/init.d/S98supervise stop
    (printf "HTTP/1.0 200 OK\r\nContent-Length: 0\r\n\r\n" | nc -l -p 8099 >/dev/null 2>&1 &)
    sleep 1
    SUPERVISE_URL=http://127.0.0.1:8099/ SUPERVISE_INTERVAL=5 /etc/init.d/S98supervise start
'
```

Expected: the board reboots within about six minutes. Poll until it answers, then
confirm on the new boot rather than on a sample taken before it went down:

```shell
ssh root@10.0.0.222 'cut -d. -f1 /proc/uptime; cat /data/kvm-diag/reboot-*/reason'
```

Expected: a small uptime, and a reason reading `hung: 2 cures did not restore
service`. Nothing was staged, so the board comes back healthy on its own.

- [ ] **Step 7: Prove the crash-loop escalation fires, with the shipped defaults**

No threshold override except the poll interval. A run that proves the feature
with `SUPERVISE_REBOOT_FLOOR=0` proves something that does not ship. The board's
uptime is well past 600 by now, so the floor is satisfied honestly.

```shell
ssh root@10.0.0.222 '
    /etc/init.d/S98supervise stop
    SUPERVISE_INTERVAL=2 /etc/init.d/S98supervise start
    sleep 10
    /etc/init.d/S98supervise status
'
```

Expected: `answering : yes` — this is the poll that sets the latch. Now stage the
stub with the fault injection above, and expect the board to reboot within about
three minutes. Poll until it answers again, then:

```shell
ssh root@10.0.0.222 'cut -d. -f1 /proc/uptime; ls -d /data/kvm-diag/reboot-*; \
    cat /data/kvm-diag/reboot-*/reason; ls /data/kvm-diag/reboot-*/'
```

Expected: a small uptime, a `reboot-*` directory, a `reason` reading `crash loop:
5 runs shorter than 30s`, and the files `dmesg`, `server.log`, `supervise.log`,
`proc`, `ion` inside it. Check `/proc/uptime` — a poll that returns quickly can be
answering from before the reboot.

`/tmp` is tmpfs and `S95nanokvm` copies the real binary back at boot, so the stub
is gone and the board comes up healthy. Steps 8 and 9 stage it again.

- [ ] **Step 8: Prove the floor blocks the same run**

**Run this inside ten minutes of the reboot in step 7.** It is step 9 with one
variable changed — the floor — and the two together are the only falsifiable form
of this test. Read the uptime first and abandon the step if it is already near
600; the point of the test is the uptime, not the elapsed wall clock.

`SUPERVISE_NO_REBOOT=1` is set here as well as in step 9, so the board cannot
reboot during the step that is meant to prove it does not.

```shell
ssh root@10.0.0.222 '
    cut -d. -f1 /proc/uptime
    /etc/init.d/S98supervise stop
    SUPERVISE_NO_REBOOT=1 SUPERVISE_INTERVAL=2 /etc/init.d/S98supervise start
    sleep 10
    /etc/init.d/S98supervise status
'
```

Expected: `answering : yes`. Stage the stub with the fault injection above, wait
about three minutes, then:

```shell
ssh root@10.0.0.222 'cut -d. -f1 /proc/uptime; tail -25 /data/supervise.log'
```

Three assertions, and the first two are the positive control that makes the third
mean anything:

1. The start line reads `(floor 600s, 5 short runs, 2 cures, no_reboot=1)`.
2. Repeated `NanoKVM-Server is gone after Ns (short_runs=N)` lines appear, and
   the count reaches 5 or more. A step that never reached the threshold has
   proven nothing about the floor.
3. **No** `would reboot` line and no `rebooting:` line, with `/proc/uptime` still
   under 600 at the end.

Record both uptime readings. Then restore the real binary as above.

- [ ] **Step 9: The positive control — the same run with the floor at zero**

Identical to step 8 in every respect except `SUPERVISE_REBOOT_FLOOR=0`. If this
step does not produce the line step 8 must not produce, step 8 proved nothing.

```shell
ssh root@10.0.0.222 '
    /etc/init.d/S98supervise stop
    SUPERVISE_NO_REBOOT=1 SUPERVISE_REBOOT_FLOOR=0 SUPERVISE_INTERVAL=2 \
        /etc/init.d/S98supervise start
    sleep 10
    /etc/init.d/S98supervise status
'
```

Expected: `answering : yes`. Stage the stub, wait about three minutes, then:

```shell
ssh root@10.0.0.222 'grep -E "supervisor started|would reboot" /data/supervise.log | tail -4; cut -d. -f1 /proc/uptime'
```

Expected: a start line reading `(floor 0s, 5 short runs, 2 cures, no_reboot=1)`,
followed by `would reboot (crash loop: 5 runs shorter than 30s), but
SUPERVISE_NO_REBOOT is set`. The uptime shows the board did not restart, and the
SSH session staying alive is itself the evidence.

- [ ] **Step 10: Restore the board**

```shell
ssh root@10.0.0.222 '
    /etc/init.d/S98supervise stop
    cp /data/NanoKVM-Server.real /tmp/server/NanoKVM-Server
    chmod 755 /tmp/server/NanoKVM-Server
    /etc/init.d/S95nanokvm restart
    sleep 20
    /etc/init.d/S98supervise start
    /etc/init.d/S98supervise status
'
```

Expected: `verdict : healthy`, `answering : yes`, `server : up`.

Confirm the running server is the real one, not a leftover:

```shell
ssh root@10.0.0.222 'ls -l /proc/$(pidof NanoKVM-Server)/exe'
```

Expected: it points at `/tmp/server/NanoKVM-Server`, and the web UI answers.

- [ ] **Step 11: Prove a healthy board is untouched**

Leave the supervisor running with the real binary for at least one hour, then:

```shell
ssh root@10.0.0.222 'cut -d. -f1 /proc/uptime; ls /data/kvm-diag/ ; tail -5 /data/supervise.log'
```

Expected: uptime over 3600, no new `reboot-*` directory, and no new log lines
beyond what the supervisor writes today. A guard that fires on a healthy board is
worse than no guard.

- [ ] **Step 12: Record the result**

Write the outcome of each of steps 1 to 11 into the plan's ledger, including the
`failed_cures` sequence from step 5 and both uptime readings from step 8. Then
remove the leftovers:

```shell
ssh root@10.0.0.222 'rm -f /tmp/test-supervise.sh /tmp/test-supervise-mutation.sh /data/NanoKVM-Server.real /tmp/stub'
```

Keep `/data/S98supervise.before` until the branch is merged. It is the rollback.

---

## Self-Review

**Spec coverage.** Every section of the spec maps to a task: the decision and the thresholds to Task 1; "Every assertion must be able to fail" to Task 2; the short-run reasoning and the counters to Task 3; evidence, bounding, pruning and the `/proc/cvitek/vb` prohibition to Task 4; the `watch_loop` integration, the `cure_hung` return value and the README to Task 5; all four hardware tests to Task 6. The spec's failure-mode table is covered by Task 4's bounded capture and `mkdir -p || return 0`, and by Task 1's floor cases.

**One deviation from the spec, deliberate.** The spec said the counter transitions would be "extracted from `watch_loop`". `watch_loop` is a `while` loop with side effects and cannot be extracted or driven. Task 3 makes them pure functions in their own block instead, shaped like the existing `delay_after_run`. Same intent, and testable.

**Placeholder scan.** No step says "add error handling" or "write tests for the above". Every code step carries the code. Every run step carries the command and the expected result.

**Name consistency.** `should_reboot`, `next_short_runs`, `next_failed_cures`, `escalate`, `capture_bounded`, `capture_evidence`, `prune_reboot_dirs` are spelled identically in the definitions (Tasks 1, 3, 4), the tests that call them (Tasks 1, 3, 4), the wiring checks (Task 5) and the call sites (Task 5). `REBOOT_FLOOR`, `SHORT_RUN`, `CRASH_LOOP_N`, `HANG_CURES_K` and `NO_REBOOT` are the internal names throughout; the `SUPERVISE_*` spellings appear only in the assignments, the header comment, the README table and the device commands.

**Ordering.** No task calls a function a later task defines. Task 5 is the only task that makes anything reachable, and by then everything it calls exists.
