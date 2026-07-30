# OLED

Protect the status panel from burn-in, without changing `kvm_system`.

## Why this panel is exposed

The NanoKVM status screen is close to the worst case for an OLED. It shows the
same labels in the same pixels for the whole life of the device: `HDMI`, `USB`,
`ETH`, an IP address, a resolution. An OLED pixel dims as it is driven, so
pixels that are always lit lose brightness against pixels that never are, and
the layout stays visible as a ghost after the content changes.

Three levers exist, and they are independent:

| lever            | where it lives                    | effect                       |
| ---------------- | --------------------------------- | ---------------------------- |
| screen off       | `/etc/kvm/oled_sleep`, Web UI     | the strongest, all or nothing |
| move the image   | `S97oled-nudge` in this directory | spreads the wear             |
| lower the drive  | SSD1306 contrast, command `0x81`  | slows the wear everywhere    |

The sleep timer already exists and defaults to 3600 seconds. Shortening it is
one write and costs nothing:

```shell
echo 300 > /etc/kvm/oled_sleep         # or use Settings in the Web UI
```

## How the nudge works

The SSD1306 display offset is a property of the controller, not of the frame
buffer. Command `0xD3` selects which COM line row zero drives, so the whole
image moves and the drawing code never knows. `kvm_system` writes that register
once in `OLED_Init` and never again, so a value set from outside survives every
redraw.

That is the reason this needs no rebuild of `kvm_system`. A content shift inside
the drawing code would work too, and it would not wrap - see the limit below -
but it needs the MaixCDK builder and a replacement binary.

`S97oled-nudge` walks the offset 0, 1, 2, 1, 0 and back again, one row at a
time, by default every 600 seconds. The walk is a triangle rather than a
saw-tooth: a saw-tooth snaps the whole image back in one step, which reads as a
glitch rather than as a screen that does not sit still.

## Install

```shell
scp tools/oled/S97oled-nudge root@<device>:/etc/init.d/S97oled-nudge
ssh root@<device> 'chmod 755 /etc/init.d/S97oled-nudge && /etc/init.d/S97oled-nudge start'
```

The number puts it after `S95nanokvm`, which starts `kvm_system` and therefore
initialises the display. The first move happens one period later in any case.

```shell
/etc/init.d/S97oled-nudge status        # panel, travel, period, running
/etc/init.d/S97oled-nudge demo 3        # walk 0..3..0 with a second between rows
/etc/init.d/S97oled-nudge stop          # stops, and puts the offset back to 0
```

Use `demo` to judge the movement on the panel. If the screen has gone to sleep,
wake it first, or the walk happens with the display off.

Environment: `OLED_NUDGE_MAX` rows of travel, 0 disables movement;
`OLED_NUDGE_PERIOD` seconds between moves; `OLED_BUS` and `OLED_ADDR` to skip
detection.

## Which bus and address

The script probes, and reports what it found. Do not assume:

| board            | `/etc/kvm/hw` | bus | address |
| ---------------- | ------------- | --- | ------- |
| Cube, Lite       | `beta`        | 5   | `0x3d`  |
| earlier revision | `alpha`       | 1   | `0x3d`  |
| PCIe             |               | 5   | `0x3c`  |

`kvm_system` builds both `oled_alpha(1)` and `oled_beta(5)` and picks by
hardware version. Probing is safer than reading `/etc/kvm/hw`, because a wrong
guess sends display commands to whatever else answers at that address. On this
board `i2cdetect` also finds `0x2b` and `0x44` on bus 4.

## The limit: the shift wraps

The offset moves the whole image and wraps. Rows pushed off the bottom reappear
at the top. With a travel of one or two rows on a 64 row panel that is invisible
while the bottom rows are blank, and obvious if they are not. Look at the screen
once with `demo` before you raise the travel.

If a larger travel is wanted without wrapping, the shift has to move the content
instead, which means `kvm_system`. `make support` builds it, and it can be
driven without a TTY the way `tools/build` drives the app build - the `-it` in
the Makefile is the only reason it appears to need one.

## The cost, and one race worth knowing

One I2C transaction per period. `kvm_system` writes the panel far more often
than that.

It sends each command byte as its own transaction. `oled_write_register(OLED_CMD,
0xD3)` and the value after it are two separate writes on the bus, so a nudge can
land between a command and its argument. The worst case is one malformed frame,
corrected by the next redraw. A long period keeps it rare. There is no way to
hold an I2C bus against another process, so the race cannot be closed from here
- only made unlikely and harmless.

## Tests

```shell
sh tools/oled/test-nudge.sh                        # the repository copy
sh /tmp/test-nudge.sh /etc/init.d/S97oled-nudge    # what is installed, on the board
```

`test-nudge.sh` extracts the offset walk and the probe order from the script
with `sed`, so the test cannot drift from what ships. Run it on the board as
well: busybox `ash` is not the shell it was written in.

## One trap

The nudge loop never returns, so it must not inherit the calling shell's stdio.
Started over ssh without `< /dev/null > /dev/null 2>&1` it holds the file
descriptor open and the ssh command never comes back. `tools/build/README.md`
records the same trap for `S95nanokvm`.
