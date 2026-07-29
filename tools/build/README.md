# App-only builder

`make app` builds the Go server inside the full MaixCDK builder image, which
also carries the C++ SDK needed for `make support`. When only `server/` changed,
that image is a large download for nothing, and `make app` uses `docker run -it`
so it needs a TTY and cannot be driven non-interactively.

This is the same cross-compile without the SDK, plus the `patchelf` step that
`make app` does not perform.

```shell
docker build -t nanokvm-app-builder tools/build

docker run --rm \
  -v "$PWD/server:/src" -v "$PWD/tools/build:/build" \
  -v nanokvm-gopath:/gopath -v nanokvm-gocache:/gocache \
  -w /src -e GOPATH=/gopath -e GOCACHE=/gocache \
  -e BUILD_STAMP="dev.$(date +%Y%m%d.%H%M).$(git rev-parse --short HEAD)" \
  nanokvm-app-builder sh /build/build-app.sh
```

The named volumes cache Go modules and build output, so rebuilds are quick.

Deploy the binary alone — leave the device's `dl_lib` untouched:

```shell
scp server/NanoKVM-Server root@<device>:/kvmapp/server/NanoKVM-Server
ssh root@<device> "setsid sh -c '/etc/init.d/S95nanokvm restart > /tmp/nanokvm.log 2>&1' < /dev/null > /dev/null 2>&1 &"
```

`S95nanokvm restart` backgrounds the server with `&`, so it inherits the ssh
session's stdout and the connection never closes. Worse, killing that ssh can
take the server down with a SIGPIPE. `setsid` with the output redirected
detaches it properly. `/tmp` is tmpfs, so the log costs no SD wear.
