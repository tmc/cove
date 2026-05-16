# Disk I/O Benchmark

Design 027 changes disk image attachments from the Virtualization.framework
defaults to explicit cache and synchronization policies.

## Host

- Machine: MacBook Pro, Apple M4 Max
- CPU: 16 cores, 12 performance and 4 efficiency
- Memory: 128 GB
- OS: macOS 26.4.1 (25E253), arm64
- OS firmware: 18000.101.7
- Date: 2026-05-04

## Slice 3 Host-Only Workload

Design 027 Slice 3 is about host allocation behavior, so this benchmark does
not boot a VM. It compares:

- Before-style sparse image: `hdiutil create -size 8g -type SPARSE -fs 'Case-sensitive APFS'`
- After-style raw image: `dd if=/dev/zero of=raw.img bs=8m count=1024`
- First-write workload for both: overwrite the first 1 GiB with `dd bs=1m count=1024 conv=notrunc`, then `sync`.

The 8 GiB image size keeps the benchmark short while still measuring APFS block
allocation behavior. The first-write size is 1 GiB as requested.

## Slice 3 Results

| Run | Create command | Create real | First 1 GiB write real | Total through first write | Host disk used |
| --- | --- | ---: | ---: | ---: | ---: |
| Sparse image | `hdiutil create -size 8g -type SPARSE -fs 'Case-sensitive APFS'` | 2.05s | 0.43s | 2.48s | 1.0 GiB after first write |
| Preallocated raw | `dd if=/dev/zero of=raw.img bs=8m count=1024` | 1.82s | 0.26s | 2.08s | 8.0 GiB immediately |

Raw preallocation reduced the measured first 1 GiB write from 0.43s to 0.26s
on this host, a 40% reduction for the allocation-heavy first-write portion. It
also used the full 8 GiB up front, which is why `-raw-disk` stays hidden and
default-off.

## Slice 1 Install Workload

Ubuntu 24.04 Desktop autoinstall from the cached desktop ISO:

```
/Users/tmc/.vz/cache/linux-ubuntu-desktop.iso
```

Fresh after-change command:

```
/usr/bin/time -p ./cove install -linux -desktop \
  -vm disk-io-after-20260503-2145 \
  -cpu 4 -memory 8 -disk-size 64 \
  -iso /Users/tmc/.vz/cache/linux-ubuntu-desktop.iso \
  -force
```

The binary was rebuilt and re-signed before the run:

```
go build -o cove .
codesign -s - -f --entitlements internal/autosign/vz.entitlements ./cove
```

## Slice 1 Results

| Run | Disk policy | Wall clock | Notes |
| --- | --- | ---: | --- |
| Baseline from design 027 | Framework default, Automatic + Full | 20-30 min | Ubuntu Desktop install on `linux-gui-debug`; guest `iostat -x 5` saw about 17 MB/s writes during unpacking. |
| After, fresh run | Install disk `DiskCacheEphemeral`, Cached + None | 18m44s | `real 1124.40`; VM `disk-io-after-20260503-2145`; marker `linux-installed` written at 2026-05-03T22:04:08-0700. |
| Slice 2 NVMe | Install disk `DiskCacheEphemeral`, Cached + None, NVMe controller | deferred | Not run on 2026-05-04 because the host's active VM slots were occupied by `gha-runner-mlx-go-v1` and `gha-runner-mlx-go-libs-2`. Stopping those workers is out of scope. |

The after run completed successfully and wrote `ubuntu-desktop` to
`linux-installed`.

## T36 Current Status On This Host

Current status on this host: the Ubuntu Desktop benchmark recipe is not yet
producing a stable agent-visible boot. The install-only path can stop before
the final boot boundary, `up -headless` fails on the post-install disk attach
step, and the GUI `up` path reaches Ubuntu Setup Assistant and then crashes
before `agent: available`. Until that provisioning issue is fixed, the host
cannot produce a trustworthy virtio-blk vs NVMe wall-clock comparison.

This blocks Design 027 disk I/O tuning follow-up benchmarks. Track the
first-boot reliability issue in T42 before treating new Ubuntu Desktop
wall-clock numbers as storage results.

Suggested next benchmark command after the provisioning bug is fixed:

```sh
/usr/bin/time -p ./cove up -linux -desktop -gui -user ubuntu -password '<admin-password>' \
  -cpu 4 -memory 8 -disk-size 64 -force -vm disk-io-<mode>-<timestamp>
```

Then repeat with `-nvme` and record:

- wall clock to `agent: available`
- any `iostat` or installer throughput output
- whether the VM reaches the desktop without Setup Assistant crashing

## Pending NVMe Run

Run this when a host VM slot is free:

```sh
/usr/bin/time -p ./cove install -linux -desktop -nvme \
  -vm disk-io-nvme-YYYYMMDD-HHMM \
  -cpu 4 -memory 8 -disk-size 64 \
  -iso /Users/tmc/.vz/cache/linux-ubuntu-desktop.iso \
  -force
```

Before the run, rebuild and re-sign:

```sh
go build -o cove .
codesign -s - -f --entitlements internal/autosign/vz.entitlements ./cove
```

## Interpretation

Slice 1 lands the attachment policy plumbing and removes the legacy constructor
from non-test code, but this run does not meet the `<10 min` Ubuntu Desktop
acceptance target. The current change is a measurable improvement over the
20-30 minute baseline, but it is not the full performance fix.

The remaining bottleneck needs another slice. The next discriminating benchmark
should compare:

- current install policy with guest `iostat -x 5` captured for the full install;
- `-disk-sync=full` on the same binary, to isolate synchronization mode from the
  other install changes;
- NVMe controller for Linux, from design 027 slice 2;
- preallocated raw disk image, from design 027 slice 3.

## Slice 4: -block (block device passthrough)

Slice 4 of design [027](../designs/027-disk-io-tuning.md) adds `-block` for
attaching a raw block device through `VZDiskBlockDeviceStorageDeviceAttachment`.
The unprivileged VM process never opens the device; `cove-helper` opens and
validates the node, then passes the file descriptor over `SCM_RIGHTS`.

Modes:

- `:ro` — read-only attachment. Host may keep the device mounted.
- `:rw` — read-write. Helper refuses to continue if the device is mounted.
  Defaults to full synchronization.
- `:rw:sync=none` — read-write with synchronization disabled. Explicit operator
  opt-in; risks data loss on host crash.

Status: spec doc; live results pending. No `-block` numbers were captured in
this round because the host has no spare raw block device to dedicate to a VM.
The numbers below are placeholders so that future runs can drop measured values
into the same table.

| Mode | Device | Workload | Wall clock | fio seq write | fio rand write | fio fsync-heavy |
| --- | --- | --- | ---: | ---: | ---: | ---: |
| `:ro` | pending | pending | pending | pending | pending | pending |
| `:rw` (sync=full) | pending | pending | pending | pending | pending | pending |
| `:rw:sync=none` | pending | pending | pending | pending | pending | pending |

### How to bench

Pick a raw block device the host can dedicate (an external USB SSD, a
Thunderbolt enclosure, or an `hdiutil attach -nomount` image). Resolve the raw
node (`/dev/rdiskN`) via `diskutil list`. Then:

```sh
# Read-only attach
./cove run -linux -block /dev/rdiskN:ro -vm block-ro-YYYYMMDD-HHMM

# Read-write attach, full sync (default)
./cove run -linux -block /dev/rdiskN:rw -vm block-rw-YYYYMMDD-HHMM

# Read-write, sync=none (explicit opt-in; data-loss risk on host crash)
./cove run -linux -block /dev/rdiskN:rw:sync=none -vm block-rwn-YYYYMMDD-HHMM
```

Inside the guest, run `fio` with sequential write, random write, and an
fsync-heavy profile against the resolved device. Record host model, OS build,
device class (internal / USB / Thunderbolt / hdiutil-backed), exact cove
command line, guest-visible device name, and whether `sync=full` or
`sync=none` was used, per design 027 §Slice 4.

Rebuild and re-sign before each run:

```sh
go build -o cove .
codesign -s - -f --entitlements internal/autosign/vz.entitlements ./cove
```
