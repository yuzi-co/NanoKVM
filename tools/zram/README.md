# zram

Compressed swap in RAM, as two loadable modules against the stock kernel. No
new image, no kernel replacement, no boot-path change; `rmmod` undoes it.

## Why it is possible

```
# CONFIG_MODVERSIONS is not set     only vermagic must match, no symbol CRCs
# CONFIG_MODULE_SIG is not set      no signature enforcement
CONFIG_MODULES=y                    ~30 .ko already load from /mnt/system/ko
CONFIG_SWAP=y  CONFIG_CRYPTO_LZO=y  CONFIG_CRYPTO_ZSTD=y
# CONFIG_ZSMALLOC is not set        so zram needs zsmalloc built too
```

The board's own toolchain is the one in `tools/build` — Xuantie V2.6.1, gcc
10.2.0 — which is what `CONFIG_CC_VERSION_TEXT` on the device names.

## Why lzo-rle

`lz4` is not an option: `CONFIG_CRYPTO_LZ4` is unset, so `/proc/crypto` offers
only `deflate`, `lzo`, `lzo-rle` and `zstd`. The real choice is lzo-rle against
zstd.

The scarce resource on this board is CPU, not compression ratio: one in-order
C906 at 1GHz with no acceleration, also running H.264 encode. Compression is
paid on swap-*out*, which happens under memory pressure — exactly when that core
is already saturated. zstd costs several times more there for a better ratio.

## Reconsidered, with numbers

The reason above is a *kernel config* constraint, and this repository can now
build modules against the stock kernel — that is what `build-modules.sh` does.
So `CONFIG_CRYPTO_LZ4` could be built and lz4 offered to zram. Measured on a
live board, it would buy nothing:

```
zram offers   : lzo [lzo-rle] zstd
ratio         : 2.76x   (orig 3.21 MB -> compressed 1.17 MB)
mem_used      : 1.63 MB against the 40 MB mem_limit -> 4% of the cap
swap in use   : 3.2 MB of 96 MB
lifetime      : pswpout 40449 pages, pswpin 6503
```

Ratio is not the binding constraint: at 4% of the cap, a much worse ratio would
still fit. lz4 over lzo-rle is a few percent of compression time on a board that
idles at 5% of one core. And lzo-rle is the kernel's default for zram because of
its run-length handling of zero pages, which is most of what swap holds — lz4
has no equivalent. Against that, an extra module pinned to the kernel's vermagic
has to be rebuilt whenever the kernel moves.

If this is revisited, measure CPU during a sustained swap-out, not the ratio.
The ratio is already comfortable and is not what costs anything here.

An earlier version of this file claimed lzo-rle "reached about 5x". Today it
measures 2.76x on live pages. Both are real; they are different workloads, and
a single figure should not have been stated as the property of the algorithm.

Switching is one write, before `disksize` is set:

```shell
echo zstd > /sys/block/zram0/comp_algorithm
```

## Build and install

```shell
ssh root@<device> 'zcat /proc/config.gz' > kernel.config
tools/zram/build-modules.sh /path/to/linux_5.10 kernel.config ./ko
scp ko/*.ko root@<device>:/mnt/system/ko/
scp tools/zram/S01zram root@<device>:/etc/init.d/ && ssh root@<device> 'chmod 755 /etc/init.d/S01zram'
```

`build-modules.sh` refuses to emit modules whose vermagic does not match
`5.10.4-tag- preempt mod_unload riscv` exactly, so a module that cannot load
never reaches a device.

`loadsystemko.sh` lists modules explicitly and does not glob, so dropping files
into `/mnt/system/ko` does not make them load. `S01zram` is what enables them.

## The trade

With no disk swap behind it, exceeding zram means the OOM killer rather than
slow paging, and the largest process is `NanoKVM-Server` — so an OOM takes video
with it. `mem_limit` caps the RAM zram may consume so it cannot spiral, but the
tail behaviour is a cliff rather than a slope. On a board that measured 143
pages ever swapped, and zero under a live streaming session, that cliff is a
long way off.
