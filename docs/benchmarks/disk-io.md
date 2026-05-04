# Disk I/O Benchmark

Design 027 changes disk image attachments from the Virtualization.framework
defaults to explicit cache and synchronization policies.

## Host

- Machine: MacBook Pro, Apple M4 Max
- CPU: 16 cores, 12 performance and 4 efficiency
- Memory: 128 GB
- OS firmware: 18000.101.7
- Date: 2026-05-03

## Workload

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

## Results

| Run | Disk policy | Wall clock | Notes |
| --- | --- | ---: | --- |
| Baseline from design 027 | Framework default, Automatic + Full | 20-30 min | Ubuntu Desktop install on `linux-gui-debug`; guest `iostat -x 5` saw about 17 MB/s writes during unpacking. |
| After, fresh run | Install disk `DiskCacheEphemeral`, Cached + None | 18m44s | `real 1124.40`; VM `disk-io-after-20260503-2145`; marker `linux-installed` written at 2026-05-03T22:04:08-0700. |

The after run completed successfully and wrote `ubuntu-desktop` to
`linux-installed`.

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
