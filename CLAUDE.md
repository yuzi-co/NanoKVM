# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this repo is

Firmware and application source for NanoKVM Cube/Lite/PCIe — an IP-KVM built on the Sipeed
LicheeRV Nano (SG2002, 256MB DDR3). The SG2002 has a C906B that runs Linux and a C906L intended for
an RTOS; on this image the C906L runs nothing, so treat the board as single-core. About 96MB is
reserved before Linux starts — a 75MB fixed ION carveout for the video pipeline, plus the kernel
image and firmware regions — leaving ~158MB of usable RAM. The carveout is sized at build time by
the board's `memmap.py` and is not CMA, so none of it comes back when the capture path is idle.

The device tree reserves nothing for the RTOS core, and ION reports the whole 75MB as its own. See
`tools/README.md` for what that carveout is measured to use, and for why the boot messages about
the RTOS are expected rather than a fault. Four deliverables live here:

| Path             | What it is                                                                 |
| ---------------- | -------------------------------------------------------------------------- |
| `server/`        | Go backend (gin). Serves the API, the websockets, the video streams, and the built web UI. |
| `web/`           | React + TypeScript + Vite frontend. Built output is uploaded as `/kvmapp/server/web`. |
| `support/sg2002/`| C++ built with MaixCDK: `kvm_system` (system monitor, OLED, updates) and `kvm_vision` (`libkvm.so`, capture/encode). |
| `kvmapp/`        | The install package that lands in `/kvmapp` on the device: init scripts, kernel modules, updater, PicoClaw agent assets. |

NanoKVM-Pro (AX630C) lives in a separate repository. Everything here targets SG2002.

## Commands

### Frontend (`web/`, pnpm)

```shell
pnpm install
pnpm dev        # dev server on :3001, talks to a real device
pnpm mocked     # dev server backed by MSW handlers (src/mocks), no device needed
pnpm build      # tsc && vite build && node scripts/precompress.mjs
pnpm lint       # eslint
pnpm format     # prettier (import order is enforced by config, see .prettierrc.yaml)
```

`pnpm dev` needs a reachable device: set `VITE_SERVER_IP` in `web/.env.development`, and set
`authentication: disable` in the device's `/etc/kvm/server.yaml` (CORS blocks the login flow
otherwise). Auth changes therefore cannot be tested through `pnpm dev` — build and deploy instead.

There is no frontend test runner.

### Backend (`server/`, Go 1.24)

The device build is a RISC-V cross-compile and links against `libkvm` from `server/dl_lib` +
`server/include`, so it does not build natively on a workstation.

```shell
make app          # cross-compile NanoKVM-Server in the Docker builder
make support      # build kvm_system via MaixCDK and drop it into kvmapp/
make shell        # interactive shell inside the builder
make all          # app + support
make clean
```

The Makefile targets shell out to `id -u` and refuse to run as root — they need Docker and a
POSIX shell (Git Bash/WSL on Windows, not PowerShell). They also use `docker run -it`, so they need
a TTY and cannot be driven from a non-interactive tool call. `server/build.sh` is the same build for
a host that already has the toolchain and `patchelf` on PATH; it is also the only one of the two
that patches the RPATH, so after `make app` that step still has to be done by hand:

```shell
docker run --rm -v "$PWD/server:/src" -w /src ubuntu:24.04 \
  sh -c 'apt-get update -qq && apt-get install -y -qq patchelf \
         && patchelf --add-rpath "\$ORIGIN/dl_lib" NanoKVM-Server'
```

Both build paths stamp the binary. `common/version.Build` is set through `-ldflags -X` to
`dev.<date>.<sha>[.dirty]`, computed on the host because the builder container only sees the
checkout, not `.git`. `Decorate` attaches it to the reported application version as semver *build
metadata* (`2.4.3+dev.20260729.1023.0414ec9`), so `Settings > Update` — which compares versions with
`semver.gte` — still orders it correctly against the release feed. A prerelease suffix would sort
below the release it was built from and leave that page advertising an upgrade forever. Pass
`BUILD_STAMP=` to build unstamped the way a release does, or `BUILD_STAMP=<value>` to override.

The stamp is the only way to tell which server binary is running: the application version itself
lives in `/kvmapp/version` and is rewritten by the updater, not by deploying a binary.

For anything that does not need the real capture bindings, use the `novision` build tag — it swaps
`common/kvm_vision.go` for a pure-Go stub so the tree can be type-checked and tested off-device:

```shell
cd server
go vet -tags novision ./...
go test -tags novision ./...
go test -tags novision ./service/hid/ -run TestSomething -v
```

Without a local Go toolchain, a plain `golang` image is enough for this — the
heavy MaixCDK builder is only needed for an actual device build:

```shell
docker run --rm -v "$PWD/server:/src" -v nanokvm-gomod:/go/pkg/mod -w /src \
  -e CGO_ENABLED=0 golang:1.25 go test -tags novision ./...
```

`-race` needs `CGO_ENABLED=1`. The same image cross-checks the target arch with
`CGO_ENABLED=0 GOOS=linux GOARCH=riscv64 go build -tags novision ./...`.

A few tests are additionally gated on `//go:build linux` (`service/hid/stale_test.go`,
`service/extensions/tailscale/cli_test.go`) and silently do not run elsewhere.

### Deploying to a device

SSH must be enabled first (Web UI: `Settings > SSH`; default login `root`/`root`).

- Backend: replace `/kvmapp/server/NanoKVM-Server`.
- Frontend: rename `web/dist` to `web` and upload to `/kvmapp/server/`.
- Then `/etc/init.d/S95nanokvm restart`.

## Backend architecture

**Request flow.** `main.go` → `router.Init` → per-domain `*Router(r)` functions in `router/` →
`service/<domain>` implementations. `proto/` holds the request/response structs shared by both.

**Every route is under `/api/`.** `router/static.go` serves the web UI as gin middleware that runs
before routing; it returns early on the `/api/` prefix so API calls and websocket upgrades do not
stat the SD card and cannot be shadowed by a file on disk.

**Responses always return HTTP 200** with a `{code, msg, data}` envelope — `code: 0` means success.
Use the `proto.Response` helpers (`OkRsp`, `OkRspWithData`, `ErrRsp`) rather than `c.JSON` directly.
Parse and validate input with `proto.ParseQueryRequest` / `ParseJsonRequest`, which run
`validator/v10` over the struct tags.

**Auth is three middlewares**, all of which also enforce an origin check:

- `middleware.CheckToken` — a JWT session cookie *or* an API key (`X-API-Key` / `Authorization:
  Bearer`). This guards nearly everything.
- `middleware.CheckSession` — session only. Used for the routes that mint and revoke API keys, so a
  stolen key cannot issue successors.
- `middleware.CheckLoopbackInternalToken` — for calls the on-device PicoClaw runtime makes back into
  the server over loopback. `router.LoopbackHTTPAllowedPaths()` collects the paths that stay
  reachable over plain HTTP when HTTPS is on.

Setting `authentication: disable` in the config turns on permissive CORS in `main.go`; it exists for
local frontend development only.

**The cgo boundary** is `common/kvm_vision.go` (`//go:build !novision`), which `#cgo`-links `-lkvm`
from `server/dl_lib`. Prebuilt `.so` files (including `libkvm.so`) are committed there, so the
cross-compile needs nothing extra; rebuilding them from `support/sg2002` is only necessary when
changing `kvm_vision`. The executable's RPATH must be patched to `$ORIGIN/dl_lib` after linking —
a build that skips `patchelf` will not start on the device, and `make app` does not run it.

Linking prints `libopencv_video.so.409, needed by dl_lib/libkvm.so, not found (try using -rpath or
-rpath-link)`. This is expected: `libkvm.so` links against five OpenCV libraries, and only four of
them ship in `dl_lib` — `libopencv_video.so.409` lives in the device rootfs at `/usr/lib`, which the
cross-linker has no view of. The warning does not affect the output; the executable records only
`libkvm.so` and `libc.so` as its own `NEEDED` entries.

**Video has three delivery paths**, all under `service/stream/`: `mjpeg`, `webrtc` (H.264 over
WebRTC, with STUN/TURN configured in `server.yaml`), and `direct` (H.264 over plain HTTP). Capture is
reference-counted against viewers — `service/vm/hdmi_idle.go` stops it after an idle timeout, and
`main.go` starts the idle countdown at boot because nothing is watching yet.

**Config** is a viper-backed singleton, `config.GetInstance()`, read from `/etc/kvm/server.yaml`
with a compiled-in default if the file is missing. See `server/README.md` for the annotated schema.

**PicoClaw** (`service/picoclaw/`, `kvmapp/picoclaw/`) is an on-device AI agent runtime that drives
the KVM through HID and screenshots. The server acts as its gateway: browser-facing routes take the
normal token check, the runtime's own callbacks come back over loopback with an internal token.

## Frontend architecture

Hash router (`src/router.tsx`) with lazily imported pages; `ProtectedRoute` wraps everything but
login and the Wi-Fi provisioning page. Global state is jotai atoms in `src/jotai/`. `src/api/`
mirrors the backend domains one file per domain and goes through `src/lib/http.ts`, an axios wrapper
that unwraps `response.data` and hard-reloads on a 401.

`src/i18n/locales/` carries ~20 languages. Adding one means a new locale file plus an entry in
`src/i18n/languages.ts`; adding a string means adding it to `en.ts` at minimum.

## Device constraints worth remembering

- **Do not write runtime state under `/kvmapp`** — that is the boot SD card, and hot files wear it
  out. `kvmapp/system/init.d/S95nanokvm` shows the pattern: put the file in `/tmp` and leave a
  symlink behind so existing readers keep their path.
- Memory is scarce and mostly spoken for. Per-connection buffering and extra frame copies matter;
  several recent commits exist purely to remove them.
- Boot order is the numbered `S*` scripts in `kvmapp/system/init.d/` (USB gadget, network, then
  `S95nanokvm`, then `S96picoclaw`).

## Docs

Top-level, `server/`, `web/`, and `support/` READMEs each have `_ZH` and `_JA` variants. Keep them in
step when changing the English original.

Write the English source to ASD-STE100 (Simplified Technical English) where it fits. Those docs are
translated, and STE exists to make a source text translate predictably, so the rules earn their keep
here rather than being style for its own sake:

- One word, one meaning. Pick a term and keep it — do not alternate between "image", "build" and
  "artefact" for the same thing.
- One instruction per sentence. Keep procedure sentences under 20 words, descriptive ones under 25.
- Active voice, present tense, and a stated subject. "The updater rewrites `/kvmapp/version`", not
  "`/kvmapp/version` gets rewritten".
- Do not stack more than three nouns. "USB gadget serial number" is the limit; break longer chains
  with a preposition.
- Keep the articles and the relative pronouns. Dropping "the" or "that" saves nothing and costs the
  translator.
- Say the condition before the action: "If the marker is absent, the board boots slot A."

Where it does not fit, do not force it. Commit messages explain reasoning and often need the longer
form. Code comments explain why, which STE has no vocabulary for. Prose that argues a trade-off is
clearer unconstrained than chopped into approved words.
