#!/bin/sh
# Cross-compile NanoKVM-Server and patch its RPATH. Runs inside the image
# built from the Dockerfile beside this file, with server/ as the working
# directory.
#
#   BUILD_STAMP=dev.20260729.abc1234 build-app.sh
#
# Flags match GO_BUILD_CMD in the repository Makefile. `go mod download` is
# used instead of the Makefile's `go mod tidy` so a build cannot rewrite
# go.mod/go.sum in a bind-mounted working tree.
#
# GOEXPERIMENT=boringcrypto is deliberately not set. server/build.sh sets it
# and the Makefile does not, so the two entry points do not produce the same
# binary; BoringCrypto only supports amd64 and arm64, so it buys nothing on
# riscv64. This follows the Makefile.
set -e

BINARY=NanoKVM-Server

export CGO_ENABLED=1
export GOOS=linux
export GOARCH=riscv64
export CC=riscv64-unknown-linux-musl-gcc
export CGO_CFLAGS="-mcpu=c906fdv -march=rv64imafdcv0p7xthead -mcmodel=medany -mabi=lp64d"

echo "== go mod download"
go mod download

echo "== go build (stamp: ${BUILD_STAMP:-none})"
if [ -n "$BUILD_STAMP" ]; then
    go build -ldflags "-X NanoKVM-Server/common/version.Build=$BUILD_STAMP" -o "$BINARY"
else
    go build -o "$BINARY"
fi

# Expected during linking:
#   libopencv_video.so.409, needed by dl_lib/libkvm.so, not found
# libkvm.so links against five OpenCV libraries and only four ship in dl_lib;
# the fifth lives in the device rootfs, which the cross-linker cannot see. The
# executable records only libkvm.so and libc.so as its own NEEDED entries.

echo "== patchelf"
patchelf --add-rpath '$ORIGIN/dl_lib' "$BINARY"

echo "== result"
readelf -d "$BINARY" | grep -i -E 'runpath|rpath'
readelf -h "$BINARY" | grep -i -E 'machine|class'
ls -l "$BINARY"
