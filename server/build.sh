#!/bin/bash

set -e

# Configuration Variables
BINARY_NAME="NanoKVM-Server"
CC_COMPILER="riscv64-unknown-linux-musl-gcc"
CGO_CFLAGS_OPTS="-mcpu=c906fdv -march=rv64imafdcv0p7xthead -mcmodel=medany -mabi=lp64d"

# Define colors for output
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Helper function to check if a command exists
check_dependency() {
    if ! command -v "$1" &> /dev/null; then
        echo -e "${RED}[ERROR] Required command '$1' not found.${NC}"
        echo "Please install it or ensure it is in your PATH."
        exit 1
    fi
}

# ------------------------------------------------------------------------------
# Step 1: Check Prerequisites
# ------------------------------------------------------------------------------
echo -e "${YELLOW}[INFO] Checking build environment...${NC}"

check_dependency "go"
check_dependency "patchelf"
check_dependency "$CC_COMPILER"

echo -e "${GREEN}[OK] All dependencies found.${NC}"

# ------------------------------------------------------------------------------
# Step 2: Build the Binary
# ------------------------------------------------------------------------------
echo -e "${YELLOW}[INFO] Starting cross-compilation for RISC-V 64-bit (BoringCrypto enabled)...${NC}"

export CGO_ENABLED=1
export GOOS=linux
export GOARCH=riscv64
export GOEXPERIMENT=boringcrypto
export CC="$CC_COMPILER"
export CGO_CFLAGS="$CGO_CFLAGS_OPTS"

# Build stamp. The application version lives in /kvmapp/version and is written
# by the updater, so it does not change when a binary is deployed by hand. This
# is linked in and reported as semver build metadata, which identifies a
# hand-built server without affecting version comparison.
#
# Set BUILD_STAMP to override, or BUILD_STAMP= to build unstamped like a
# release does.
if [ -z "${BUILD_STAMP+set}" ]; then
    BUILD_STAMP="dev.$(date +%Y%m%d.%H%M)"

    if git_sha=$(git rev-parse --short HEAD 2>/dev/null); then
        BUILD_STAMP="$BUILD_STAMP.$git_sha"

        if [ -n "$(git status --porcelain 2>/dev/null)" ]; then
            BUILD_STAMP="$BUILD_STAMP.dirty"
        fi
    fi
fi

echo -e "${YELLOW}[INFO] Build stamp: ${BUILD_STAMP:-none}${NC}"

if [ -n "$BUILD_STAMP" ]; then
    go build -ldflags "-X NanoKVM-Server/common/version.Build=$BUILD_STAMP" -o "$BINARY_NAME" -v
else
    go build -o "$BINARY_NAME" -v
fi

if [ -f "$BINARY_NAME" ]; then
    echo -e "${GREEN}[SUCCESS] Binary '$BINARY_NAME' created successfully.${NC}"
else
    echo -e "${RED}[ERROR] Build failed. Binary not found.${NC}"
    exit 1
fi

# ------------------------------------------------------------------------------
# Step 3: Patch RPATH
# ------------------------------------------------------------------------------
echo -e "${YELLOW}[INFO] Patching RPATH with patchelf...${NC}"

patchelf --add-rpath '$ORIGIN/dl_lib' "$BINARY_NAME"

echo -e "${GREEN}[DONE] Build script completed successfully!${NC}"
