# Design 027: Disk I/O Performance Tuning

Status: Implemented (2026-05-04)
Author: Travis Cline
Date: 2026-05-03

## Problem

Linux installs on cove are **5-10x slower than they should be** on Apple Silicon hardware. Concrete measurement during an Ubuntu 24.04 desktop install on `linux-gui-debug`:

```
iostat -x 5 (inside guest, during cmd-system-install/unpacking ubuntu-desktop):
Device: vda    kB_wrtn/s = 17094.53    kB_dscd/s = 122244.06
```

**~17 MB/s sustained write throughput.** A native APFS sparse file on M-series internal SSD does 500-2000 MB/s for the same workload pattern. We are getting roughly 1/30th of native throughput.

This explains the user-visible symptom that started this investigation: Ubuntu 24.04 desktop autoinstall takes **20-30 minutes** under cove, even on idle hardware. NotebookLM independently confirmed this duration is "the standard expected duration for an unattended Ubuntu 24.04 Desktop installation" — meaning everyone running this stack hits the same wall, not just us.

## Root cause

Today every `VZDiskImageStorageDeviceAttachment` in cove is constructed via:

```go
attachment, err := vz.NewDiskImageStorageDeviceAttachmentWithURLReadOnlyError(url, readOnly)
```

This constructor takes the framework defaults:

- **Caching mode:** `VZDiskImageCachingModeAutomatic` — framework decides; in practice this is conservative.
- **Synchronization mode:** `VZDiskImageSynchronizationModeFull` — every guest fsync calls down to APFS-level fsync, which on a sparse-bundle-backed file is expensive (block allocation + journal commit per sync).

The Apple Virtualization framework offers a longer-form constructor:

```go
attachment, err := vz.NewDiskImageStorageDeviceAttachmentWithURLReadOnlyCachingModeSynchronizationModeError(
    url,
    readOnly,
    cachingMode,        // VZDiskImageCachingMode: Automatic | Uncached | Cached
    synchronizationMode, // VZDiskImageSynchronizationMode: Full | Fsync | None
)
```

We never call the longer form. As a result, an Ubuntu installer issuing thousands of fsync calls (one per file extracted, plus journal commits, plus discard/TRIM operations during ext4 setup) gets serialized through the host's slowest sync path.

## The matrix

| Caching | Sync | Behavior | Use case |
|---|---|---|---|
| Automatic | Full | Framework picks; safe | Today's default |
| Cached | Full | Host caches reads/writes; full fsync | Run-time durable VMs |
| Cached | Fsync | Host caches; best-effort sync | **Recommended default** |
| Cached | None | Host caches; no sync | Installer / ephemeral / fork-from |
| Uncached | None | Direct, no sync | Benchmark only |

## Proposal

### Slice 1: Per-attachment policy (this design)

Introduce a `DiskCachePolicy` enum and route every existing `NewDiskImageStorageDeviceAttachmentWithURLReadOnlyError` callsite through a new helper that picks caching+sync based on the policy.

```go
// In a new file: disk_attachment.go

package main

import (
    vz "github.com/tmc/appledocs/generated/virtualization"
)

type DiskCachePolicy int

const (
    DiskCacheDurable    DiskCachePolicy = iota // Cached + Fsync — for long-lived disks
    DiskCacheEphemeral                         // Cached + None  — for installs, fork-from children, scratch
    DiskCacheReadOnly                          // Automatic + Full — for ISOs (read-only, sync irrelevant)
)

func newDiskAttachment(url *foundation.NSURL, readOnly bool, policy DiskCachePolicy) (*vz.VZDiskImageStorageDeviceAttachment, error) {
    var caching vz.VZDiskImageCachingMode
    var sync vz.VZDiskImageSynchronizationMode
    switch policy {
    case DiskCacheEphemeral:
        caching = vz.VZDiskImageCachingModeCached
        sync = vz.VZDiskImageSynchronizationModeNone
    case DiskCacheDurable:
        caching = vz.VZDiskImageCachingModeCached
        sync = vz.VZDiskImageSynchronizationModeFsync
    case DiskCacheReadOnly:
        caching = vz.VZDiskImageCachingModeAutomatic
        sync = vz.VZDiskImageSynchronizationModeFull
    }
    return vz.NewDiskImageStorageDeviceAttachmentWithURLReadOnlyCachingModeSynchronizationModeError(
        url, readOnly, caching, sync,
    )
}
```

### Callsite migration

| File:line | Current | Proposed policy |
|---|---|---|
| `linux_installer.go:456` (install root disk) | URLReadOnly | `DiskCacheEphemeral` (durability irrelevant during install — if we crash, restart) |
| `linux_installer.go:469` (cidata cloud-init iso) | URLReadOnly readOnly=true | `DiskCacheReadOnly` |
| `linux_installer.go:479` (install ISO) | URLReadOnly readOnly=true | `DiskCacheReadOnly` |
| `linux.go:92` (run-time root disk) | URLReadOnly | `DiskCacheDurable` |
| `linux.go:98` (run-time ISO if any) | URLReadOnly readOnly=true | `DiskCacheReadOnly` |
| `installer.go:1405` (macOS install root disk) | URLReadOnly | `DiskCacheEphemeral` |
| `macos.go:727` (macOS run-time root disk) | URLReadOnly | `DiskCacheDurable` |
| `system_disk.go:82` (recovery disk) | URLReadOnly | `DiskCacheReadOnly` if RO else `DiskCacheDurable` |
| `usb.go:67` (USB attach) | URLReadOnly | `DiskCacheDurable` if RW, `DiskCacheReadOnly` if RO |
| `windows.go:244` | URLReadOnly | `DiskCacheDurable` (but Windows uses NVMe controller — separate path) |
| `internal/windows/installer.go:227,243,255` | URLReadOnly | install = `DiskCacheEphemeral`, EFI/ISO = `DiskCacheReadOnly` |

Plus matching changes in `runtime` callsites that hot-attach disks during a running VM (`control_runtime_disk.go`, etc.) — those default to `DiskCacheDurable` because the user expects durability for runtime adds.

### CLI escape hatch

Add a hidden flag for power users / benchmarking:

```
cove run -disk-sync=fsync   # default for run-time
cove run -disk-sync=none    # for ephemeral / fork-from-and-throw-away
cove run -disk-sync=full    # legacy / extra-paranoid
```

Hidden by default; documented in `docs/runbooks/disk-tuning.md`.

### Test plan

1. **Unit:** policy → enum mapping table-test.
2. **Integration:** `cove install -linux` → measure wall-clock to power-off. Baseline (today, Full sync) vs. proposed default (`DiskCacheEphemeral` for install). Acceptance: ≥2x speedup on Ubuntu 24.04 desktop install.
3. **Crash safety smoke:** start a VM with `DiskCacheDurable`, `kill -9` cove, confirm guest filesystem mounts cleanly on next boot.
4. **No regression:** existing macOS install + run path unchanged in user-visible behavior, just faster.

## Follow-up slices

- **Slice 2 — NVMe controller for Linux**: Implemented (benchmark deferred). Hidden `-nvme` flag on Linux run/install paths, wrapping root disk attachments with `VZNVMExpressControllerDeviceConfiguration` instead of virtio-blk. The install benchmark is still pending a free host VM slot.
- **Slice 3 — Pre-allocated RAW images** instead of sparse: `hdiutil create -fs UDIF` writes a sparse bundle; switching to `dd if=/dev/zero` style raw allocation eliminates APFS first-write pauses. Cost: disk image is its full size on host immediately. Worth it for benchmark images, not for casual user disks.
- **Slice 4 — Block device passthrough** (`VZDiskBlockDeviceStorageDeviceAttachment`): bypass APFS entirely, attach `/dev/rdiskN`. Needs root helper (we already have `cove-helper`); biggest architectural lift; max performance.

## Risks

- **Data loss on host crash:** `DiskCacheEphemeral` (`SynchronizationModeNone`) means a host kernel panic during install leaves a corrupt disk. Mitigation: install workflow already restarts on failure, and we name the policy `Ephemeral` so callers self-select. Run-time disks stay on `Fsync`.
- **Behavior change in run path:** moving from `Automatic+Full` to `Cached+Fsync` is a real semantic change for users who pull plugs. We claim `Fsync` is "best-effort sync" per Apple's docs, which matches what most filesystems expect, but this should be flagged in changelog.
- **Discard/TRIM amplification:** the 122 MB/s discard rate observed in iostat is curtin laying down ext4 + initial fstrim. With `SynchronizationModeNone`, those discards are batched in host cache and may not reach the underlying APFS sparse bundle promptly — meaning host disk usage can lag guest reality. This is a feature for ephemeral installs (faster), and irrelevant since we throw the install state away on power-off anyway.

## Acceptance

- [ ] `disk_attachment.go` lands with `DiskCachePolicy` + helper
- [ ] All 11 callsites migrated, no remaining `NewDiskImageStorageDeviceAttachmentWithURLReadOnlyError` in non-test code
- [ ] `-disk-sync` flag wired on `cove run` and `cove install`
- [ ] Benchmark recorded in `docs/benchmarks/disk-io.md`: before-N, after-N, on M-series host
- [ ] Ubuntu 24.04 desktop install completes in <10 min on idle host (was 20-30 min)

## Slice 2 acceptance

- [x] Linux root disks can be attached through NVMe with `VZNVMExpressControllerDeviceConfiguration`
- [x] Hidden `-nvme` flag wired on Linux run/install paths
- [ ] Ubuntu Desktop install benchmark recorded for virtio-blk vs. NVMe on this host

## References

- NotebookLM session 2026-05-03 (notebook `6e768319-9962-48a6-9fef-71db2dceed84`):
  - Confirmed enum constants: `VZDiskImageCachingMode{Automatic=0, Uncached=1, Cached=2}`, `VZDiskImageSynchronizationMode{Full=1, Fsync=2, None=3}`
  - Identified gap vs. UTM: UTM also uses default mode but exposes NVMe toggle; cove can leapfrog by exposing caching/sync directly
- Apple docs: [VZDiskImageStorageDeviceAttachment](https://developer.apple.com/documentation/virtualization/vzdiskimagestoragedeviceattachment)
- Code-Hex/vz API: `NewDiskImageStorageDeviceAttachmentWithURLReadOnlyCachingModeSynchronizationModeError`
- Project memory: `project_linux_vm_followups.md` (workstream #2 — disk I/O performance verification)
