# Manual APFS Snapshotting

**Status**: draft
**Author**: Codex
**Date**: 2026-04-22
**Scope**: manual APFS snapshot create/list/delete for macOS guests; no dependency on Carbon Copy Cloner at runtime

## Why this doc exists

`cove` already has two adjacent mechanisms:

1. VM state snapshots and suspend/restore
2. whole-disk clonefile snapshots of `disk.img`

Those are useful, but neither exposes native APFS snapshot management inside a
macOS guest volume. Users who want filesystem-native checkpoints, especially on
SIP-enabled guests, need a lower-level primitive: create, inspect, and delete
APFS snapshots deliberately, without taking a full VM-state save.

The immediate goal is not to replicate Carbon Copy Cloner. The goal is to add a
reliable, explicit APFS snapshot surface to `cove`, then use Carbon Copy
Cloner as a reference implementation to validate which guest-side and helper
behaviors are required on modern macOS.

## Goals

1. Add manual APFS snapshot create/list/delete for macOS guests.
2. Keep the feature usable on SIP-enabled guests.
3. Avoid taking a runtime dependency on private CCC behavior.
4. Preserve current PIT and disk-snapshot features.
5. Build enough observability to understand how APFS snapshots relate to
   later rollback and restore workflows.

## Non-goals

1. No attempt in v1 to replace current PIT save/restore.
2. No promise of booting directly from an APFS snapshot in v1.
3. No dependency on Carbon Copy Cloner binaries, helpers, or private formats.
4. No Linux support in this design.

## Current state

Today `cove` can:

1. save VM machine state plus a cloned disk image
2. clone and run disposable linked clones from a whole-disk snapshot
3. restore a PIT disk plus suspend state into the VM directory

Today `cove` cannot:

1. enumerate native APFS snapshots inside the guest filesystem
2. create or delete APFS snapshots by user request
3. name and track APFS snapshots independently of disk-image clones
4. inspect how third-party tooling such as CCC creates and manages snapshots

## User-facing scope

The first user-facing surface should be a new command family:

```text
cove apfs-snapshot create <name>
cove apfs-snapshot list
cove apfs-snapshot delete <name>
cove apfs-snapshot inspect <name>
```

`inspect` is included because native APFS snapshot names alone are not enough
for later automation. We need stable metadata for correlation with CCC and with
future rollback work.

## Design principles

1. Prefer public guest-visible mechanisms first. If `diskutil`, `tmutil`,
   `bless`, or documented APFS behaviors are sufficient, use them.
2. Keep snapshot creation explicit. Do not hide APFS snapshots behind PIT save.
3. Treat APFS snapshots as filesystem objects, not as VM-state objects.
4. Use CCC only to discover behavior we can reproduce with public or already
   accepted private APIs.
5. Capture enough metadata that we can compare snapshots created by `cove`,
   `tmutil`, and CCC.

## Proposed architecture

### 1. Snapshot manager

Add a small manager, likely under a future `internal/` package, that owns:

1. guest-side command execution
2. snapshot metadata persistence
3. parsing of snapshot inventory output
4. error normalization for user-facing CLI output

Inputs:

1. VM name / VM directory
2. guest agent access or a clear error if unavailable
3. target APFS volume selection policy

Outputs:

1. native APFS snapshot name
2. structured metadata record
3. normalized create/list/delete/inspect responses

### 2. Execution mode

The initial implementation should be guest-assisted, not offline host-mounted.

Reason:

1. CCC behavior of interest is guest-local on a booted SIP-enabled macOS system.
2. Guest execution sees the same volume-group, sealing, and authorization rules
   as real user software.
3. Offline host-mounted APFS operations are useful later, but they are not the
   right first truth source for matching CCC.

The command path should therefore require:

1. a running macOS VM
2. a reachable guest agent
3. a provisioned user account

### 3. Metadata model

Persist metadata under the VM directory, for example:

```text
vmDir/apfs-snapshots/manifest.json
```

Each entry should record:

1. `name`
2. `created_at`
3. `os_version`
4. `sip_status`
5. `volume_device`
6. `volume_uuid`
7. `volume_group_uuid` if available
8. `native_snapshot_name`
9. `native_snapshot_xid` if discoverable
10. `creation_method`
11. `notes`

`creation_method` should distinguish:

1. `cove.diskutil`
2. `cove.tmutil`
3. `cove.bless`
4. `observed.ccc`

## Candidate mechanisms to validate

The design intentionally leaves room for multiple create/list/delete backends.
The lab work should validate which of these are necessary:

1. `diskutil apfs listSnapshots`
2. `diskutil apfs deleteSnapshot`
3. `tmutil localsnapshot`
4. `bless --create-snapshot`
5. direct `fs_snapshot_*` behavior through a helper tool, only if the shell
   tools above are insufficient

The implementation should not assume which command is authoritative until the
CCC lab confirms the behavior on a current SIP-enabled guest.

## Reverse-engineering plan

Create a dedicated macOS lab VM with SIP enabled and install Carbon Copy
Cloner 7. Observe behavior; do not depend on CCC for shipping functionality.

### Lab VM requirements

1. fresh macOS guest
2. dedicated VM name
3. provisioned admin user
4. guest agent available
5. SIP verified enabled

### Carbon Copy Cloner acquisition

Use Bombich’s official distribution path. As of 2026-04-22, the current
Homebrew cask metadata points to CCC 7.1.5 build 8335 from Bombich’s official
distribution host and can serve as a reproducible lookup source for the lab:

- [Homebrew Cask API](https://formulae.brew.sh/api/cask/carbon-copy-cloner.json)
- [Bombich download landing page](https://bombich.com/download/get)

### Observation checklist

For the CCC lab, collect:

1. bundle inventory
2. entitlements on app and helper binaries
3. `Info.plist` contents
4. framework dependencies
5. helper and launchd installation behavior
6. snapshot inventory before any CCC action
7. snapshot inventory after each CCC action
8. relevant unified log output
9. helper process tree and launchd labels
10. changes in APFS volume roles or blessed snapshot state

### Instrumentation

Use the repo’s existing macOS reversing workflow and standard host/guest tools:

1. `codesign -d --entitlements -`
2. `plutil -p`
3. `otool -L`
4. `strings`
5. `launchctl print`
6. `ps`, `log stream`, `log show`
7. `diskutil apfs list`, `diskutil apfs listSnapshots`
8. guest-agent command execution via `cove ctl agent-exec`

### Success criteria for the lab

We should be able to answer:

1. Which executable actually creates snapshots: the app, a helper, or a CLI tool?
2. Which command-line tools or system APIs it invokes?
3. Whether it creates snapshots on the System volume, Data volume, or both.
4. Whether naming is user-controlled, generated, or partially opaque.
5. Whether any blessed or boot-related metadata changes are involved.
6. Whether SIP changes the behavior we care about.

## Implementation phases

### Phase 1: lab setup and behavioral spec

1. launch a fresh macOS VM
2. verify SIP enabled
3. install CCC
4. collect the behavioral spec described above

Deliverable:

1. a short internal spec of how CCC creates and manages APFS snapshots on a
   current SIP-enabled guest

### Phase 2: guest-assisted `cove apfs-snapshot`

1. implement `create`
2. implement `list`
3. implement `delete`
4. persist metadata manifest
5. add `inspect`

Deliverable:

1. manual APFS snapshot management via the guest agent on a running macOS VM

### Phase 3: validation and future hooks

1. compare `cove` snapshots to CCC snapshots on the same guest
2. verify snapshot naming and deletion semantics
3. decide whether future rollback should:
   - restore from native APFS snapshots
   - continue using disk-image clones
   - combine both

## Risks

1. Modern macOS may require a helper-specific privilege boundary that shell
   tools alone do not expose.
2. APFS snapshots on sealed system volume groups may require behavior that is
   different from plain Data-volume snapshots.
3. Snapshot names may not be sufficient identifiers; XIDs or other metadata may
   be necessary.
4. The guest agent may not be enough for all observations if CCC relies on a
   GUI authorization flow or a privileged helper.

## Open questions

1. Is `tmutil` enough for the create path, or does it create the wrong class of
   snapshot for our goals?
2. Does CCC use `diskutil`, `tmutil`, `bless`, direct APFS APIs, or a mix?
3. Should `cove` expose guest APFS snapshots only, or also support offline host
   inspection of stopped VMs?
4. Do we need separate snapshot surfaces for System and Data volumes?
5. When later integrating with rollback, should APFS snapshots become inputs to
   PIT restore, or remain a separate filesystem-only feature?

## Recommendation

Ship manual APFS snapshot management as a guest-assisted macOS-only feature
first. Use a clean SIP-enabled CCC lab VM to identify the minimum command/API
set required, but keep the shipping feature independent of CCC and narrowly
scoped to create/list/delete/inspect until the behavior is well understood.
