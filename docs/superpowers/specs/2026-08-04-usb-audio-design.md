# USB audio capture for NanoKVM

Date: 2026-08-04
Status: approved, ready for an implementation plan

## Summary

NanoKVM carries no audio today. This design adds one direction only: sound produced by the managed
host reaches the operator's browser. The host sends it over USB, because the HDMI path cannot carry
it on this board. The server reads the audio from an ALSA capture device, encodes it as G.711
mu-law, and sends it as a second track on the existing WebRTC connection.

## Why USB and not HDMI

The LT6911 bridge converts HDMI to CSI, and CSI is a video port. Sipeed states the board does not
wire the audio pins: "NanoKVM does not support this feature in hardware, and there is a possibility
that USB Audio will be used in the future" (sipeed/NanoKVM issue 100).

Measurements on the device agree. The only sound cards are the SoC's own analog codec —
`cv182xaadc` at `0x300a100` and `cv182xadac` at `0x300a000`. The I2S controller `4100000.i2s` is
rx-capable and probes, but no card binds to it and no pinctrl entry claims its pins. The LT6911
reaches the SoC over I2C bus 4 and nothing else. The same chip does carry I2S audio on GL.iNet's
KVM, so the limit is the board, not the bridge.

The USB path is available. The kernel sets `CONFIG_USB_CONFIGFS_F_UAC1=y` (UAC2 is not set). A
`uac1.usb0` function, linked into the running gadget beside three HID functions and ACM, binds
without error and produces a third ALSA card:

```
 2 [UAC1Gadget]: UAC1_Gadget
02-00: UAC1_PCM : playback 1 : capture 1
```

`hw:UAC1Gadget,0` capture is audio the host sends to us. That is the direction this feature needs.

## Scope

In scope:

- Listen only. The operator hears the host.
- WebRTC only. The mjpeg and direct video paths get no audio.
- Opt-in, and switched from the web UI. A `/boot/usb.uac` marker gates the gadget function, and the
  existing virtual-device toggle turns it on and off.

Out of scope, and deliberately so:

- Talking back to the host (browser microphone to `hw:UAC1Gadget,0` playback).
- Audio on the mjpeg and direct paths.

## Constraints that shaped the design

- The gadget offers exactly `S16_LE`, 48000 Hz, 2 channels. It offers nothing else, so any rate
  change is ours to make.
- The board has one usable core and about 158 MB of RAM.
- The capture loop must never wait on a viewer. The existing video path enforces this with a
  one-frame slot per client, and audio follows it.
- Audio must not hold the HDMI capture lease. It comes from USB and is independent of video.

## Architecture

### New package: `server/service/stream/audio`

This package knows nothing about WebRTC. It produces 20 ms frames of mu-law and stops.

| File | Responsibility |
| ---- | -------------- |
| `audio.go` | The package API: `Available() bool`, `Start() error`, `Stop()`, and `Frames() <-chan []byte`. Each frame is 20 ms of mu-law. `Stop` kills the child and closes the channel. |
| `source.go` | Owns the `arecord` child process. Restarts it with backoff. Emits raw 48 kHz stereo `S16_LE` chunks. |
| `resample.go` | Downmixes stereo to mono and decimates 6:1 through an anti-alias FIR with a passband edge at 3.4 kHz and stopband rejection from 4 kHz, the Nyquist limit of the 8 kHz output. Pure functions. |
| `g711.go` | Encodes mu-law. Pure functions. |

### Changes to `server/service/stream/webrtc`

- `audio.go` (new). `sendAudioStream()` mirrors `sendVideoStream()`: take a frame, packetize it once
  with `codecs.G711Payloader`, hand the packets to every client.
- `client.go`. `AddTrack()` adds a PCMU track when audio is available.
- `types.go`. `Client` gains a second `FrameSlot` and its own writer goroutine. `Track` gains an
  `audio rtpWriter`.

Audio gets its own slot rather than sharing the video one. A client that falls behind on video must
not lose its audio as well.

### Changes outside the server

- `kvmapp/system/init.d/S03usbdev` creates `uac1.usb0` when `/boot/usb.uac` exists, and links it
  **last**. configfs numbers interfaces in link order, so a function inserted ahead of the HID ones
  renumbers the keyboard and the mice under a host that is already bound to them. The file already
  carries this warning for the ACM function.
- `server/service/vm/virtual-device.go` gains an `audio` case beside `network` and `disk`.
  `server/proto` gains an `Audio` field in the response and accepts `audio` in the request. The
  route does not change.
- `web/src/api/virtual-device.ts` and
  `web/src/pages/desktop/menu/settings/device/virtual-devices.tsx` gain a third row.
- `web/src/pages/desktop/screen/h264-webrtc.tsx` sets `offerToReceiveAudio: true`, attaches the
  inbound track, and starts **muted**. Browsers block autoplay with sound until the user acts.
- A speaker toggle in the desktop menu, with strings added to `web/src/i18n/locales/en.ts`.

## Data flow

```
host PC ──USB──▶ uac1.usb0 ──▶ hw:UAC1Gadget,0 ──▶ arecord ──▶ pipe
                                                                │
                        48 kHz stereo S16_LE, 3840 B per 20 ms  ▼
                                              downmix + 6:1 decimate
                                                                │
                                       8 kHz mono, 160 samples  ▼
                                                       mu-law encode
                                                                │
                                                  160 B per 20 ms ▼
                              codecs.G711Payloader ──▶ per-client slot
                                                                │
                                                                ▼
                                                    PCMU track, 50 pkt/s
```

The RTP clock rate for this track is 8000, and each packet carries 160 samples. Payload bandwidth is
64 kbit/s before RTP overhead.

## Enabling, availability, start and stop

The operator turns audio on from `Settings > Device`, beside the existing virtual network and
virtual disk switches. `UpdateVirtualDevice` already does exactly this job for those two, and audio
is a third case rather than a new mechanism:

```
touch /boot/usb.uac
/etc/init.d/S03usbdev stop
/etc/init.d/S03usbdev start
```

Turning it off removes the **symlink** and the marker, then restarts:

```
/etc/init.d/S03usbdev stop
rm -rf /sys/kernel/config/usb_gadget/g0/configs/c.1/uac1.usb0
rm /boot/usb.uac
/etc/init.d/S03usbdev start
```

Two properties of that existing code matter here, and neither is accidental. The handler holds the
HID lock and calls `CloseNoLock` before the commands and `OpenNoLock` after, so the HID service is
not holding `/dev/hidg*` open while the gadget is rebuilt. And the teardown removes the config
symlink but never `rmdir`s the function directory. Removing a function directory blocks until every
holder of its character device closes it, which is how a `rmdir` of `acm.GS0` hangs forever against
the `ttyGS0` getty that `/etc/inittab` respawns.

Because `S03usbdev start` relinks in script order and `uac1.usb0` is created last in the script, HID
interface numbering survives the rebuild.

Applying the switch drops USB to the host for about a second. The operator's keyboard and mouse come
back by themselves; the host may need a moment to notice the new sound device, and reselecting the
output device on the host is a manual step in any case.

Audio availability is therefore **not** fixed for the life of the process, and the server must not
cache it at start. It reads `/proc/asound/cards` and looks for `UAC1Gadget` when a client negotiates
a connection. That is one small file read per WebRTC client, which is nothing.

There is no new API route and no new configuration key. When audio is available, the PCMU track is
in the offer, and the browser's `ontrack` event is the availability signal. When it is not, no track
arrives and the UI shows no speaker. Nothing has to be negotiated or versioned. A browser that was
already connected when the switch was thrown keeps its old set of tracks until it reconnects.

Start and stop mirror the video path. The first client starts the stream under an `audioSending`
guard, and the departure of the last client stops it.

**Stopping must kill the child process.** When the host plays nothing, `arecord` blocks in `read`,
so the loop does not tick and cannot notice that it is idle. Killing the child is what returns EOF
to the reader and lets the goroutine exit. A stop path copied from the video loop, which relies on
the next tick, would hang here.

## Error handling

| Condition | Behaviour |
| --------- | --------- |
| `UAC1Gadget` card absent | No track offered to that client. Rechecked on the next connection, because the switch can add the card at any time. |
| `arecord` binary absent | Audio off for the life of the process. Logged once. |
| `arecord` exits during a stream | Restart with backoff, doubling from 200 ms to a 5 s cap. After five restarts inside one minute, mark audio failed for the life of the process and stop offering the track. |
| Host plays nothing | The read blocks. This is the normal state. No packets, no CPU, silence in the browser. |
| Client falls behind | The slot drops one 20 ms frame, which is one click. Counted the way dropped video frames are counted. |
| Last viewer leaves, or the server stops | Kill the child, wait for it, reap it. No zombie. |

## Cost

`arecord` measures 1232 kB RSS on the device. The FIR runs about 48000 multiply-accumulate
operations per second. Both are small next to the 75 MB ION carveout the video pipeline holds.

## Testing

`resample.go` and `g711.go` are pure and get table tests: a known sine downmixed and decimated, and
mu-law encoding checked against the ITU G.711 reference values. The 6:1 decimator is tested for
attenuation above 4 kHz, because rejecting that band is the reason it exists.

`source.go` takes the command it runs as a field, so a fake command exercises the restart path, the
backoff, and the give-up threshold without ALSA.

The WebRTC additions follow the existing `track_test.go` and `client_test.go` patterns: packetize a
known frame, confirm every client receives it, and confirm a client that does not drain its slot
drops frames instead of blocking the sender.

`UpdateVirtualDevice` gets a test for command selection only: the `audio` case must choose the
mount list when the marker is absent and the unmount list when it is present. The commands
themselves shell out to the device and are not run in a test.

All of this runs off-device:

```shell
cd server
go test -tags novision ./service/stream/...
```

There is no frontend test runner, so the browser side is checked by hand against a device.

## Alternatives considered

**`plughw:UAC1Gadget,0` instead of our own resampler.** ALSA's plug layer would convert 48 kHz
stereo to 8 kHz mono inside `arecord`, and `resample.go` would not exist. It is rejected because
alsa-lib's rate conversion is a linear interpolator with no anti-alias filter. Aliasing a 5 kHz beep
down into the passband is the failure this feature would be blamed for, and beeps are the main thing
people want to hear.

**Opus through cgo.** Full 48 kHz stereo quality. It is rejected for a first version because it
means cross-building libopus for riscv64, committing the library to `server/dl_lib`, patching its
RPATH and `NEEDED` entries the way `CLAUDE.md` describes for `libkvm.so`, and writing a `novision`
stub so off-device tests still build. That is a build-system change, not a feature change. The
encoder sits behind one function, so this stays a later swap.

**A pure-Go ALSA library.** It removes the child process and gives direct control over underruns,
at the cost of trusting an unproven dependency with a `/dev/snd` handle on a board whose first
requirement is that it stays reachable.

**Audio over a separate websocket, for all video modes.** It would reach mjpeg and direct users, but
it means writing jitter buffering and drift correction by hand. WebRTC already does both.

## Risks

The managed host must select NanoKVM as its output device. HDMI audio would have been automatic;
this is not. Worse, Windows and Linux both tend to move default output to a newly attached USB sound
device, which is the reason the function is opt-in rather than on by default.

Quality is telephone grade: 8 kHz, mono. It suits beeps, alarms, and speech. It does not suit music.

Throwing the switch rebuilds the USB gadget, which drops HID to the host for about a second. The
virtual network and virtual disk switches already carry that cost, so the behaviour is not new, but
it is still a reason not to throw it while someone is typing into the host.

The reverse risk is worse and is why the teardown removes only the symlink: a `rmdir` of a function
directory whose character device is still open never returns, and recovering from that state needs a
full teardown of the gadget followed by `S03usbdev start`.
