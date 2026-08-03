#!/bin/bash
# Build soph_vi.ko with the VI driver's four idle waits moved to TASK_IDLE.
#
#   build-soph-vi.sh <bsp-source> <device-config> [output-dir]
#
# The VI driver starts four kernel threads that sleep in TASK_UNINTERRUPTIBLE.
# Linux counts that state in the load average, so an idle board reports about 4.
# Three of the four threads have used no CPU at all since boot, so the number
# describes nothing. `wait_event_idle` sleeps in TASK_IDLE, which is
# TASK_UNINTERRUPTIBLE|TASK_NOLOAD: the same uninterruptible sleep, excluded
# from the load calculation. The wake condition, the return value and the
# SCHED_FIFO priority do not change.
#
# Get the config from the device itself:
#   ssh root@<device> 'zcat /proc/config.gz' > kernel.config
# Get the source from the board's build tree:
#   git clone --depth 1 --filter=blob:none --sparse -b NanoKVM \
#       https://github.com/sipeed/LicheeRV-Nano-Build
#   cd LicheeRV-Nano-Build && git sparse-checkout set linux_5.10 osdrv
#
# Seeding .config from the running kernel means every option the vermagic is
# derived from already matches. Only CONFIG_LOCALVERSION is forced: the device
# reports 5.10.4-tag-, and reproducing that through LOCALVERSION_AUTO would
# depend on the git state of the machine Sipeed built on.
#
# The osdrv Makefiles read $(PWD), which make does not set - it is the shell's
# own variable. `make -C dir` therefore roots every include path at "/", which
# is why this script and the top-level osdrv Makefile both `cd` first.
set -e

SRC=${1:?usage: build-soph-vi.sh <bsp-source> <device-config> [output-dir]}
CONFIG=${2:?usage: build-soph-vi.sh <bsp-source> <device-config> [output-dir]}
OUT=${3:-$PWD/ko}
CHIP=mars
WANT_VERMAGIC="5.10.4-tag- preempt mod_unload riscv"

KERNEL=$SRC/linux_5.10
OSDRV=$SRC/osdrv/interdrv/v2
VI=$OSDRV/vi
VI_C=$VI/chip/$CHIP/vi.c

[ -f "$KERNEL/Makefile" ] || { echo "no kernel tree: $KERNEL"; exit 1; }
[ -f "$VI_C" ]            || { echo "no VI driver: $VI_C"; exit 1; }
[ -f "$CONFIG" ]          || { echo "no such config: $CONFIG"; exit 1; }

export ARCH=riscv
export CROSS_COMPILE=riscv64-unknown-linux-musl-
export KBUILD_BUILD_USER=build
export KBUILD_BUILD_HOST=nanokvm
export KERNEL_DIR=$KERNEL
export CHIP_CODE=$CHIP

mk() { ( cd "$1" && shift && make KERNEL_DIR="$KERNEL_DIR" "$@" ); }

echo "===== configure the kernel ====="
cd "$KERNEL"
cp "$CONFIG" .config
./scripts/config --file .config --set-str CONFIG_LOCALVERSION "-tag-"
./scripts/config --file .config --disable CONFIG_LOCALVERSION_AUTO
make olddefconfig >/dev/null
echo "  kernelrelease: $(make -s kernelrelease)"

echo
echo "===== prepare the kernel for out-of-tree modules ====="
make -j"$(nproc)" modules_prepare 2>&1 | tail -2

echo
echo "===== patch the four waits ====="
# Match on the waitqueue, not on line numbers: all four waits are on the same
# vi_th[th_id].wq and nothing else in this file waits at all. Refuse to patch if
# that stops being true, because a silent miss produces a module that loads,
# runs, and changes nothing.
plain=$(grep -c 'wait_event(vdev->vi_th\[th_id\]\.wq,' "$VI_C" || true)
timed=$(grep -c 'wait_event_timeout(vdev->vi_th\[th_id\]\.wq,' "$VI_C" || true)

if [ "$plain" = "0" ] && [ "$timed" = "0" ]; then
    echo "  already patched"
elif [ "$plain" = "3" ] && [ "$timed" = "1" ]; then
    sed -i \
        -e 's/wait_event_timeout(vdev->vi_th\[th_id\]\.wq,/wait_event_idle_timeout(vdev->vi_th[th_id].wq,/' \
        -e 's/\bwait_event(vdev->vi_th\[th_id\]\.wq,/wait_event_idle(vdev->vi_th[th_id].wq,/' \
        "$VI_C"
    echo "  patched 4 sites"
else
    echo "  expected 3 wait_event and 1 wait_event_timeout, found $plain and $timed"
    grep -n "wait_event" "$VI_C"
    exit 1
fi
grep -n "wait_event" "$VI_C" | sed 's/^/    /'

echo
echo "===== build ====="
# soph_vi resolves symbols from these two, so they have to exist first.
mk "$OSDRV/base" all -j"$(nproc)" 2>&1 | tail -2
mk "$OSDRV/sys"  all -j"$(nproc)" 2>&1 | tail -2
mk "$VI" clean >/dev/null 2>&1 || true
mk "$VI" all -j"$(nproc)" 2>&1 | tail -3

[ -f "$VI/soph_vi.ko" ] || { echo "no module produced"; exit 1; }
mkdir -p "$OUT"
cp "$VI/soph_vi.ko" "$OUT/"

echo
echo "===== verify ====="
got=$(modinfo "$OUT/soph_vi.ko" | sed -n 's/^vermagic: *//p')
if [ "$got" = "$WANT_VERMAGIC" ]; then
    echo "  OK   vermagic: $got"
else
    echo "  FAIL vermagic: '$got'"
    echo "       want      '$WANT_VERMAGIC'"
    exit 1
fi

# TASK_UNINTERRUPTIBLE is 0x0002, TASK_IDLE is 0x0002|0x0400 = 1026.
# ___wait_event passes that to prepare_to_wait_event, so the change is visible
# as an immediate in the four thread functions and nowhere else. Checking the
# object code catches a patch that applied to the source but not to the build.
dis=$(mktemp)
"${CROSS_COMPILE}objdump" -d "$OUT/soph_vi.ko" > "$dis"
idle=$(grep -cE '\bli\s+a2,1026$' "$dis" || true)
rm -f "$dis"
if [ "$idle" = "4" ]; then
    echo "  OK   four TASK_IDLE waits in the object code"
else
    echo "  FAIL found $idle TASK_IDLE waits, want 4"
    exit 1
fi

echo
echo "  module in $OUT"
echo "  install with: scp $OUT/soph_vi.ko root@<device>:/mnt/system/ko/"
echo "  S00kmod loads it from there; keep the original for a rollback."
