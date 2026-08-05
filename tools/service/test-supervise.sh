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
[ -s "$WORK/decide.sh" ]  || { echo "could not extract the decide block"; exit 1; }
[ -s "$WORK/backoff.sh" ] || { echo "could not extract the backoff block"; exit 1; }
[ -s "$WORK/cure.sh" ]    || { echo "could not extract the cure block"; exit 1; }
[ -s "$WORK/escalate.sh" ] || { echo "could not extract the escalate block"; exit 1; }
[ -s "$WORK/count.sh" ] || { echo "could not extract the count block"; exit 1; }
[ -s "$WORK/act.sh" ] || { echo "could not extract the act block"; exit 1; }

fails=0
note() { printf '  %-60s %s\n' "$1" "$2"; [ "$2" = FAIL ] && fails=$((fails + 1)); return 0; }

echo "===== crashed, or stopped on purpose? ====="
# The distinction costs nothing to make: `S95nanokvm stop` removes /tmp/server
# after killing the process, so the binary's presence is the operator's intent.
# Without this the supervisor would fight every deliberate stop.
decide_case() {
    desc="$1"; binary="$2"; running="$3"; serving="$4"; unhealthy="$5"; want="$6"
    got=$(BIN="$binary" RUN="$running" SRV="$serving" UNW="$unhealthy" WORK="$WORK" sh -c '
        binary_present()  { [ "$BIN" = yes ]; }
        process_running() { [ "$RUN" = yes ]; }
        serving()         { [ "$SRV" = yes ]; }
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

# A wiring check, here because cure_hung was defined and never called while every
# case above stayed green: it was testable in isolation and unreachable from the
# loop. Like the setsid check this asserts a string, so the real evidence is the
# on-device test that wedges the server and watches it come back.
grep -qE '^[[:space:]]+cure_hung$' "$SV" \
    && note "the hang branch actually calls cure_hung" OK \
    || note "cure_hung is defined but never reached" FAIL

echo
if [ "$fails" -eq 0 ]; then
    echo "===== all supervisor cases pass ====="
else
    echo "===== $fails case(s) failed ====="
    exit 1
fi
