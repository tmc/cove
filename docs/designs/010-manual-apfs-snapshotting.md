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

## Initial CCC findings

The first CCC reversing pass on 2026-04-23 materially narrowed the design.

Environment used:

1. VM `ccc-apfs-lab-20260423`
2. macOS Tahoe guest
3. SIP disabled for the lab run
4. Carbon Copy Cloner 7.1.5 (build 8335) from Bombich's official download page

Static bundle findings:

1. The snapshot-heavy logic is concentrated in
   `Contents/Frameworks/CloneKit.framework`.
2. The packaged XPC service
   `Contents/Frameworks/CloneKit.framework/.../CloneKitService.xpc` carries the
   entitlement `com.apple.developer.vfs.snapshot = true`.
3. `CloneKitService.xpc` also has `com.apple.security.files.all = true`.
4. The packaged privileged helper `com.bombich.ccc.helper` exists separately as
   a root Mach service bundle, but it is not the only snapshot-relevant
   component.
5. First launch of CCC does not immediately install the privileged helper into
   `/Library/PrivilegedHelperTools`; the app can launch and enter the task UI
   without that installation step.

Snapshot API findings from strings:

1. `CloneKitService` contains direct references to:
   - `fs_snapshot_create`
   - `fs_snapshot_mount`
   - `fs_snapshot_delete`
   - `fs_snapshot_rename`
2. `CloneKitService` exposes a `SnapshotServiceProtocol` with methods for:
   - create
   - mount
   - unmount
   - delete
   - rename
   - enumerate
   - compute snapshot disk usage
   - prune snapshots with a retention policy
3. `CloneKitService` also references:
   - `/usr/sbin/diskutil`
   - `/usr/sbin/bless`
   - `clonefile`
4. Snapshot labels and classes are first-class concepts in the service:
   - `CCC SafetyNet Snapshot`
   - `CCC Transient Snapshot`
   - `CCC Checkpoint Snapshot`
   - `com.bombich.ccc.permanent`

Observed live behavior:

1. With Full Disk Access granted and `com.bombich.ccc.helper` active, the
   bundled CLI can create a protected snapshot directly:

   ```text
   ccc --create /System/Volumes/Data "codex-lab-20260423"
   ```

2. CCC reported the created snapshot as:

   ```text
   🔒 CCC Snapshot (by user:'tmc' notes:'codex-lab-20260423')
   ```

3. `diskutil apfs listSnapshots /System/Volumes/Data` showed the native APFS
   snapshot name as:

   ```text
   com.bombich.ccc.permanent.<payload>.2026-04-23-015727
   ```

4. The `<payload>` component is not opaque random data. It is:
   - base64
   - of a raw DEFLATE stream
   - containing JSON metadata

5. For the test snapshot created in the lab, the decoded JSON was:

   ```json
   {"l":"com.bombich.ccc.2026-04-23-015727","n":"codex-lab-20260423","u":"tmc"}
   ```

6. The practical meaning appears to be:
   - `l`: internal CCC logical label
   - `n`: user-visible notes/comment
   - `u`: user name

Public Apple tool findings:

1. `diskutil apfs` does **not** expose a snapshot create verb. It exposes:
   - `listSnapshots`
   - `deleteSnapshot`
2. `tmutil localsnapshot` can create a snapshot on the startup disk, but:
   - the name is system-generated
   - there is no free-form comment field
   - the snapshot is explicitly treated as purgeable
3. `bless --create-snapshot` exists, but its surface is oriented around boot
   snapshot management and not free-form named/commented snapshots.

Entitlement gate findings:

1. A minimal lab-built binary calling `fs_snapshot_create()` works without the
   entitlement in the sense that it launches, but it gets `EPERM`.
2. Adding `com.apple.developer.vfs.snapshot` to an ad hoc-signed binary causes
   AMFI to kill the process before launch.
3. The guest log makes the failure explicit: restricted entitlements on an
   invalid or ad hoc code signature are rejected fatally.
4. That means a self-signed helper is not enough. A working direct
   `fs_snapshot_*` implementation requires a properly provisioned binary with a
   real entitlement grant.

Implications:

1. CCC is not just wrapping `tmutil localsnapshot`.
2. Full-control snapshot behavior appears to rely on the VFS snapshot
   entitlement plus direct `fs_snapshot_*` calls.
3. `diskutil` and `bless` are still part of the overall workflow, especially
   around APFS groups, preboot, and bootability, but they are not the only
   snapshot mechanism.
4. For `cove`, a shell-only design is unlikely to match CCC semantics for
   create/mount/delete/rename with stable labels and XIDs.
5. The entitlement is in practice gated. A custom ad hoc helper in the lab
   cannot simply opt into `com.apple.developer.vfs.snapshot`.
6. If `cove` needs CCC-style named/commented snapshots without depending on
   CCC, we likely need one of two models:
   - a public-tools implementation with external metadata, accepting that the
     native APFS snapshot will not be CCC-style
   - a properly provisioned helper with Apple-granted snapshot entitlement

Developer forum signal:

Public Apple Developer Forums search results line up with the lab findings.
Notable threads seen on 2026-04-23:

1. 2025-06-03:
   [How to create file system snapshots?](https://developer.apple.com/forums/thread/786595)
   The public search snippet shows a developer hitting the same failure we saw:
   adding `com.apple.developer.vfs.snapshot` caused Xcode to report that the
   provisioning profile did not include that entitlement.
2. 2017-era:
   [fs_snapshot_create required entitlement…](https://developer.apple.com/forums/thread/89635)
   The public search snippet says full snapshot control requires
   `com.apple.developer.vfs.snapshot`.
3. 2020-06-20:
   [APFS take a snapshot](https://developer.apple.com/forums/thread/79038)
   The public search snippet says the older `apfs_snapshot` tooling is gone and
   that entitlement access was required for snapshot API support.
4. 2018-era:
   [Keep APFS snapshots](https://developer.apple.com/forums/thread/89977)
   The public search snippet points back to the same special-entitlement
   requirement.

This is not primary evidence of the entitlement policy by itself, but it
matches both Apple's public managed-capability documentation and the direct
ASC/Developer Portal behavior observed in the lab: there is no public
`vfs.snapshot` capability exposed for self-service enablement.

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

Use Bombich’s official distribution path. As of 2026-04-23, Bombich’s official
download page advertises CCC 7.1.5 for Ventura 13.1+, Sonoma 14, Sequoia 15,
and Tahoe 26:

- [Bombich download landing page](https://bombich.com/download/get)
- [Bombich download page](https://bombich.com/download)

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

Current answer after the first pass:

1. snapshot orchestration lives in `CloneKit.framework`
2. direct snapshot operations are exposed by `CloneKitService.xpc`
3. the service is entitled for `com.apple.developer.vfs.snapshot`
4. direct `fs_snapshot_*` calls are present and likely central
5. `diskutil`, `bless`, and `clonefile` are supporting mechanisms, not the
   whole implementation
6. CCC stores user comment metadata inside the native snapshot name via a
   compressed JSON payload
7. public Apple CLI tools do not appear to offer the same named/commented
   snapshot surface

### Phase 2: guest-assisted `cove apfs-snapshot`

1. implement `create`
2. implement `list`
3. implement `delete`
4. persist metadata manifest
5. add `inspect`

Deliverable:

1. manual APFS snapshot management via the guest agent on a running macOS VM

Updated design pressure from the CCC pass:

1. If public shell tools prove insufficient, Phase 2 likely needs a small
   guest-side helper binary rather than more `diskutil` parsing.
2. The helper boundary should be explicit from the start so we can swap between:
   - public shell tools only
   - a custom snapshot helper
   - an experimental entitlement-bearing helper in the lab
3. The CLI surface should not hardcode CCC-like retention behavior in v1.
   Snapshot create/list/delete remain the right first scope.

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
