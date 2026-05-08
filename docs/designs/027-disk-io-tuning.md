# Design 027: Disk I/O Performance Tuning

Status: Implemented (2026-05-04; Slices 1-4 shipped). Last verified R71 (2026-05-08).

- Slice 1 (DiskCachePolicy + `-disk-sync` + callsite migration): `fc7ff1e`.
- Slice 2 (Linux NVMe wiring + `-nvme`): `8500ecb`, `968cbde`; benchmark deferred at `1b7c947` / `f71851b`.
- Slice 3 (preallocated raw install disk + `-raw-disk`): `7ca06a9`; benchmark recorded at `093d63d`.
- Slice 4 (block device passthrough): helper protocol `b522ab3`, run wiring `a78e891`, smoke runbook `74d9527`, final spec `65b6964`, benchmark blocker `50ba06e`.

Carried-forward open items:
- Slice 1 acceptance: Ubuntu 24.04 desktop install <10 min wall-clock — still gated on Ubuntu Desktop first-boot reliability (see `50ba06e`).
- Slice 2 acceptance: virtio-blk vs. NVMe install benchmark — deferred for the same reason.
- Slice 4 acceptance: `docs/benchmarks/disk-io.md` row separated from desktop reliability — pending the same gate.
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

## Shipped state before Slice 4

Slice 1 is the required baseline for all later work. It shipped
`DiskCachePolicy`, explicit Virtualization.framework cache/sync selection,
`-disk-sync`, and the callsite migration away from the legacy
`NewDiskImageStorageDeviceAttachmentWithURLReadOnlyError` constructor.

Slice 2 shipped Linux NVMe controller wiring. The hidden `-nvme` flag can attach
Linux root disks through `VZNVMExpressControllerDeviceConfiguration` instead of
virtio-blk on install and run paths. The installer benchmark is still deferred
because the host's Ubuntu Desktop first-boot path was not reliable enough to
separate storage performance from provisioning failures.

Slice 3 shipped preallocated raw install disk images. The hidden
`cove install -raw-disk` path creates full-size raw images up front rather than
sparse files. The host-only benchmark in `docs/benchmarks/disk-io.md` recorded
the allocation tradeoff: faster first write, immediate full host disk usage.

The remaining performance ceiling is the host filesystem. Even with explicit
cache/sync policy, NVMe, and raw preallocation, image-backed disks still flow
through APFS file metadata and allocation behavior. Slice 4 is the final design
slice for bypassing that layer when the operator deliberately supplies a raw
macOS block device.

## Slice 4: Block device passthrough final spec

Slice 4 uses `VZDiskBlockDeviceStorageDeviceAttachment` to attach an already
opened raw device such as `/dev/rdiskN` to a Linux guest. This is the maximum
performance path in the disk I/O tuning stack because guest reads and writes no
longer target an APFS-hosted disk image file.

Design [028](028-block-device-passthrough.md) is the detailed architecture for
the helper protocol, descriptor passing, validation, and CLI surface. This
section is the Design 027 final slice contract: when Slice 4 is considered part
of disk I/O tuning, what it must do, what it must not do, and how it should be
benchmarked against Slices 1-3.

### Scope

Slice 4 is a runtime attach feature first:

```sh
cove run -linux -block /dev/rdisk8:ro
cove run -linux -block /dev/rdisk8:rw
cove run -linux -block /dev/rdisk8:rw:sync=full
cove run -linux -block /dev/rdisk8:rw:sync=none
```

It does not make raw block devices the default root disk path. Image-backed
root disks remain the normal cove experience because they support cloning,
fork-from-image, artifacts, cleanup, and predictable disk files under
`~/.vz/vms/`. Slice 4 is for benchmark rigs, removable media workflows, and
appliance-style runs where the operator explicitly chooses host-device
ownership over cove-managed image files.

Slice 4 should not add `cove install -target /dev/rdiskN` in this design. A
destructive installer target needs its own confirmation UX, partition-table
policy, and recovery story. The safe first step is attaching extra raw devices
to a guest that already boots from a cove-managed root disk.

### Privilege and safety boundary

The VM process must stay unprivileged. Raw device opens belong in `cove-helper`,
which already runs as root and authenticates the requesting user. The helper
opens the device, validates that it is safe for the requested mode, and passes
the descriptor back to cove over the Unix socket with `SCM_RIGHTS`.

Writable requests must be conservative:

- path must be absolute and under `/dev/`
- writable paths must use raw disk names such as `/dev/rdiskN`
- target must be a device node, not a regular file or symlink escape
- mounted devices must be rejected
- APFS container members and synthesized volumes should be rejected unless a
  future force flag has a separate design
- helper freshness must be checked before VM start so stale helpers fail early

Read-only mode may be looser, but only after tests prove the
Virtualization.framework accepts the candidate device form. The default operator
guidance should still prefer `/dev/rdiskN`.

### Sync policy

Block devices use `VZDiskSynchronizationMode`, not
`VZDiskImageSynchronizationMode`. The defaults are intentionally stricter than
the install-image fast path:

| Spec | Mode | Sync | Reason |
|---|---|---|---|
| `:ro` | read-only | `Full` | Conservative; sync is irrelevant for read-only devices. |
| `:rw` | read-write | `Full` | Operator asked for a real device; default to host-crash durability. |
| `:rw:sync=none` | read-write | `None` | Explicit benchmark/appliance opt-in only. |

No automatic policy should select `sync=none` for writable block devices. The
operator must spell it out.

### Benchmark contract

Slice 4 should produce a benchmark row in `docs/benchmarks/disk-io.md` only
after the Ubuntu Desktop first-boot reliability issue is no longer contaminating
wall-clock measurements. Until then, block-device benchmarks should be framed
as storage microbenchmarks, not install UX claims.

Minimum benchmark matrix:

| Path | Controller | Host backing | Goal |
|---|---|---|---|
| Slice 1 default | virtio-blk | sparse image, explicit cache/sync | current safe baseline |
| Slice 2 | NVMe | sparse image, explicit cache/sync | controller comparison |
| Slice 3 | virtio-blk or NVMe | preallocated raw image file | APFS allocation comparison |
| Slice 4 | virtio-blk | `/dev/rdiskN` block device | APFS-image bypass comparison |

Each benchmark must report:

- host model, OS build, storage medium, and whether the block device is internal,
  external USB, Thunderbolt, or disk-image-backed through `hdiutil`
- exact cove command line
- guest-visible device name
- `fio` or equivalent sequential write, random write, and fsync-heavy workload
- wall-clock install timing only when the guest reaches agent readiness
- whether `sync=full` or `sync=none` was used

The headline acceptance target remains Ubuntu Desktop install under 10 minutes
on an idle Apple Silicon host, but Slice 4 should not claim that target unless
the whole install path is healthy. If the storage microbenchmark improves while
desktop first boot still fails, record both facts separately.

### Operator UX

The CLI should be explicit about ownership:

```text
warning: /dev/rdisk8 is attached read-write to the guest; host writes are unsafe
```

For read-write devices, cove should print the resolved device, sync policy, and
helper validation result before VM start. It should refuse to continue if the
device is mounted or if the helper cannot prove that it opened the intended
node.

The docs should keep `-block` out of beginner quickstarts. It belongs in a
runbook and the CLI reference as an advanced feature.

### Slice 4 acceptance

- [x] `cove run -linux -block /dev/rdiskN:ro` attaches a read-only block device
      through `VZDiskBlockDeviceStorageDeviceAttachment`. (b522ab3)
- [x] `cove run -linux -block /dev/rdiskN:rw` attaches read-write only after
      helper validation proves the device is unmounted. (b522ab3)
- [x] `:rw` defaults to full synchronization; `:rw:sync=none` requires explicit
      operator opt-in. (b522ab3)
- [x] the VM process never runs as root; raw device opening stays in
      `cove-helper`. (b522ab3, no test)
- [x] descriptor passing uses `SCM_RIGHTS`; no device path is reopened by the
      unprivileged VM process after helper validation. (b522ab3, no test)
- [ ] tests cover block spec parsing, sync mapping, non-`/dev` rejection,
      mounted-device rejection, and stale-helper failure text.
      (b522ab3 covers parsing/sync/non-`/dev`/mounted; stale-helper failure
      text has no test)
- [ ] a guarded Darwin integration test opens an `hdiutil attach -nomount`
      raw device and verifies descriptor passing without requiring a physical
      USB drive.
- [x] `docs/benchmarks/disk-io.md` records Slice 4 results separately from
      Ubuntu Desktop first-boot reliability. (0695fcd; spec doc, live numbers
      pending host with a spare raw block device)

## Risks

- **Data loss on host crash:** `DiskCacheEphemeral` (`SynchronizationModeNone`) means a host kernel panic during install leaves a corrupt disk. Mitigation: install workflow already restarts on failure, and we name the policy `Ephemeral` so callers self-select. Run-time disks stay on `Fsync`.
- **Behavior change in run path:** moving from `Automatic+Full` to `Cached+Fsync` is a real semantic change for users who pull plugs. We claim `Fsync` is "best-effort sync" per Apple's docs, which matches what most filesystems expect, but this should be flagged in changelog.
- **Discard/TRIM amplification:** the 122 MB/s discard rate observed in iostat is curtin laying down ext4 + initial fstrim. With `SynchronizationModeNone`, those discards are batched in host cache and may not reach the underlying APFS sparse bundle promptly — meaning host disk usage can lag guest reality. This is a feature for ephemeral installs (faster), and irrelevant since we throw the install state away on power-off anyway.

## Slice 1 acceptance

- [x] `disk_attachment.go` lands with `DiskCachePolicy` + helper
- [x] All callsites migrated, no remaining
      `NewDiskImageStorageDeviceAttachmentWithURLReadOnlyError` in non-test code
- [x] `-disk-sync` flag wired on `cove run` and `cove install`
- [x] Benchmark recorded in `docs/benchmarks/disk-io.md`: before-N, after-N,
      on M-series host
- [ ] Ubuntu 24.04 desktop install completes in <10 min on idle host (was 20-30 min)

## Slice 2 acceptance

- [x] Linux root disks can be attached through NVMe with `VZNVMExpressControllerDeviceConfiguration`
- [x] Hidden `-nvme` flag wired on Linux run/install paths
- [ ] Ubuntu Desktop install benchmark recorded for virtio-blk vs. NVMe on this host

## Slice 3 acceptance

- [x] Hidden `-raw-disk` flag wired on install paths
- [x] Default install disk creation remains sparse
- [x] Raw path preallocates host blocks up front
- [x] Host-only benchmark recorded for image creation plus first 1 GiB write

## References

- NotebookLM session 2026-05-03 (notebook `6e768319-9962-48a6-9fef-71db2dceed84`):
  - Confirmed enum constants: `VZDiskImageCachingMode{Automatic=0, Uncached=1, Cached=2}`, `VZDiskImageSynchronizationMode{Full=1, Fsync=2, None=3}`
  - Identified gap vs. UTM: UTM also uses default mode but exposes NVMe toggle; cove can leapfrog by exposing caching/sync directly
- Apple docs: [VZDiskImageStorageDeviceAttachment](https://developer.apple.com/documentation/virtualization/vzdiskimagestoragedeviceattachment)
- Design [028](028-block-device-passthrough.md): block-device passthrough
  helper protocol, `SCM_RIGHTS`, validation, and CLI architecture
- Code-Hex/vz API: `NewDiskImageStorageDeviceAttachmentWithURLReadOnlyCachingModeSynchronizationModeError`
- Project memory: `project_linux_vm_followups.md` (workstream #2 — disk I/O performance verification)
