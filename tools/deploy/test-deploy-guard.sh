#!/bin/sh
# Exercise the deploy guard's decisions, taken straight out of the script that
# ships so the test cannot drift from it.
#
#   test-deploy-guard.sh [path-to-deploy-server]
#
# Not destructive: every case runs against a temporary directory tree and the
# probe is stubbed. No device is touched.
DG=${1:-$(dirname "$0")/deploy-server}
[ -f "$DG" ] || { echo "usage: test-deploy-guard.sh <deploy-server>"; exit 1; }

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

sed -n '/^# --- verdict ---$/,/^# --- end verdict ---$/p'   "$DG" > "$WORK/verdict.sh"
sed -n '/^# --- snapshot ---$/,/^# --- end snapshot ---$/p' "$DG" > "$WORK/snapshot.sh"
sed -n '/^# --- preflight ---$/,/^# --- end preflight ---$/p' "$DG" > "$WORK/preflight.sh"
[ -s "$WORK/verdict.sh" ]  || { echo "could not extract the verdict block"; exit 1; }
[ -s "$WORK/snapshot.sh" ] || { echo "could not extract the snapshot block"; exit 1; }
[ -s "$WORK/preflight.sh" ] || { echo "could not extract the preflight block"; exit 1; }

fails=0
note() { printf '  %-62s %s\n' "$1" "$2"; [ "$2" = FAIL ] && fails=$((fails + 1)); return 0; }

echo "===== keep or restore ====="
# The guard exists because a manual backup only helps if someone is watching.
# The verdict must depend on whether the service answers, nothing else.
verdict_case() {
    desc="$1"; healthy="$2"; matches="$3"; want="$4"
    got=$(HEALTHY="$healthy" MATCHES="$matches" WORK="$WORK" sh -c '
        serving() { [ "$HEALTHY" = yes ]; }
        running_matches_installed() { [ "$MATCHES" = yes ]; }
        . "$WORK/verdict.sh"
        verdict
    ')
    [ "$got" = "$want" ] && note "$desc -> $got" OK || note "$desc -> $got, want $want" FAIL
}

verdict_case "answers, and the running copy is the one installed" yes yes keep
verdict_case "does not answer"                                  no  yes restore

# The failure this was rewritten after. tmpfs filled up, the candidate copied in
# truncated, and the guard said OK - because S95nanokvm runs a copy of the binary
# from /tmp and that copy was still the old good one. Serving proves a server is
# up. It does not prove it is the server just installed.
verdict_case "answers, but a stale copy is running"             yes no  restore

echo
echo "===== the known-good copy is only replaced by a proven one ====="
# The trap this closes: deploy twice in a row and a naive backup would snapshot
# the first broken binary as the thing to fall back to. A snapshot is only taken
# when the server that is running right now is answering.
snap_case() {
    desc="$1"; healthy="$2"; existing="$3"; want="$4"
    D="$WORK/d"; rm -rf "$D"; mkdir -p "$D/known-good"
    printf 'running\n' > "$D/current"
    [ "$existing" = none ] || printf '%s\n' "$existing" > "$D/known-good/binary"

    HEALTHY="$healthy" D="$D" WORK="$WORK" sh -c '
        serving() { [ "$HEALTHY" = yes ]; }
        CURRENT="$D/current"; GOOD="$D/known-good/binary"
        . "$WORK/snapshot.sh"
        snapshot_if_proven
    ' > /dev/null 2>&1

    if [ -f "$D/known-good/binary" ]; then got=$(cat "$D/known-good/binary"); else got=none; fi
    [ "$got" = "$want" ] && note "$desc -> $got" OK || note "$desc -> $got, want $want" FAIL
}

snap_case "running server is healthy, so snapshot it"        yes "old"  "running"
snap_case "running server is sick, keep the old known-good"  no  "old"  "old"
snap_case "running server is sick and nothing saved yet"     no  none   none
snap_case "healthy with nothing saved yet"                   yes none   "running"

echo
echo "===== a failed restore must be loud ====="
# If the restore also fails to come up, the box is in the state this script
# exists to prevent. It must report that rather than exit 0 and look successful.
got=$(WORK="$WORK" sh -c '
    serving() { return 1; }
    . "$WORK/verdict.sh"
    if restore_worked; then echo quiet; else echo loud; fi
')
[ "$got" = "loud" ] && note "restore that does not come up reports failure" OK \
                    || note "restore failure reported as $got" FAIL

got=$(WORK="$WORK" sh -c '
    serving() { return 0; }
    . "$WORK/verdict.sh"
    if restore_worked; then echo quiet; else echo loud; fi
')
[ "$got" = "quiet" ] && note "restore that comes up reports success" OK \
                     || note "successful restore reported as $got" FAIL

echo
echo "===== refuse before touching anything ====="
# Staging in tmpfs is what broke this. /tmp is 79MB, S95nanokvm copies the whole
# of /kvmapp/server into it on every restart, and a short copy is silent.
pre_case() {
    desc="$1"; elf="$2"; tmpfree="$3"; want="$4"
    got=$(ELF="$elf" TMPFREE="$tmpfree" NEEDED=24000 WORK="$WORK" sh -c '
        candidate_is_elf()  { [ "$ELF" = yes ]; }
        tmp_free_kb()       { echo "$TMPFREE"; }
        candidate_size_kb() { echo "$NEEDED"; }
        . "$WORK/preflight.sh"
        preflight && echo proceed || echo refuse
    ')
    [ "$got" = "$want" ] && note "$desc -> $got" OK || note "$desc -> $got, want $want" FAIL
}

pre_case "intact candidate and room in tmpfs"       yes 48000 proceed
pre_case "candidate is not an ELF"                  no  48000 refuse
pre_case "tmpfs cannot hold the copy S95nanokvm makes" yes 20000 refuse

# The boundary is 1.5x the candidate, because /kvmapp/server is not just the
# binary: measured on the device it is a 23.6MB binary plus about 5MB of dl_lib
# and 2.5MB of web assets, so the copy needs roughly 1.31x. 1.5x is margin over
# a measured number rather than a guess. NEEDED is 24000, so the line is 36000.
pre_case "exactly at the 1.5x boundary"             yes 36000 proceed
pre_case "one kB under the boundary"                yes 35999 refuse

echo
echo "===== the script still parses ====="
sh -n "$DG" && note "deploy-server is valid shell" OK || note "deploy-server does not parse" FAIL
grep -q 'setsid' "$DG" \
    && note "detaches, so a dropped ssh cannot abandon it mid-deploy" OK \
    || note "does not detach - a dropped ssh would leave it half done" FAIL

echo
if [ "$fails" -eq 0 ]; then
    echo "===== all deploy guard cases pass ====="
else
    echo "===== $fails case(s) failed ====="
    exit 1
fi
