# Design 032: Per-VM Resource Quotas

Status: Shipped on 2026-05-05. SHA chain:
- `94bf2d2` design doc landed
- `62a71aa` `internal/vmquota` quota persistence and `diskutil apfs setQuota` wrapper
- `2bad0e8` `cove quota` CLI plus install-time `persistInstallQuota` wired into
  `installer.go` and `linux_installer.go`

Acceptance items shipped: `quotas.json` per-VM file, sparse cpus/memory_gb/disk_gb
schema, `cove quota <vm> show|cpu|memory|disk`, host-side APFS directory quota via
`diskutil apfs setQuota`, install-time quota write for fresh macOS and Linux VMs.
Deferred items in this doc remain deferred. Roadmap segment R36-629F closed.

Verified 2026-05-10 (R362): `handleQuotaCommand` at `quota_cli.go:32`,
`persistInstallQuota` at `quota_cli.go:181` (called from `installer.go:438`,
`linux_installer.go:278`, `windows.go:445`), `internal/vmquota/quota.go`
exports `FileName = "quotas.json"`.

Author: Travis Cline
Date: 2026-05-05

## Problem

cove already lets users choose CPU count, memory size, and disk size at VM
creation, but those choices are scattered across flags and runtime config. There
is no durable quota record per VM and no command that shows or updates the
operator's intended caps.

Issue #246 tracks the need for explicit per-VM CPU, memory, and disk caps. The
goal is not to invent a second scheduler. It is to make the existing native
limits visible, persistent, and enforceable at the boundaries cove controls.

## Quota Model

Persist quotas in:

```text
~/.vz/vms/<name>/quotas.json
```

The JSON shape is:

```json
{
  "cpus": 4,
  "memory_gb": 8,
  "disk_gb": 50
}
```

Unset or zero fields mean cove has no explicit quota recorded for that resource.
The VM may still have an effective limit from existing `config.json`, the
Virtualization framework configuration, or the disk image size.

## Enforcement

CPU cap is the vCPU count. cove already applies it through the VM configuration
using `VZVirtualMachineConfiguration.SetCPUCount`, and `vmconfig.Config` already
persists the selected CPU count.

Memory cap has two layers:

- Initial memory cap: cove applies `SetMemorySize` when constructing the VM.
- Runtime target: cove uses the existing memory balloon control path for
  lowering or raising the target inside the configured maximum.

Disk cap has two layers:

- Guest-visible virtual disk size: install-time `-disk-size` creates the
  preallocated or sparse backing image at the requested size.
- Host-side VM directory quota: cove applies `diskutil apfs setQuota` to the VM
  bundle directory, `~/.vz/vms/<name>/`, so snapshots, logs, metadata, and disk
  backing files cannot grow past the host APFS quota.

## Boundaries

The Virtualization framework natively enforces CPU and memory limits. cove should
not try to police those with background sampling or host process throttling.

Disk quota is host-side APFS enforcement. It applies only when the VM directory
is on an APFS volume that supports directory quotas. It does not change the
guest partition table, the guest filesystem's own free-space accounting, or any
external files mounted into the guest through shared folders.

If APFS quota application fails, cove should return an actionable error. The VM
disk image size still caps the guest-visible root disk, but the operator should
know that host-side quota enforcement is missing.

## CLI

Add:

```text
cove quota <vm> show
cove quota <vm> cpu 4
cove quota <vm> memory 8
cove quota <vm> disk 50
```

`show` reads `quotas.json` and prints the recorded caps. Setters update only the
requested field and leave the others unchanged.

`cpu` and `memory` also update the existing VM hardware config so the next boot
uses the requested values. `memory` remains bounded by the VM's configured
maximum at runtime; lowering the live target still uses `cove ctl memory set`.

`disk` saves the quota and applies `diskutil apfs setQuota` to the VM directory.
It does not resize an existing guest disk in this slice. New installs apply the
disk quota during VM creation.

## Install Integration

At VM creation, cove writes `quotas.json` from the requested `-cpu`, `-memory`,
and `-disk-size` values. It then applies the APFS quota to the VM directory using
the requested disk size. This makes fresh VMs quota-aware without changing the
existing install flags.

## Deferred

- Live guest disk resize.
- Per-snapshot quota accounting.
- Quotas for shared-folder targets outside the VM directory.
- Non-APFS host quota backends.
- Runtime CPU hot-plug or live CPU throttling.

## Cross-references

- [`docs/designs/031-vm-lifecycle.md`](031-vm-lifecycle.md) for the policy
  stop path that runs alongside quotas.
- [`docs/designs/033-cove-daemon.md`](033-cove-daemon.md) for the daemon that
  can schedule or report quota-related host work later.
