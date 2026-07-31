#!/bin/sh
# Exercise the decisions in S99vidiag, taken straight out of the script that
# ships so the test cannot drift from it.
#
#   test-vidiag.sh [path-to-S99vidiag]
#
# Not destructive: every case runs against a temporary directory. No device is
# touched, and nothing reads the real syslog.
VD=${1:-$(dirname "$0")/../../kvmapp/system/init.d/S99vidiag}
[ -f "$VD" ] || { echo "usage: test-vidiag.sh <S99vidiag>"; exit 1; }

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

sed -n '/^# --- filter ---$/,/^# --- end filter ---$/p' "$VD" > "$WORK/filter.sh"
sed -n '/^# --- record ---$/,/^# --- end record ---$/p' "$VD" > "$WORK/record.sh"
sed -n '/^# --- trim ---$/,/^# --- end trim ---$/p'     "$VD" > "$WORK/trim.sh"
[ -s "$WORK/filter.sh" ] || { echo "could not extract the filter block"; exit 1; }
[ -s "$WORK/record.sh" ] || { echo "could not extract the record block"; exit 1; }
[ -s "$WORK/trim.sh" ]   || { echo "could not extract the trim block"; exit 1; }

fails=0
note() { printf '  %-64s %s\n' "$1" "$2"; [ "$2" = FAIL ] && fails=$((fails + 1)); return 0; }

echo "===== which lines earn a write to the SD card ====="
# The card is the reason this filter exists. Every kept line is a write, and the
# device boots from the same card.
keep_case() {
    desc="$1"; line="$2"; want="$3"
    got=$(LINE="$line" WORK="$WORK" sh -c '. "$WORK/filter.sh"; keep_line "$LINE" && echo keep || echo drop')
    [ "$got" = "$want" ] && note "$desc -> $got" OK || note "$desc -> $got, want $want" FAIL
}

# The three ways _mmf_vpss_init_new can fail. These are the whole point: each
# one names the call and carries the error code the investigation lacks.
keep_case "CreateGrp failed" \
    'Jul 30 20:59:40 nanokvm local5.err : [VPSS-ERR] cvi_vpss.c:401:CVI_VPSS_CreateGrp(): Grp(0) has been created' keep
keep_case "ResetGrp failed" \
    'Jul 30 20:59:40 nanokvm local5.err : [VPSS-ERR] cvi_vpss.c:512:CVI_VPSS_ResetGrp(): Grp(0) reset fail' keep
keep_case "StartGrp failed" \
    'Jul 30 20:59:40 nanokvm local5.err : [VPSS-ERR] cvi_vpss.c:600:CVI_VPSS_StartGrp(): Grp(0) start fail' keep

# The teardown half. A group that is never destroyed is the state that makes the
# next init fail, so the destroy calls matter as much as the create calls.
keep_case "DestroyGrp failed" \
    'Jul 30 20:59:40 nanokvm local5.err : [VPSS-ERR] cvi_vpss.c:450:CVI_VPSS_DestroyGrp(): Grp(0) destroy fail' keep
keep_case "StopGrp failed" \
    'Jul 30 20:59:40 nanokvm local5.err : [VPSS-ERR] cvi_vpss.c:580:CVI_VPSS_StopGrp(): Grp(0) stop fail' keep

# The kernel side of the same fault, and the pair that started the investigation.
keep_case "kernel job init fail" \
    'Jul 30 20:59:40 nanokvm kern.err kernel: base_mod_jobs_init: mod(VI) job init fail, already inited' keep
keep_case "kernel job exit fail" \
    'Jul 30 20:59:40 nanokvm kern.err kernel: base_mod_jobs_exit: mod(VI) job exit fail, not inited yet' keep

keep_case "our own init failure line" \
    'Jul 30 20:59:40 nanokvm user.err : _mmf_vpss_init_new failed. s32Ret: 0xc0078003 !' keep
keep_case "a VI error from the driver" \
    'Jul 30 20:59:40 nanokvm local5.err : [VI-ERR] cvi_vi.c:100:CVI_VI_Something(): fail' keep

# The reason the rule is not a list of expected call names. We do not know which
# call fails. A line naming a call nobody predicted is exactly the evidence this
# script exists to catch, so it has to be kept.
keep_case "a VPSS error from a call nobody predicted" \
    'Jul 30 20:59:40 nanokvm local5.err : [VPSS-ERR] cvi_vpss.c:777:CVI_VPSS_SetChnAttr(): Grp(0) unexpected' keep
keep_case "a SYS error during pipeline setup" \
    'Jul 30 20:59:40 nanokvm local5.err : [SYS-ERR] cvi_sys.c:200:CVI_SYS_Bind(): fail' keep

echo
echo "===== the per-frame noise must never reach the card ====="
# This is the case that decides whether the script is safe to leave running.
# GetChnFrame fails once a second for as long as a viewer is connected to a
# target whose display is asleep. It matched 62 times in one boot of an
# otherwise healthy board. Writing it would be a hot file on the boot medium,
# which is the one thing the device documentation says not to create.
keep_case "GetChnFrame failure (once a second, understood)" \
    'Jul 30 20:59:40 nanokvm local5.err : [VPSS-ERR] cvi_vpss.c:911:CVI_VPSS_GetChnFrame(): Grp(0) Chn(0) get chn frame fail' drop
keep_case "ReleaseChnFrame failure" \
    'Jul 30 20:59:40 nanokvm local5.err : [VPSS-ERR] cvi_vpss.c:930:CVI_VPSS_ReleaseChnFrame(): fail' drop

# The exclusion has to win over the general VPSS-ERR match, not sit after it.
keep_case "GetChnFrame is dropped even though it is a VPSS-ERR" \
    'local5.err : [VPSS-ERR] CVI_VPSS_GetChnFrame(): fail' drop

echo
echo "===== the server's own log is a second source ====="
# S95nanokvm now sends the server's stdout and stderr to /tmp/nanokvm-server.log,
# and this reader follows that file as well as the syslog. Those lines carry no
# syslog prefix, because libkvm prints them with printf. The first two are the
# reason the whole investigation exists: they name the failing call and carry
# the error code that no earlier attempt could recover.
keep_case "libkvm's own init failure, no syslog prefix" \
    '_mmf_vpss_init_new failed. s32Ret: 0xc0078003 !' keep
keep_case "the caller's report of the same failure" \
    'mmf_vi_init failed!' keep

# libkvm prints on error paths only, so a CVI call that reports a failure is
# worth keeping even when nobody predicted it. These are one-shot setup and
# teardown errors, not per-frame ones.
keep_case "encoder channel would not start" \
    'CVI_VENC_CreateChn [0] failed with 0xc0038007' keep
keep_case "an attribute call nobody predicted" \
    'CVI_VPSS_SetChnAttr failed with 0xc0078003' keep
keep_case "a bind failure during setup" \
    'CVI_SYS_Bind failed with 0xc0018005' keep

# The per-frame half of the same vocabulary. These repeat for as long as a
# viewer is connected, so they must not reach the SD card from this source
# either.
keep_case "encoder send, once per frame" \
    'CVI_VENC_SendFrame failed with -1' drop
keep_case "encoder poll finds nothing, once per frame" \
    'CVI_VENC_QueryStatus find not pack' drop
keep_case "stream read, once per frame" \
    'CVI_VENC_GetStream failed with 0xc0038010' drop
keep_case "stream release, once per frame" \
    'CVI_VENC_ReleaseStream failed with 0xc0038010' drop
keep_case "frame allocation, once per frame" \
    'Alloc frame failed!' drop

# The device runs the server with logger.level: info and logger.file: stdout,
# so logrus writes into the same file. Those entries are not driver errors and
# must not be recorded, or an ordinary boot would fill the log.
keep_case "a logrus info entry" \
    '2026-07-31 08:00:00 INFO logger set success' drop
keep_case "a logrus error entry about something else" \
    '2026-07-31 08:00:00 ERRO get hardware version failed' drop

echo
echo "===== the teardown that precedes a failure must be recorded ====="
# ~CameraCviMmf ends with mmf_try_deinit(true), which sets the reference count to
# zero and destroys the media stack for every user at once. Camera::restart calls
# it, and the resolution probe and the detect thread call Camera::restart while
# the board runs. A failed init that follows one of those has a cause that the
# driver's own message does not name, so the record needs the marker as well.
#
# These are lifecycle markers, not errors. The log holds a few for each start.
keep_case "the media stack was destroyed for everyone" \
    'maix multi-media driver destroyed.' keep
keep_case "something asked for a camera restart" \
    '[kvmv] restart cam...' keep
keep_case "the reload fired, so a stale pool was there" \
    'mmf insmod..' keep

# The probe prints this on every pass of a loop that does not advance, and the
# loop body has no delay. Recording it would put an unbounded flood on the SD
# card, which is the one thing this filter exists to prevent.
keep_case "the probe cannot read the input, and says so forever" \
    '[kvmv] Cannot obtain HDMI input' drop
keep_case "the probe walking its resolution list" \
    '[kvmv] Trying 1600 * 900 res ..' drop
keep_case "the probe reporting a healthy pipeline" \
    '[kvmv] VI subsystem is normal' drop

echo
echo "===== everything else is ignored ====="
keep_case "an ordinary syslog line" \
    'Jul 30 20:59:40 nanokvm daemon.info udhcpc[300]: lease of 10.0.0.222 obtained' drop
keep_case "an empty line" '' drop

echo
echo "===== the cap protects the card, and says so ====="
# A pipeline failing in a loop must not write for as long as the board is up.
cap_case() {
    desc="$1"; total="$2"; max="$3"; want_lines="$4"; want_notice="$5"
    out="$WORK/cap.log"; : > "$out"
    got=$(TOTAL="$total" MAX="$max" LOG="$out" WORK="$WORK" sh -c '
        MAX_LINES=$MAX
        . "$WORK/record.sh"
        kept=0
        i=0
        while [ $i -lt $TOTAL ]; do
            record_line "error line $i"
            i=$((i + 1))
        done
    ')
    lines=$(grep -c '^error line' "$out")
    notice=$(grep -c 'cap reached' "$out")
    [ "$lines" = "$want_lines" ] && [ "$notice" = "$want_notice" ] \
        && note "$desc -> $lines written, $notice notice" OK \
        || note "$desc -> $lines written / $notice notice, want $want_lines / $want_notice" FAIL
}

cap_case "under the cap, everything is written"      3  5 3 0
cap_case "exactly at the cap, no notice"             5  5 5 0
cap_case "over the cap, the rest is dropped"        20  5 5 1
cap_case "far over the cap, the notice appears once" 200 5 5 1

echo
echo "===== the server log must not fill tmpfs ====="
# tmpfs holds 80MB and a server restart copies 36MB of /kvmapp/server into it.
# A pipeline that fails per frame prints without limit, so an untrimmed log
# fills tmpfs and stops the next restart. That is a worse fault than the one
# this script investigates, so the reader empties the file when it grows.
#
# stderr is captured with stdout on purpose. A missing file must produce no
# message at all: the earlier version of rotate_if_large redirected wc, but the
# shell reports a failed redirection itself, so the message escaped anyway.
trim_case() {
    desc="$1"; bytes="$2"; max="$3"; want_verdict="$4"; want_size="$5"
    f="$WORK/srv.log"; rm -f "$f"
    [ "$bytes" = absent ] || dd if=/dev/zero of="$f" bs=1 count="$bytes" 2>/dev/null
    got=$(SRVLOG="$f" SRV_MAX_BYTES="$max" WORK="$WORK" sh -c '
        . "$WORK/trim.sh"
        trim_server_log && echo trimmed || echo kept
    ' 2>&1)
    if [ -f "$f" ]; then size=$(wc -c < "$f" | tr -d ' '); else size=absent; fi
    [ "$got" = "$want_verdict" ] && [ "$size" = "$want_size" ] \
        && note "$desc -> $got, $size bytes" OK \
        || note "$desc -> [$got]/$size, want [$want_verdict]/$want_size" FAIL
}

trim_case "under the cap, left alone"          100 1000 kept    100
trim_case "exactly at the cap, left alone"    1000 1000 kept    1000
trim_case "one byte over the cap, emptied"    1001 1000 trimmed 0
trim_case "no server log yet, and no message" absent 1000 kept  absent

echo
if [ "$fails" -eq 0 ]; then
    echo "all cases passed"
else
    echo "$fails case(s) FAILED"
fi
exit "$fails"
