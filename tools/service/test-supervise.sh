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
[ -s "$WORK/decide.sh" ]  || { echo "could not extract the decide block"; exit 1; }
[ -s "$WORK/backoff.sh" ] || { echo "could not extract the backoff block"; exit 1; }
[ -s "$WORK/cure.sh" ]    || { echo "could not extract the cure block"; exit 1; }

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
