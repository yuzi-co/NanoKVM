# App-only cross-compile toolchain for NanoKVM-Server.
#
# This is the golang + host_tools half of docker/Dockerfile without the MaixCDK
# stage. That stage exists to build kvm_system (`make support`) and is a large
# clone and C++ build, neither of which is needed when only the Go server
# changed. patchelf is included because the RPATH patch is required and
# `make app` does not perform it.
FROM ubuntu:24.04

ARG GO_VERSION=1.25.0
ARG HOST_TOOLS_URL=https://sophon-file.sophon.cn/sophon-prod-s3/drive/23/03/07/16/host-tools.tar.gz

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        wget ca-certificates build-essential patchelf git \
    && rm -rf /var/lib/apt/lists/*

RUN wget -q -O /tmp/host-tools.tar.gz "$HOST_TOOLS_URL" \
    && tar -C /usr/local -zxf /tmp/host-tools.tar.gz \
    && rm -f /tmp/host-tools.tar.gz

RUN wget -q -O /tmp/go.tar.gz "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" \
    && tar -C /usr/local -zxf /tmp/go.tar.gz \
    && rm -f /tmp/go.tar.gz

ENV PATH="/usr/local/go/bin:/usr/local/host-tools/gcc/riscv64-linux-musl-x86_64/bin:${PATH}"
ENV GOFLAGS="-buildvcs=false"

RUN riscv64-unknown-linux-musl-gcc -v && go version && patchelf --version
