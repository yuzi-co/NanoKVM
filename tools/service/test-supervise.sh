#!/bin/sh
# Exercise the supervisor's decisions, taken straight out of the script that
# ships so the test cannot drift from it.
#
#   test-supervise.sh [path-to-S98supervise] [path-to-S95nanokvm]
#
# Not destructive: nothing is started or killed. The probes are stubbed.
SV=${1:-$(dirname "$0")/S98supervise}
S95=${2:-$(dirname "$0")/../../kvmapp/system/init.d/S95nanokvm}
[ -f "$SV" ] || { echo "usage: test-supervise.sh <S98supervise>"; exit 1; }

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

sed -n '/^# --- decide ---$/,/^# --- end decide ---$/p'   "$SV" > "$WORK/decide.sh"
sed -n '/^# --- backoff ---$/,/^# --- end backoff ---$/p' "$SV" > "$WORK/backoff.sh"
sed -n '/^# --- cure ---$/,/^# --- end cure ---$/p'       "$SV" > "$WORK/cure.sh"
sed -n '/^# --- escalate ---$/,/^# --- end escalate ---$/p' "$SV" > "$WORK/escalate.sh"
sed -n '/^# --- count ---$/,/^# --- end count ---$/p' "$SV" > "$WORK/count.sh"
sed -n '/^# --- act ---$/,/^# --- end act ---$/p' "$SV" > "$WORK/act.sh"
sed -n '/^# --- ion ---$/,/^# --- end ion ---$/p' "$SV" > "$WORK/ion.sh"
[ -s "$WORK/decide.sh" ]  || { echo "could not extract the decide block"; exit 1; }
[ -s "$WORK/backoff.sh" ] || { echo "could not extract the backoff block"; exit 1; }
[ -s "$WORK/cure.sh" ]    || { echo "could not extract the cure block"; exit 1; }
[ -s "$WORK/escalate.sh" ] || { echo "could not extract the escalate block"; exit 1; }
[ -s "$WORK/count.sh" ] || { echo "could not extract the count block"; exit 1; }
[ -s "$WORK/act.sh" ] || { echo "could not extract the act block"; exit 1; }
[ -s "$WORK/ion.sh" ] || { echo "could not extract the ion block"; exit 1; }

fails=0
note() { printf '  %-60s %s\n' "$1" "$2"; [ "$2" = FAIL ] && fails=$((fails + 1)); return 0; }

echo "===== crashed, or stopped on purpose? ====="
# The distinction costs nothing to make: `S95nanokvm stop` removes /tmp/server
# after killing the process, so the binary's presence is the operator's intent.
# Without this the supervisor would fight every deliberate stop.
# serving has three answers, so the stub has three values. Anything that is not
# yes or no stands for "the probe could not run at all", which is what the
# shipped serving reports when curl is missing.
decide_case() {
    desc="$1"; binary="$2"; running="$3"; serving="$4"; unhealthy="$5"; want="$6"
    got=$(BIN="$binary" RUN="$running" SRV="$serving" UNW="$unhealthy" WORK="$WORK" sh -c '
        binary_present()  { [ "$BIN" = yes ]; }
        process_running() { [ "$RUN" = yes ]; }
        serving()         { case "$SRV" in yes) return 0 ;; no) return 1 ;; *) return 2 ;; esac }
        unhealthy_for()   { echo "$UNW"; }
        . "$WORK/decide.sh"
        action
    ')
    [ "$got" = "$want" ] && note "$desc -> $got" OK || note "$desc -> $got, want $want" FAIL
}

#           binary running serving unhealthy-for  want
decide_case "running and answering"          yes yes yes 0   healthy
decide_case "binary staged but no process"   yes no  no  0   restart
decide_case "stopped on purpose"             no  no  no  0   stopped
# Odd but real during a restart: the old process is still dying while /tmp/server
# has already been removed. Interfering there would race S95nanokvm.
decide_case "no binary, process still dying" no  yes no  0   healthy

echo
echo "  --- alive but not answering: a hang"
# The failure the supervisor could not see. A process that is up and not serving
# looks healthiest of all from outside, and pidof cannot tell the difference.
#
# A board reboot is the wrong response: it costs 17s, risks the boot path, and can
# land on a slot without any of this. Restarting the server is cheaper and safer,
# so the hardware watchdog is reserved for a kernel lockup that userspace cannot
# act on at all.
decide_case "just stopped answering, still inside grace" yes yes no 10  healthy
decide_case "not answering for a minute: hung"           yes yes no 60  hung
decide_case "not answering for a long time"              yes yes no 600 hung

# Never on ambiguity. A curl that cannot run, a probe that errors, a slow start
# under heavy IO - none of those are worth killing a working KVM for, so the
# grace period has to be generous and the default has to be inaction.
decide_case "answering again before the threshold"       yes yes yes 59  healthy

echo
echo "  --- a probe that cannot run is not evidence of anything"
# serving reports a third answer for "curl is missing, so nothing was measured".
# action must read that as serving, at any silence, forever. The alternative is
# that a board without curl reports hung on every poll, and the supervisor then
# kills and restarts a perfectly healthy KVM once a minute - which is worse than
# the reboot cycle this third answer exists to close.
decide_case "the probe cannot run, exactly at the threshold" yes yes unavailable 60   healthy
decide_case "the probe cannot run, hours of silence"         yes yes unavailable 9999 healthy

echo
echo "===== curing a hang means killing it first ====="
# S95nanokvm's restart uses killall, which is SIGTERM. A wedged process may never
# act on that, and while it lives it holds port 80 - so the replacement could not
# bind and the hang would survive its own cure.
got=$(WORK="$WORK" sh -c '
    force_kill()   { echo "kill"; }
    wait_gone()    { echo "waited"; }
    full_restart() { echo "restart"; }
    . "$WORK/cure.sh"
    cure_hung
' | tr '
' ' ')
[ "$got" = "kill waited restart " ]     && note "SIGKILL, wait for it to go, then the normal restart" OK     || note "order was [$got], want [kill waited restart ]" FAIL

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

echo
echo "===== the retry delay grows and is capped ====="
# A binary that can never start must not be retried in a tight loop: that burns
# the one core and writes a log line every pass. It must also never give up,
# because this is the device you reach for when nothing else answers.
got=$(WORK="$WORK" sh -c '. "$WORK/backoff.sh"; d=$(first_delay); i=0; while [ $i -lt 8 ]; do printf "%s " "$d"; d=$(next_delay "$d"); i=$((i+1)); done')
want="5 10 20 40 60 60 60 60 "
[ "$got" = "$want" ] && note "delay walks 5 10 20 40 then holds at 60" OK \
                     || note "got [$got] want [$want]" FAIL

got=$(WORK="$WORK" sh -c '. "$WORK/backoff.sh"; next_delay 0')
[ "$got" -gt 0 ] && note "a zero delay still advances (no tight loop)" OK \
                 || note "next_delay 0 = $got" FAIL

echo
echo "===== a run that lasted resets the delay ====="
# Otherwise a board that crashes once a day would creep to the cap and stay
# there, so the next real crash waits a minute for no reason.
reset_case() {
    desc="$1"; uptime="$2"; current="$3"; want="$4"
    got=$(WORK="$WORK" sh -c ". \"\$WORK/backoff.sh\"; delay_after_run $uptime $current")
    [ "$got" = "$want" ] && note "$desc -> $got" OK || note "$desc -> $got, want $want" FAIL
}

reset_case "ran 5 minutes then died, so start over"   300 60 5
reset_case "died again after 3 seconds, so keep backing off" 3 20 40
reset_case "died right at the threshold"              60 40 5

echo
echo "===== a restarted server still reports where libkvm fails ====="
# The supervisor restarts a crashed server itself, rather than through
# S95nanokvm, because a full restart copies 36MB back into tmpfs for nothing.
# That shortcut has to carry the redirection with it.
#
# libkvm reports a capture pipeline that does not start with printf, and that
# output is the only record of the failure. A server started with its output on
# /dev/null is a server nobody can debug, and a crash is when the record matters
# most. S99vidiag would go on reading a file that nothing writes to, and the
# file would still be there, so nothing would look wrong.
if grep -q '"\$SERVER_BIN" < /dev/null >> "\$SERVER_LOG" 2>&1 &' "$SV"; then
    note "the crash restart sends the server's output to the log" OK
else
    note "the crash restart discards the server's output" FAIL
fi

# One path, spelled in two scripts, drifts. S99vidiag reads one file, so a
# second spelling here means the collector follows a file nobody writes.
sv_log=$(sed -n 's/^SERVER_LOG=\(.*\)$/\1/p' "$SV")
s95_log=$(sed -n 's/^SERVER_LOG=\(.*\)$/\1/p' "$S95")
if [ -z "$sv_log" ]; then
    note "the supervisor never names the log" FAIL
elif [ "$sv_log" = "$s95_log" ]; then
    note "both scripts name $sv_log" OK
else
    note "the supervisor says $sv_log, S95nanokvm says $s95_log" FAIL
fi

# kvm_system drives the OLED. It prints nothing anybody reads, and pointing it
# at the server's log would mix two programs into one record.
if grep -q '"\$SYSTEM_BIN" < /dev/null > /dev/null 2>&1 &' "$SV"; then
    note "kvm_system keeps its output on /dev/null" OK
else
    note "kvm_system no longer starts the way it did" FAIL
fi

echo
echo "===== when restarting cannot work, reboot ====="
# S98supervise restarts and never reboots, which is right for a hung server and
# wrong for an exhausted ION carveout: the allocation is leaked inside the kernel
# modules, no userspace action frees it, and the server dies again in under a
# second. On 2026-08-04 that produced 23 restarts over 22 minutes into a
# guaranteed failure, and it would have continued indefinitely.
escalate_case() {
    desc="$1"; verdict="$2"; short="$3"; cures="$4"; up="$5"; ever="$6"; want="$7"
    got=$(WORK="$WORK" sh -c ". \"\$WORK/escalate.sh\"; should_reboot $verdict $short $cures $up $ever")
    [ "$got" = "$want" ] && note "$desc -> $got" OK || note "$desc -> $got, want $want" FAIL
}

#              desc                                            verdict short cures up   ever want
escalate_case "five short runs on a board that has been up"    restart 5  0  3600 yes  yes
escalate_case "four short runs is not a loop yet"              restart 4  0  3600 yes  no
escalate_case "no short runs at all"                           restart 0  0  3600 yes  no
escalate_case "two failed cures on a board that has been up"   hung    0  2  3600 yes  yes
escalate_case "one failed cure is not enough"                  hung    0  1  3600 yes  no

# A deliberate stop is the operator's intent, and a healthy server has nothing
# wrong with it. Neither is ever a reason to take the KVM away from someone.
escalate_case "a deliberate stop is never escalated"           stopped 99 99 3600 yes  no
escalate_case "a healthy server is never escalated"            healthy 99 99 3600 yes  no

echo
echo "  --- a board that has never served since boot is never rebooted"
# The floor sets the period of a reboot cycle. It does not prevent one: a fault
# present from boot keeps producing restart or hung verdicts, the counters do not
# survive a reboot, and the moment uptime crosses the floor the same sequence
# escalates again. Measured against the shipped loop, that is every 10.5 minutes
# forever - a board that answers nothing and cannot be repaired.
#
# A reboot cures a board that worked and then broke. It is never a cure for a
# server that has not answered once since this boot: an unreadable certificate,
# a binary that skipped patchelf, a truncated copy from a full SD card - all of
# them leave the binary staged and executable, so the verdict is restart, and
# all of them survive a reboot unchanged.
escalate_case "a crash loop that never answered since boot"    restart 9  0  3600 no   no
escalate_case "a hang that never answered since boot"          hung    0  9  3600 no   no

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
escalate_case "crash loop one second under the floor"          restart 5  0  599  yes  no
escalate_case "crash loop straight out of boot"                restart 9  0  0    yes  no
escalate_case "hang one second under the floor"                hung    0  2  599  yes  no
escalate_case "hang straight out of boot"                      hung    0  9  0    yes  no
escalate_case "crash loop exactly at the floor"                restart 5  0  600  yes  yes
escalate_case "hang exactly at the floor"                      hung    0  2  600  yes  yes

echo
echo "  --- a guard that inverts its own meaning on bad input is not a guard"
# `[ "" -lt 600 ]` is an error, not a false comparison. The `if` is false, so the
# floor is skipped and the board reboots at whatever uptime it has. `up` comes
# from `cut -d. -f1 /proc/uptime`, and any failure to fork that - memory
# pressure, PID exhaustion, a stalled filesystem - yields an empty string. A typo
# in SUPERVISE_REBOOT_FLOOR opens the same hole from the other side.
#
# These cases cannot use escalate_case: field splitting cannot produce an empty
# argument, and the threshold ones have to set a variable before sourcing.
got=$(WORK="$WORK" sh -c '. "$WORK/escalate.sh"; should_reboot hung 0 2 "" yes')
[ "$got" = no ] && note "an uptime that could not be read -> $got" OK \
                || note "an uptime that could not be read -> $got, want no" FAIL

got=$(WORK="$WORK" sh -c '. "$WORK/escalate.sh"; should_reboot restart 5 0 abc yes')
[ "$got" = no ] && note "an uptime that is not a number -> $got" OK \
                || note "an uptime that is not a number -> $got, want no" FAIL

got=$(WORK="$WORK" sh -c 'REBOOT_FLOOR=ten; . "$WORK/escalate.sh"; should_reboot restart 5 0 10 yes')
[ "$got" = no ] && note "a floor that is not a number -> $got" OK \
                || note "a floor that is not a number -> $got, want no" FAIL

# Digits are not a range. busybox `[` answers "out of range" on a value wider
# than the comparison can hold, and that is an error, so it skips the floor by
# exactly the route an empty string does. /proc/uptime cannot produce one. A
# typo in SUPERVISE_REBOOT_FLOOR can, and it would turn an operator's "never
# reboot this board" into "reboot this board at any uptime".
got=$(WORK="$WORK" sh -c '. "$WORK/escalate.sh"; should_reboot restart 5 0 99999999999999999999 yes')
[ "$got" = no ] && note "an uptime too wide for the comparison -> $got" OK \
                || note "an uptime too wide for the comparison -> $got, want no" FAIL

got=$(WORK="$WORK" sh -c 'REBOOT_FLOOR=99999999999999999999; . "$WORK/escalate.sh"; should_reboot restart 5 0 3600 yes')
[ "$got" = no ] && note "a floor too wide for the comparison -> $got" OK \
                || note "a floor too wide for the comparison -> $got, want no" FAIL

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

echo
echo "===== clearing the counters needs an answer, not just a verdict ====="
# action() reports healthy for a process that is up and not answering yet,
# inside HANG_AFTER, and the hang branch resets LAST_OK after every cure - so
# the very next poll reports healthy too. Clearing on the verdict name alone
# would wipe the counters between every cure and the hung verdict that should
# follow it, and the counted hang escalation could never reach its threshold.
clear_case() {
    desc="$1"; verdict="$2"; answered="$3"; want="$4"
    got=$(WORK="$WORK" sh -c ". \"\$WORK/count.sh\"; should_clear $verdict $answered")
    [ "$got" = "$want" ] && note "$desc -> $got" OK || note "$desc -> $got, want $want" FAIL
}

#          desc                                                 verdict answered want
clear_case "answering: the fault is over"                       healthy yes      yes
clear_case "up but not answering yet, inside the grace"         healthy no       no
# The supervisor's own cure is S95nanokvm restart, which removes /tmp/server and
# copies 36MB back. For that whole window there is no process and no binary, so
# the verdict is the one a deliberate stop gives - and clearing there wipes the
# cure counters on exactly the slow SD card the fault arrives with. Measured: a
# 20-second re-stage escalated on the third hung verdict, a 40-second re-stage
# never escalated at all. An operator who stops the server and brings it back
# produces an answering healthy poll, which clears the counters safely.
clear_case "mid-cure, while S95nanokvm re-stages /tmp"          stopped no       no
clear_case "stopped but something answered - not a cure signal" stopped yes      no
clear_case "hung and answered this pass - still mid-cure"       hung    yes      no
clear_case "hung and silent - stale counts must survive"        hung    no       no
clear_case "gone and answered this pass - impossible but safe"  restart yes      no
clear_case "gone and silent"                                    restart no       no

echo
echo "===== rebooting, and refusing to ====="
# SUPERVISE_NO_REBOOT exists for the hardware test and for an operator who wants
# to leave a board in its failed state to investigate it.
# Asserting silence would pass on any breakage that produces nothing at all, so
# this asserts the decision was written down. That log line is the whole point of
# the switch: it exists for the hardware test and for an operator who wants the
# board left in its failed state, and both need to read what it would have done.
got=$(WORK="$WORK" sh -c '
    SUPERVISE_NO_REBOOT=1
    NO_REBOOT=1
    . "$WORK/act.sh"
    log()             { echo "LOG: $*"; }
    capture_bounded() { echo "captured"; }
    sync()            { echo "synced"; }
    reboot()          { echo "REBOOTED"; }
    sleep()           { :; }
    escalate "test"
' | tr '\n' ' ')
want="LOG: would reboot (test), but SUPERVISE_NO_REBOOT is set "
[ "$got" = "$want" ] && note "SUPERVISE_NO_REBOOT=1 records the decision and does nothing else" OK \
                     || note "SUPERVISE_NO_REBOOT=1 did [$got], want [$want]" FAIL

got=$(WORK="$WORK" sh -c '
    NO_REBOOT=0
    . "$WORK/act.sh"
    log()             { :; }
    capture_bounded() { echo "captured"; }
    sync()            { echo "synced"; }
    reboot()          { echo "REBOOTED"; }
    sleep()           { :; }
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
    . "$WORK/act.sh"
    log()              { :; }
    capture_evidence() { sleep 60; }
    capture_bounded "test"
' > /dev/null 2>&1
elapsed=$(( $(date +%s) - start ))
[ "$elapsed" -lt 20 ] && note "a wedged capture is abandoned after ~10s (took ${elapsed}s)" OK \
                      || note "a wedged capture held the reboot for ${elapsed}s" FAIL

# A reader of /proc/cvitek/vb blocks forever in uninterruptible sleep and cannot
# be killed. Reading it here would mean the board never reboots at all.
if grep -v '^[[:space:]]*#' "$SV" | grep -q '/proc/cvitek/vb'; then
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

echo
echo "===== the carveout is recorded at each restart ====="
# The carveout erodes with restarts, not with uptime, so the only place this can
# be measured is here, at the moment a restart happens.
ion_case() {   # $1 = name, $2 = fixture dir, $3 = expected line, or "" for none
    : > "$WORK/ion.log"
    (
        # ION_DIR is read by the block through its ${ION_DIR:-...} default, so it
        # is set inside the subshell that sources the block and nowhere else.
        ION_DIR=$2
        log() { echo "$*" >> "$WORK/ion.log"; }
        . "$WORK/ion.sh"
        ion_line
    ) 2> "$WORK/ion.err"
    rc=$?
    got=$(cat "$WORK/ion.log")
    err=$(cat "$WORK/ion.err")
    # Every path through ion_line ends in an explicit `return 0`, and it never
    # writes to stderr. A guard that has been removed does not just skip a line
    # - on the zero-total fixture it lets the shell divide by zero, which exits
    # nonzero and prints to stderr rather than quietly producing empty output.
    # Checking only the log line would call that "caught" for free and prove
    # nothing about the guard.
    if [ "$got" = "$3" ] && [ "$rc" -eq 0 ] && [ -z "$err" ]; then
        note "$1 -> [$got]" OK
    else
        note "$1 -> [$got] rc=$rc err=[$err], want [$3] rc=0" FAIL
    fi
}

mkfixture() {   # $1 = dir, $2 = alloc, $3 = total, $4 = generations
    mkdir -p "$1"
    echo "$2" > "$1/alloc_mem"
    echo "$3" > "$1/total_mem"
    {
        echo "Details:"
        i=0
        while [ "$i" -lt "$4" ]; do
            echo "               0           294912         8bef4000                1 ISP_SHARED_BUFFER_0"
            i=$(( i + 1 ))
        done
        echo "minimum ion allocate unit = 4096"
    } > "$1/summary"
}

mkfixture "$WORK/ion-clean"  19050496 78643200 1
mkfixture "$WORK/ion-orphan" 49459200 78643200 2
mkfixture "$WORK/ion-zero"   19050496 0        1

ion_case "a healthy board reports one generation" \
    "$WORK/ion-clean"  "ion 19050496/78643200 24% gen=1"
ion_case "an orphaned generation is counted" \
    "$WORK/ion-orphan" "ion 49459200/78643200 62% gen=2"
# ion-absent is deliberately never created by mkfixture. A board without the
# debugfs entry must write no line at all.
ion_case "a missing carveout writes nothing" \
    "$WORK/ion-absent" ""
ion_case "a zero total writes nothing rather than dividing by it" \
    "$WORK/ion-zero"   ""

# A summary that cannot be read must not lose the counters.
rm -f "$WORK/ion-clean/summary"
ion_case "a missing summary still reports the counters" \
    "$WORK/ion-clean"  "ion 19050496/78643200 24% gen=0"

# Not in the brief's fixture set: every mkfixture value above is a plain digit
# string, so a case guard that stopped rejecting non-numeric input would leave
# every case above unchanged and pass anyway - the debugfs file is text ("carveout
# heap size:..." on a kernel where the split integer files do not exist), so a
# malformed total is a real state, not a hypothetical one.
mkdir -p "$WORK/ion-total-garbage"
echo 19050496 > "$WORK/ion-total-garbage/alloc_mem"
echo "carveout heap size:78643200 bytes" > "$WORK/ion-total-garbage/total_mem"
ion_case "a non-numeric total writes nothing rather than being accepted" \
    "$WORK/ion-total-garbage" ""

# Anchored to the call site inside full_restart, not to the function name: a
# grep for "ion_line" alone would also match its own definition, so a function
# that is defined and never called would pass every case above.
got=$(sed -n '/^full_restart()/,/^}/p' "$SV" | grep -c '^[[:space:]]*ion_line$')
[ "$got" = "1" ] && note "full_restart actually calls ion_line" OK \
                 || note "full_restart calls ion_line $got times, want 1" FAIL

echo
echo "===== the script still parses ====="
sh -n "$SV" && note "S98supervise is valid shell" OK || note "S98supervise does not parse" FAIL
# This check used to grep for the redirection string, and passed while the
# behaviour was wrong: measured over ssh, `start` printed its line and then held
# the session open until the client gave up after five minutes. Redirecting the
# loop's stdio to /dev/null is not enough on busybox - it needs its own session.
#
# Whether it really detaches can only be shown on a device, by timing how long
# `start` takes to return. This asserts the mechanism is present; the timing is
# the evidence, and it belongs in the deploy notes rather than here.
grep -q 'setsid "\$0" __watch' "$SV" \
    && note "detaches with setsid, not merely redirected fds" OK \
    || note "no setsid - start would hold the calling ssh session" FAIL
grep -q '__watch)' "$SV" \
    && note "the entry point setsid re-enters is handled" OK \
    || note "setsid re-enters a subcommand the script does not handle" FAIL

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
# One pattern matching both call sites meant deleting either alone left it
# green - the other call kept the string in the file. Anchored one per site,
# the way every other wiring check here is one-to-one with the call it proves.
grep -qE '^[[:space:]]+escalate "\$hang_reason"$' "$SV" \
    && note "the hang branch actually calls escalate" OK \
    || note "escalate is defined but the hang branch never reaches it" FAIL

# The reason string is what lands in the evidence directory, and it is the only
# thing that says which hang this was. A process that will not die after SIGKILL
# is not two cures that did not work: no cure was attempted, and none would help.
# Collapsing the two strings parses and changes no decision, so only a check that
# both spellings exist can see it.
counted=$(grep -c 'hang_reason="hung: \$failed_cures cures did not restore service"' "$SV")
unkillable=$(grep -c 'hang_reason="hung: the process did not leave after SIGKILL"' "$SV")
if [ "$counted" -eq 1 ] && [ "$unkillable" -eq 1 ]; then
    note "the two hang reasons say different things" OK
else
    note "hang reasons: $counted counted, $unkillable unkillable, want one of each" FAIL
fi
grep -qE '^[[:space:]]+escalate "crash loop: ' "$SV" \
    && note "the restart branch actually calls escalate" OK \
    || note "escalate is defined but the restart branch never reaches it" FAIL
grep -qE '^[[:space:]]+short_runs=\$\(next_short_runs ' "$SV" \
    && note "the restart branch actually updates short_runs" OK \
    || note "next_short_runs is defined and never called" FAIL
grep -qE '^[[:space:]]+failed_cures=\$\(next_failed_cures ' "$SV" \
    && note "the hang branch actually updates failed_cures" OK \
    || note "next_failed_cures is defined and never called" FAIL
grep -qE '^[[:space:]]+if \[ "\$\(should_clear ' "$SV" \
    && note "the loop actually asks should_clear before wiping the counters" OK \
    || note "should_clear is defined and never called" FAIL

# serving fails open on purpose: a probe that cannot run must never kill a
# working KVM. That answer cannot also be the answer the latch reads, or "the
# probe could not run" is recorded as "the server answered" and a board without
# curl gets the reboot cycle the latch exists to prevent. Three answers, and
# only 0 means the server answered.
grep -qE '^[[:space:]]+\[ -x "\$\(command -v curl\)" \] \|\| return 2$' "$SV" \
    && note "a probe that cannot run says so, rather than saying success" OK \
    || note "a missing curl is indistinguishable from an answering server" FAIL

# The latch has to reach both decisions, or half the reboot cycle comes back.
grep -qE '^[[:space:]]+served_ever=yes$' "$SV" \
    && note "the loop sets the latch when the probe answers" OK \
    || note "nothing ever sets served_ever, so no board could reboot" FAIL

# watch_loop is a while loop with side effects, so this suite cannot drive it
# and the latch's own assignment has no unit case. What can be asserted is its
# shape: the one assignment in the file sits directly inside the branch that
# tests for status 0, so no other status can reach it. Deleting the branch or
# widening it to -ne 1 makes this report FAIL.
latch_line=$(sed -n '/^[[:space:]]*if \[ "\$answered" -eq 0 \]; then$/{n;s/^[[:space:]]*//;p;}' "$SV")
latch_count=$(grep -c '^[[:space:]]*served_ever=yes$' "$SV")
if [ "$latch_line" = "served_ever=yes" ] && [ "$latch_count" -eq 1 ]; then
    note "the latch is set only where the probe answered" OK
else
    note "the latch is set outside the answered branch (guarded [$latch_line], $latch_count assignments)" FAIL
fi
grep -qE 'should_reboot restart .*"\$served_ever"' "$SV" \
    && note "the restart branch passes the latch" OK \
    || note "the restart branch judges without the latch" FAIL
grep -qE 'should_reboot hung .*"\$served_ever"' "$SV" \
    && note "the hang branch passes the latch" OK \
    || note "the hang branch judges without the latch" FAIL

# served_ever is a per-boot latch, not a counter. should_clear must never reach
# it: a fault present from boot would then escalate as soon as the floor let it,
# the counters would not survive the reboot, and the identical sequence would
# repeat every REBOOT_FLOOR seconds for as long as the board had power.
if [ "$(grep -c '^[[:space:]]*served_ever=no$' "$SV")" -eq 1 ]; then
    note "the latch is set once at boot and never cleared" OK
else
    note "served_ever is assigned no in more than one place, so it is a counter" FAIL
fi

echo
if [ "$fails" -eq 0 ]; then
    echo "===== all supervisor cases pass ====="
else
    echo "===== $fails case(s) failed ====="
    exit 1
fi
