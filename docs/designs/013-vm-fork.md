# 013 — VM Forking

Status: draft
Date: 2026-04-25
Author: Claude (Opus 4.7) on behalf of Travis Cline

## Goal

Make `cove fork` produce a runnable child VM from a parent in O(seconds) and
O(metadata bytes) of disk, so users can:

- Spin up N concurrent siblings from a single golden image (test matrix,
  parallel agent boxes, claw farm).
- Branch off a known-good snapshot, diverge, throw it away.
- Restore many VMs from one captured `.vmstate` without copying gigabytes.

Scope: macOS and Linux guests. Apple Silicon hosts only (Virtualization.framework
is what we target).

Non-goals: live migration between hosts; nested virtualization; snapshotting
across host reboots without `clonefile` support.

## Why this is hard

A `vmconfig` directory is not just a disk. It bundles:

| File              | Sharable across siblings? | Notes |
| ----------------- | ------------------------- | ----- |
| `disk.img`        | No (write conflict)       | Must clone or RAM-overlay |
| `auxiliary-storage` | No                      | Mutated at boot; framework locks it |
| `hardware-model`  | Yes (read-only)           | Identity input for `.vmstate` |
| `machine.id`      | **No** — must be unique   | Clones with the same ID confuse VZ + guest |
| `config.json`     | Mostly                    | Per-child overrides for name, MAC, ports |
| `control.sock`    | No                        | Per-process |
| `suspend.vmstate` | Read-only fork source OK  | Must match aux+hw exactly |
| `snapshots/<n>.vmstate` | Read-only fork source OK | Same |

The hard constraints come from VZ:

1. `RestoreMachineStateFromURL` requires the *current* VM's hardware-model and
   aux-storage to match the snapshot byte-for-byte. Hand-editing aux-storage
   between parent and child breaks restore.
2. `VZDiskImageStorageDeviceAttachment` takes an exclusive flock on the file.
   Two VMs cannot point at the same `disk.img` even if one is read-only.
3. `machine.id` (the platform identifier) is supposed to be unique per VM.
   macOS guests will boot with a duplicate but iCloud / Find-My / FairPlay break.
4. NAT MAC addresses must differ across concurrent VMs on the same `vmnet` to
   avoid the host bridge dropping frames.

## The three fork models

### Model A — Persistent fork (clone-and-diverge)

Use case: long-lived child, will accumulate its own state.

```
parent/
  disk.img        ─clonefile→  child/disk.img    (CoW, instant, 0 B)
  hw.model        ─copy→       child/hw.model    (must be identical)
  aux/            ─copy→       child/aux/        (will diverge at boot)
  machine.id      ─generate→   child/machine.id  (NEW unique value)
  snapshots/X.vmstate ─optional→ child/suspend.vmstate (if -from-snapshot)
```

Cost: a few KB of metadata + however much the child writes.

Implementation: extend existing `clone.go` + `template.go`. They already
`clonefile` the disk; we add a `-from-snapshot <name>` flag that also seeds the
child's `suspend.vmstate` from the parent's `snapshots/<name>.vmstate`.

VZ correctness: `.vmstate` was captured against parent's hw.model + aux. The
child has the same hw.model (copied) but a *fresh* aux. **Restore will fail**
unless we also seed aux from the parent. Two options:

- **A1**: copy parent's aux → child. Works on first boot from snapshot.
  Subsequent boots use the child's evolving aux. Tested empirically.
- **A2**: don't seed `.vmstate`; cold-boot the child from the cloned disk.
  Safer. Loses the "instant resume" property.

Default to A1 with a fallback to A2 on restore failure (mirrors the existing
`Suspend restore failed → cold boot` path at macos.go:1266).

### Model B — RAM overlay (zero-divergence ephemeral)

Use case: many short-lived siblings; throw all writes away on shutdown.

```
parent/disk.img  ←(read-only)─ child1 + child2 + child3 ... concurrent
                              + per-child VZTemporaryRAMStorageDeviceAttachment
```

We already have the primitive: `pvz.NewVZTemporaryRAMStorageDeviceAttachmentWithURLReadOnlyError`
(system_disk.go:71). It backs writes with anonymous RAM; the underlying file is
opened read-only.

**Empirical answer — VZ allows N read-only attachments and takes no file lock.**
Validation #1 (validation1_test.go) confirmed that creating two
`VZTemporaryRAMStorageDeviceAttachmentWithURLReadOnly` attachments against
the same `disk.img` succeeds, and a host-side `flock(LOCK_EX|LOCK_NB)` probe
against the file while both attachments are held also succeeds. VZ does not
hold even `LOCK_SH` at attachment-creation time. Phase 3 ships Model B as
the only path; no clonefile-per-child fallback is required.

Each child still needs:
- Its own vmDir (for control.sock, screenshots, logs)
- Fresh `machine.id`
- Fresh MAC
- Copied hw.model + aux (immutable RAM-overlay means aux mutations also vanish)

Restore from snapshot in this mode: **possible but lossy**. The snapshot was
captured with a specific aux state; with RAM-overlay aux, that state is
re-established only on the first boot, then lost on shutdown. Acceptable for
ephemeral fleets; document the caveat.

### Model C — Layered file (`hdiutil compact` + sparse bundle)

Use case: persistent fork with bounded write amplification.

Convert `disk.img` to a sparse bundle with a known parent UUID; child gets a
sparse-bundle "shadow" that only stores diffs. Apple's `DiskImages2.framework`
(internal/diskimages2/) exposes this. We've stubbed bindings but not used them
in production.

More complex than A or B. Defer.

## Proposed CLI surface

```
# Persistent fork (clone-and-diverge)
cove fork <parent> <child> [-snapshot <name>] [-linked]
    -snapshot   restore child from parent/snapshots/<name>.vmstate at first boot
    -linked     use clonefile (default; fall back to copy if not APFS)

# Ephemeral siblings (RAM-overlay, run-only, not registered)
cove run -fork-from <parent> [-snapshot <name>] [-name <ephemeral-id>]
    auto-deletes vmDir on exit unless -keep is given

# Bulk
cove fork <parent> <child-prefix> -count 8
    creates child-prefix-{0..7}; useful for test matrices

# Inspect lineage
cove vm tree
    parent
    ├─ child-a (linked, 12 MiB diverged)
    ├─ child-b (linked, 3.4 GiB diverged)
    └─ child-c (full copy)
```

Reuse existing `clone.go` plumbing; `cove fork` is mostly an alias for
`cove clone -linked` plus the `-snapshot` plumbing and the lineage metadata.

## Lineage metadata

Add `parent_vm` and `parent_snapshot` to `vmconfig.Config`. Lets `cove vm tree`
work and lets `cove gc` know which children block deleting a parent disk
(`clonefile` lineage means a child's disk *physically depends* on the parent
file existing, even though it's logically a CoW copy).

```go
type Config struct {
    // ... existing fields
    ParentVM       string `json:"parentVM,omitempty"`       // VM name we forked from
    ParentSnapshot string `json:"parentSnapshot,omitempty"` // snapshot we restored from
    ForkedAt       time.Time `json:"forkedAt,omitempty"`
}
```

## Per-child uniqueness checklist

| Field | How |
| ----- | --- |
| `machine.id` | `vz.NewVZMacMachineIdentifier()` — generate fresh |
| MAC addr (NAT) | Locally-administered random; written to config.json |
| `vmDir` path | `~/.vz/vms/<child-name>/` |
| `control.sock` | Already per-vmDir |
| Vsock CID | Skip auto-assignment; let kernel pick (already does) |
| Hostname (guest) | Optional `-set-hostname` flag drives a vzscript on first boot |

## Concurrent-run guard

Even with everything above, two `cove run`s on the *same* vmDir still race.
Add a sibling guard:

- On `cove run`, take an `flock` on `<vmDir>/run.lock`.
- On exit, release.
- On startup, if held by a live PID, refuse with `vm '<name>' is already running (pid N)`.

Independent of forking; ships first because it also fixes the `-restore` flag
collision risk I flagged separately.

## Validation plan

Empirical tests required:

1. **B1**: Two `cove run` invocations against the same `disk.img` with both
   attached read-only via RAM-overlay. Do they coexist?
   - If yes: Model B is free.
   - If no: file flock is exclusive; Model B requires `clonefile` per child.
2. **A1**: After cloning hw.model + aux from parent and dropping parent's
   `snapshots/X.vmstate` as child's `suspend.vmstate`, does VZ restore?
   - Compare aux byte-for-byte before vs after restore-attempt.
3. **Restore + diverge**: Does the child VM mutate aux on first boot in a way
   that blocks subsequent restores? (Existing suspendConfigFingerprint check
   may already catch this.)
4. **machine.id collision**: Boot two macOS children with the same machine.id
   simultaneously. Confirm guest-side breakage is iCloud-only, not VZ-fatal.

Add tests to `runtime_lifecycle_test.go` style txtar suite: spin up parent,
fork to child, boot child to login window, snapshot diff.

## Open questions

- **Snapshot disk consistency**: `.vmstate` captures CPU/RAM, not disk. If
  parent's disk has writes pending in the framework's caches when we snapshot,
  the child's view will be stale. Solution: pause + fsync via vsock agent
  before the host calls `SaveMachineStateToURL`. Already partially handled by
  vzkit/snapshot.Save's pause logic.
- **Identity files** (macOS): `auxiliary-storage` contains the SEP keys that
  bind to the iCloud account. Cloning means siblings share the same SEP
  identity. Either accept (siblings are "the same Mac") or regenerate identity
  via `-recover-identity` (existing flag) on first child boot.
- **Disk-snapshot lineage**: `disk-snapshot` already uses `clonefile` for disk
  state. `cove fork` should reuse `DiskSnapshotManager` rather than
  reimplementing. Probably refactor `DiskSnapshotManager` into
  `internal/forkmgr` with snapshot + fork as two operations on the same
  primitive.

## Phased delivery

1. **Phase 0** (~50 LOC): `<vmDir>/run.lock` flock guard. Lands first; fixes
   `-restore` collision risk and is a precondition for everything below.
2. **Phase 1** (~200 LOC): `cove fork <parent> <child>` — clone vmDir, fresh
   machine.id, fresh MAC. No `-snapshot`. Boots from cloned disk.
3. **Phase 2** (~150 LOC): `cove fork -snapshot <name>` — A1 with A2 fallback.
   Includes the empirical aux-replay test from Validation #2.
4. **Phase 3** (~300 LOC): `cove run -fork-from <parent>` ephemeral mode.
   Validation #1 returned PASS with no file lock — Model B ships as the
   only path; no clonefile-per-child fallback. `-snapshot` is deferred
   until a future identity-preserving fork option lands.
5. **Phase 4** (~100 LOC): `cove vm tree`, lineage metadata, GC awareness.
6. **Phase 5**: A1 vmstate snapshot fidelity. Slice 5a preserves the four
   identity inputs needed for future vmstate resume; Slice 5b wires actual
   `RestoreMachineStateFromURL`.

Stop after any phase if the validation fails; phases 1-2 are useful on their
own.

Audit 2026-05-04: clean for abandoned preserve-identity stubs in `run_bundle.go`, `image_fork.go`, and `runtime_lifecycle.go`; A1 identity preservation remains deferred per the Phase 2 bench notes and `project_a1_snapshot_fidelity` finding referenced by design 024.

## Phase 5 — vmstate identity fidelity

Phase 5 revisits Model A1 for saved machine state. The earlier dead-code branch
proved the important constraint: a `.vmstate` is not bound only to
`machine.id`. It is bound to the tuple `{machine.id, aux storage, MAC address,
disk image}`. Slice 5a preserves that tuple for `cove run -fork-from`; Slice 5b
is deferred and will add actual vmstate restore wiring.

```
VM start
  |
  v
save vmstate
  |
  v
fork-from <vmstate>
  |
  v
restore identity
  |-- machine.id    -> copy source machine-config identity
  |-- aux storage   -> clone/copy aux.img
  |-- MAC address   -> persist and re-apply mac.address
  |-- disk image    -> clonefile CoW disk.img
  |
  v
resume
```

The identity inputs are preserved as follows:

| Input | Preservation rule |
| ----- | ----------------- |
| `machine.id` | Encoded in `machine-config.json` and represented on disk as `machine.id`; preserved by copying the source identity into the fork bundle. |
| Aux storage | `aux.img` is copied with the bundle so VZ sees the same auxiliary storage bytes as the saved state. |
| MAC address | Persist `mac.address` when present and write the same address into the fork bundle; current first-boot regeneration is not acceptable for vmstate fidelity. |
| Disk | `disk.img` stays clonefile-COW, matching Phase 3's disk primitive without copying full disk contents. |

Identity mismatch detection is part of Slice 5a. The source bundle is read as a
complete identity set, and the destination bundle is written from that set. If
any component cannot be read, copied, or re-read as equal, `cove run -fork-from`
fails loudly before boot. A partial identity-preserving fork is worse than a
cold boot because it creates a bundle that looks resumable but cannot satisfy
Virtualization.framework's restore checks.

Slice 5a deliberately stops after identity preservation. The runtime still cold
boots the fork with the RAM-overlay disk attachment; Slice 5b will decide when
and how `suspend.vmstate` or `snapshots/<name>.vmstate` should drive
`RestoreMachineStateFromURL`.

## What this replaces / consolidates

- `clone.go` becomes `cove fork` under the hood.
- `template.go` becomes "named parent for forking."
- `disposable.go` (linked-clone-then-discard) becomes `cove run -fork-from -ephemeral`.
- `disk-snapshot run` (snapshots.go:436) becomes `cove run -fork-from <parent> -snapshot <name>`.

Today these are four CLIs doing the same operation with different ergonomics.
After fork lands, deprecate the others to aliases.
