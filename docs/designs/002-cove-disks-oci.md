# cove disk handling & OCI — design doc

**Status**: draft v5 (post-Council round-3 final + liveness-gate refinement)
**Author**: cove team
**Date**: 2026-04-16
**Target**: cove 0.1 (OCI v0 path) → 0.2 (content-addressed store)

## Changes since v0

Applied Council round-1 verdicts:
- **P0 dropped**: snapshot refactor from `clonefile` to manifest. Local snapshots **keep** APFS `clonefile` semantics. OCI chunking is *only* for network crossings (`cove push`/`cove pull`). Rationale: clonefile is 0-byte, 0-ms; chunk-reassembly would be a multi-second regression and SSD-wear cost for zero product win.
- **P0 added**: GC concurrency protections. Exclusive `flock` on `~/.vz/store/gc.lock` and 1-hour `mtime` grace window on blob deletion — to avoid nuking in-flight `cove pull` blobs.
- **P1 dropped**: macFUSE lazy assembly. Cove's pitch is cgo-free and zero-dep; third-party kexts violate that. If lazy assembly is ever revisited, it uses Apple's native `FileProvider` or NFSv4-over-socket — never macFUSE. Eager sparse-write is the only shipped path.
- **P2**: docs must call out Docker credential helper requirement explicitly in `docs/getting-started/push-pull.md`.

Applied Council round-2 + user interview (v2):
- **Asymmetric lume compat**: push emits **cove-native annotations only** (`org.tmc.cove.*`); pull still reads and translates `org.trycua.lume.*` for migration-in. We don't ship lume-compatible images — that would bind us to their schema evolution. Cove images stay standard OCI and portable, just not lume-consumable.
- **Strict v0.1 streaming**: `cove pull` in v0.1 decompresses LZ4 chunks and writes them directly into `disk.img` via `WriteAt` at fixed offsets. No intermediate blob store, no `~/.vz/store/`, no `cove gc` — all of that lands in v0.2.
- **Agent-aware `cove compact`**: detect guest via `currentVMAgentPlatform()`; route to `fstrim` (Linux) or `diskutil apfs eraseFreeVolumeSpace` (macOS); fail cleanly if the agent is disconnected rather than hanging.

Applied second-opinion review (v3):
- **Atomic pull via `disk.img.partial`**: streaming pulls write to `disk.img.partial` and are atomically renamed to `disk.img` only after full verification. `disk.provenance` is written after the rename. `cove run` refuses to boot a VM directory with a lingering `disk.img.partial`. Fixes the "half-written disk with valid config.json" failure mode.
- **`cove push --lume-compat` escape valve**: opt-in flag emits both `org.tmc.cove.*` and `org.trycua.lume.*` annotations for mixed-tool teams. Default remains cove-native only; schema independence preserved. Reframed the asymmetry narrative around user value (one-command migration in; one-flag compatibility out).
- **`disk.provenance` v0.1 is unsigned**: explicitly documented as informational-only. Signed variant (Ed25519 key in aux.img) moved to v0.2 roadmap.
- **Known gaps section**: agent upgrade path for users with many running VMs, and signed provenance, are called out as tracked-but-deferred with owner required before v0.2 planning.

Applied Council round-3 (v4):
- Added checkVMNotRunning() gate to `cove pull` flow to prevent APFS inode-orphaning when pulling into an active VM (Council round-3 catch).

Applied v5 refinement:
- Liveness gate now uses a **control-socket probe** (dial `~/.vz/vms/<name>/control.sock` with a 500ms deadline and expect a status-ping reply) rather than a PID-alive or socket-exists check. A stale `control.sock` left by a crashed `cove run` would wrongly block the pull under a PID/existence check; the probe correctly distinguishes "VM is actually serving" from "sock file happens to exist." On probe failure, the stale sock is cleaned up and the pull proceeds.

---

## Goal

Define cove's disk-image model end-to-end: on-disk layout, clone/snapshot semantics, template lifecycle, and a first-class OCI distribution path. Cove **consumes** lume-produced images (one-way migration aid) but ships standard OCI images of its own, not lume-compatible ones.

The underlying observation: **disk images are the object cove ships**. Everything else — the CLI, the control socket, the guest agent — is plumbing around moving and forking a 40–200 GB file. If that file's lifecycle is right, the product is easy. If it's wrong, everything downstream feels clunky.

## Non-goals

- Replacing the VZ framework's disk attach path. `disk.img` stays a raw/sparse file on local disk.
- Guest filesystem awareness. We don't read APFS structures inside the guest image; chunks are block-level.
- Cross-host live migration. The VM suspend state is host-bound (device fingerprint); we ship disks, not live state.
- A general-purpose backup tool. Cove snapshots serve cove VMs; we don't aim to replace Time Machine.
- **Lazy assembly via macFUSE.** Ruled out. Eager sparse-write only.
- **Local snapshot refactor.** Ruled out. `clonefile`-based snapshots stay.
- **Lume-compatible push output.** Ruled out. We translate lume on pull; we don't emit lume keys on push.

---

## Today's model

### On-disk layout

```
~/.vz/vms/<name>/
├── disk.img              # raw sparse file, guest's block device
├── aux.img               # NVRAM/VZMacAuxiliaryStorage
├── hw.model              # serialized VZMacHardwareModel
├── config.json           # VM registry entry (cpus, mem, shared folders, ...)
├── control.sock          # per-VM Unix control socket (runtime)
├── control.token         # bearer token for control socket
├── suspend.vmstate       # CPU+memory snapshot (runtime, invalidated by config change)
├── snapshots/<n>.vmstate # VM state snapshots
└── disk-snapshots/<n>/   # APFS clonefile disk snapshots (STAYS AS-IS)
```

### Lifecycle primitives (unchanged)

- **Install**: `installer.go` writes a fresh disk via `VZMacOSInstaller`.
- **Clone**: `clone.go` uses `unix.Clonefile` (APFS copy-on-write) — near-instant, zero extra space until a block diverges.
- **Disk snapshots**: `snapshots.go` clonefiles `disk.img` into `disk-snapshots/<name>/`. **Keeps working this way.**
- **Templates**: `template.go` has two modes:
  - *Compressed* — gzip `disk.img` into `template/disk.img.gz` (portable, smaller, slow to restore).
  - *Fast/clonefile* — keep uncompressed, use APFS CoW for instant VM creation.
- **Disposable VMs**: `disposable.go` clones a template and discards the clone on exit.

### What's working

- APFS CoW is **the** killer feature on Apple Silicon. Free forks, no copy cost, no reflink dance. **We keep this as a moat** — lume has nothing equivalent for local snapshot restore.
- Two-axis template storage (compressed vs fast) lets users trade disk space for boot speed sensibly.
- Disk snapshots are a layer on the same primitive, so their cost is the same as clone.

### What's not (that OCI path addresses)

1. **No portability.** You can't ship a `.img.gz` to another machine without a bespoke tar/scp ritual. Lume has `lume push/pull` against any OCI registry.
2. **No content addressing for network transport.** Re-downloading the same base image is a full pull every time.
3. **No cross-template dedup for on-disk storage.** If `macos-15-base` and `macos-15-xcode` share 80 GB of identical bytes, we store it twice (v0.2 target).
4. **Compressed templates are all-or-nothing.** Restoring `disk.img.gz` gunzips the whole 40 GB up front.
5. **No trim / reclaim loop.** A VM that writes 100 GB and deletes 80 GB still takes 100 GB on disk.
6. **No integrity check.** If `disk.img` gets silently corrupted, nothing tells you until boot fails.

---

## Proposal

### Two-layer model (revised: was three)

```
   Transport     OCI blobs on any registry (ghcr, ECR, Docker Hub, private)
                        ↕
   Store         Content-addressed blob store at ~/.vz/store/ (v0.2 ONLY)
                        ↕
   Runtime       Per-VM disk.img — unchanged. clonefile snapshots unchanged.
```

- **Transport** — push/pull disk images as OCI manifests. Pull consumes both cove-native and lume images; push emits cove-native only.
- **Store** — content-addressed local cache for *pulled* blobs. **v0.2 only.** In v0.1 there is no store: pull streams straight to `disk.img`.
- **Runtime** — existing per-VM directory. When a VM is sourced from OCI, `disk.img` is eagerly reassembled (v0.1: directly from network; v0.2: from store blobs). **Snapshots stay clonefile-based.**

### On-disk layout (v0.1 — no additions to `~/.vz/`)

v0.1 adds no new directories under `~/.vz/`. The only new file is an optional `disk.provenance` marker inside the per-VM directory.

```
~/.vz/vms/<name>/
├── disk.img                # raw sparse file (unchanged); only present after successful pull
├── disk.img.partial        # NEW (transient): streaming-pull scratch file; renamed to disk.img on success
├── disk.provenance         # NEW (optional): source manifest digest if OCI-sourced
└── ... (unchanged)
```

**`disk.provenance` v0.1 is UNSIGNED and INFORMATIONAL ONLY.** A local attacker with write access to the VM directory can edit it to claim different provenance. Signed provenance (embedded in VZ auxiliary storage, Ed25519-signed) is a v0.2 roadmap item — see "Known gaps" below.

### On-disk layout (v0.2 — store arrives)

```
~/.vz/
├── store/                          # content-addressed blob store (v0.2 ONLY — not created in v0.1)
│   ├── blobs/sha256/<digest>       # immutable LZ4-compressed chunks
│   ├── manifests/<ref>.json        # parsed OCI manifests, keyed by image ref
│   ├── index.json                  # tag-to-manifest-digest mapping
│   └── gc.lock                     # flock for safe GC (P0 add)
├── vms/<name>/                     # UNCHANGED
│   ├── disk.img                    # raw sparse file
│   ├── disk-snapshots/<n>/         # UNCHANGED — APFS clonefile
│   ├── disk.provenance             # optional: source manifest digest
│   └── ... (unchanged)
└── cache/                          # pull/push chunk staging, safe to delete
```

VMs not sourced from OCI get today's layout verbatim. No format migration, no breaking changes.

---

## OCI distribution (v0.1)

### Manifest shape (cove-native on push)

Cove emits its own annotation namespace on push. Lume interop is **read-only** (pull).

```json
{
  "schemaVersion": 2,
  "mediaType": "application/vnd.oci.image.manifest.v1+json",
  "config": {
    "mediaType": "application/vnd.oci.image.config.v1+json",
    "size": 442,
    "digest": "sha256:..."
  },
  "layers": [
    { "mediaType": "application/octet-stream", "size": 524288, "digest": "sha256:...",
      "annotations": { "org.tmc.cove.role": "nvram" } },
    { "mediaType": "application/octet-stream+lz4", "size": 387112834,
      "digest": "sha256:...",
      "annotations": {
        "org.tmc.cove.uncompressed-size": "536870912",
        "org.tmc.cove.uncompressed-content-digest": "sha256:...",
        "org.tmc.cove.chunk-index": "0",
        "org.tmc.cove.chunk-total": "180"
      }
    }
    // ... chunks 1..179
  ],
  "annotations": {
    "org.tmc.cove.upload-time": "2026-04-16T12:00:00Z",
    "org.tmc.cove.uncompressed-disk-size": "96636764160",
    "org.tmc.cove.hw-model-digest": "sha256:...",
    "org.tmc.cove.aux-digest": "sha256:...",
    "org.tmc.cove.base-manifest": "sha256:..."
  }
}
```

### Annotation key mapping (pull-side translation)

The pull path accepts both cove-native and lume annotation sets. When a lume-produced manifest is encountered, the parser translates lume keys into our internal representation before any downstream code sees them.

| Lume key (input)                               | Cove key (internal)                     | Scope      |
|------------------------------------------------|-----------------------------------------|------------|
| `org.trycua.lume.uncompressed-size`            | `org.tmc.cove.uncompressed-size`        | per-layer  |
| `org.trycua.lume.uncompressed-content-digest`  | `org.tmc.cove.uncompressed-content-digest` | per-layer |
| `org.trycua.lume.chunk-index`                  | `org.tmc.cove.chunk-index`              | per-layer  |
| `org.trycua.lume.chunk-total`                  | `org.tmc.cove.chunk-total`              | per-layer  |
| `org.trycua.lume.role`                         | `org.tmc.cove.role`                     | per-layer  |
| `org.trycua.lume.upload-time`                  | `org.tmc.cove.upload-time`              | manifest   |
| `org.trycua.lume.uncompressed-disk-size`       | `org.tmc.cove.uncompressed-disk-size`   | manifest   |

Translation is one-way (pull only). If both sets appear on the same manifest (unlikely), cove-native wins; lume keys are ignored with a debug log. The translation table starts in `internal/ociimage/annotations.go` and is covered by golden tests using a canonical lume-produced manifest fixture.

### Chunking

- **512 MB fixed-offset chunks** by default. Matches lume default at `Push.swift:28` and `ImageContainerRegistry.swift:3077` — purely because that's a reasonable size, not for compatibility (we don't produce lume images).
- **LZ4 frame compression** per chunk.
- **Fixed-offset, not content-defined.** Rabin-style rolling hash would get better dedup but is too slow in pure Go without CGO/SIMD. Fixed offsets make resumable uploads and parallel HTTP range requests trivial. Defer rolling-hash to v0.3 if dedup pressure justifies it.
- **Sparse-zero chunks** get a well-known zero-digest; never uploaded twice. Registry-side dedup plus local skip.

### `cove pull` (v0.1 — streaming direct-to-disk, no store)

```
cove pull ghcr.io/me/macos-15:base
cove pull ghcr.io/me/macos-15:base --as macos-15-vm    # name the VM
```

Steps (v0.1):
1. **Liveness gate (control-socket probe).** If the target VM directory exists and contains `disk.img`, probe liveness by dialing `~/.vz/vms/<name>/control.sock` with a **500ms deadline** and issuing a status ping. If the dial succeeds and the status ping returns within the deadline, the VM is actually running — fatal the pull with: `cove pull: cannot pull into an active VM '<name>'. Stop the VM first: cove ctl stop <name>`. If the dial fails (connection refused, timeout, or no socket file), the VM is NOT running; before proceeding, clean up any stale sock file at that path, then continue with the pull. Rationale: APFS `os.Rename` will silently overwrite an actively-opened file — the directory entry flips to the new `disk.img` while the running `cove run` process keeps mutating the orphaned inode through its existing fd. The user's next boot would see the pulled disk; the running VM's writes would vanish into a ghost inode. A PID-alive or socket-exists check is not sufficient: a crashed `cove run` can leave a stale `control.sock` behind, which would spuriously block subsequent pulls. Probing the socket with a short deadline is the only signal that reliably distinguishes "VM is actually serving" from "sock file happens to exist." Gating up front, before `disk.img.partial` is even created, is the only safe move.
2. `GET /v2/<repo>/manifests/<tag>` → manifest.
3. Parse annotations through the cove/lume translation table; reject if required keys missing.
4. Pre-allocate `~/.vz/vms/<name>/disk.img.partial` as a sparse file of `uncompressed-disk-size` bytes (`ftruncate`). **Writes always target `disk.img.partial`, never `disk.img` directly.**
5. Stream missing layers with parallel HTTP range requests (N=4). For each chunk:
   - LZ4-decompress in memory.
   - Verify `uncompressed-content-digest`.
   - **`f.WriteAt(decompressed, chunk-index * chunk-size)`** into `disk.img.partial` — write directly at the fixed offset; no intermediate `.lz4` blob on disk.
   - Skip chunks whose uncompressed digest matches the zero-digest; `WriteAt` into a sparse file already yields a hole.
6. After all chunks have been written and verified, confirm the manifest-level invariants (all chunks present, aggregate sha256 matches manifest digest where applicable).
7. **Atomically rename** `disk.img.partial` → `disk.img` (`os.Rename`, same-directory, same-filesystem — guaranteed atomic on APFS).
8. Write `aux.img` and `hw.model` from the cove-specific annotations (full blobs, small).
9. Write `disk.provenance` with the manifest digest **only after the rename succeeds**. The presence of `disk.provenance` therefore implies a fully-written, verified disk.

**Interrupted-pull handling**: if any step fails before the atomic rename, `disk.img.partial` is left on disk and `disk.img` is absent. On startup, `cove run <name>` inspects the VM directory: if `disk.img.partial` exists, it refuses to boot with:

> `cove: VM <name> has incomplete disk (pull was interrupted). Delete <path> and rerun cove pull, or use cove pull --resume <ref> to continue.`

(The `--resume` flag is a v0.2 feature; in v0.1 the user deletes the partial and reruns. The error message names both paths so the docs don't need to branch by version.)

There is no `~/.vz/store/` in v0.1. There is no `cove gc` in v0.1 (nothing to collect). Resumable pulls are v0.2 — if a v0.1 pull is interrupted, the user deletes `disk.img.partial` and reruns.

No lazy mode, no FUSE. Eager is the only path in v0.1. The `--lazy` flag is reserved for a later Apple-native implementation (FileProvider/NFS), not macFUSE.

### `cove push`

```
cove push my-vm ghcr.io/me/macos-15:tag
cove push my-vm ghcr.io/me/macos-15:tag --base ghcr.io/me/macos-15:base
cove push my-vm ghcr.io/me/macos-15:tag --chunk-size 256
cove push my-vm ghcr.io/me/macos-15:tag --dry-run
cove push my-vm ghcr.io/me/macos-15:tag --lume-compat    # emit dual annotations
```

Steps:
1. If `--base` given, pull its manifest to diff against. Only push chunks whose content digest differs (delta push).
2. Split `disk.img` into fixed-size chunks (stream-read, no full materialization).
3. For each chunk: content digest (uncompressed) → LZ4 compress → compressed digest.
4. `HEAD /v2/<repo>/blobs/<digest>` to check presence. Skip if already on registry.
5. Upload missing blobs in parallel.
6. Build manifest using **`org.tmc.cove.*` annotations only**. No `org.trycua.lume.*` keys are written — **unless** `--lume-compat` was passed, in which case the manifest carries **both** annotation sets (identical values, cove-native keys and lume-compatible keys side by side). This lets a single pushed image be consumed by both cove and lume pull paths.
7. Tag with `:tag` and any `--additional-tag` values.

**`--lume-compat` semantics**: opt-in, off by default. When enabled, every `org.tmc.cove.*` annotation (per-layer and manifest-level) is mirrored with its `org.trycua.lume.*` counterpart using the same translation table defined for pull. The default remains cove-native-only so that cove's schema evolution isn't constrained by lume's. Users on mixed-tool teams, or anyone publishing images meant to be consumed by lume installations, pass `--lume-compat` on those specific pushes.

### Auth

Standard OCI token dance (matches `ImageContainerRegistry.swift:1940`). Credential precedence (matches Docker, to minimize surprise):

1. **macOS keychain** via `docker-credential-osxkeychain` helper (if present in `~/.docker/config.json`).
2. **`~/.docker/config.json`** auth entries.
3. **Environment**: `COVE_REGISTRY_TOKEN`, `GITHUB_TOKEN` (only for `ghcr.io`).
4. **Anonymous** (pull-only).

Docs explicitly call out the credential helper requirement and setup instructions.

### Library choice

[`go-containerregistry`](https://github.com/google/go-containerregistry) — handles manifests, blob uploads, auth (including keychain helpers), reference parsing. Net-new cove code is ~400 LOC (chunk metadata, zero-detect, annotation schema). Everything else is library calls.

---

## Content-addressed store (v0.2)

### What it enables

1. **Cross-VM dedup for pulled images.** Two VMs pulled from images sharing chunks use the same blobs on disk.
2. **Resumable pulls.** Interrupted pull? Already-downloaded blobs remain in the store; rerun skips them.
3. **Integrity.** Every blob is named by its SHA-256. Silent corruption → digest mismatch → refuse to use, re-pull.
4. **GC-able.** `cove store gc` walks all VMs + templates + known manifests, marks reachable blobs, deletes unreferenced ones.

### Store operations

```go
type Store interface {
    Has(digest string) bool
    Get(digest string) (io.ReadCloser, error)
    Put(r io.Reader) (digest string, err error)
    Link(digest, dst string) error            // APFS clonefile where possible, copy otherwise
    GC(reachable map[string]bool) (reclaimed int64, err error)
}
```

### GC safety (P0 from Council) — v0.2 only

GC is **not** a naïve mark-and-sweep. Three protections:

1. **Exclusive flock** on `~/.vz/store/gc.lock` throughout the entire GC run. `cove pull` takes a shared lock; GC can't start while a pull is active, and a pull can't start while GC is running.
2. **Mtime grace window.** Blobs with `mtime` younger than `1h` are *never* deleted, even if unreferenced. Covers the case of a `cove pull` that wrote blobs but hasn't yet updated the manifest-to-blob reference graph.
3. **Reference graph built from a snapshot, not live.** GC reads the VM and template manifests under the flock, then sweeps. No live VM can concurrently modify the reachable set.

Ties into `07-reliability-recovery-audit.md` — GC is the first subsystem to get proper file locking; establishes the pattern for the rest. Only relevant once the store exists (v0.2).

### `disk.img` assembly (eager only in v0.x)

v0.1 reassembles chunks directly from network → `disk.img`. v0.2 reassembles from store blobs → `disk.img`. Either way: single pass of seek-and-write into a sparse file. No FUSE, no composition at read time.

### Store ↔ template interop (v0.2)

`cove template save --fast` **optionally** writes chunks into the store rather than a separate `disk.img`. Gated by a flag; existing templates keep their format (compressed `.gz` or uncompressed `disk.img`) and continue to work. The store-backed path is an opt-in optimization for users running many VMs off shared bases.

---

## Snapshots (unchanged — Council P0 directive)

**Local snapshots keep APFS `clonefile` semantics.** No manifest refactor. No chunk reassembly. 0-byte, 0-ms restore.

What *does* move forward:

- `cove snapshot tree` — visualize parent/child lineage from existing metadata (cheap win, no format change).
- `cove snapshot push <name> <ref>` — push a `clonefile`-based snapshot *as* an OCI manifest when crossing the network boundary. The snapshot stays clonefile locally; the push-time path runs the same chunker used by `cove push`.

This gets us portable snapshots (the product win) without paying the local-restore regression (the architectural cost).

---

## Sparse handling, trim, reclaim

Two problems: disk images grow monotonically, and sparse-hole info is lost on copy/compress.

### Trim flow

- Guest `TRIM/DISCARD` → Virtio-blk → host.
- Host `disk.img` is sparse on APFS; VZ framework forwards trim to punch holes.
- **Action**: verify `VZVirtioBlockDeviceConfiguration` has trim/discard enabled. Document APFS-only behavior; other host filesystems may not reclaim.

### Push-time sparse detection

- During `cove push`, detect all-zero chunks by scanning the buffer.
- Record a well-known zero-digest; don't upload.
- Reader sees zero-digest, seeks past the region in the sparse output.

### `cove compact` (agent-aware)

New subcommand. Runs hot (VM up, agent connected) — requires the guest agent so we can issue the free-space zero-fill from inside the guest.

Implementation (v0.1):

1. **Detect guest platform** via `currentVMAgentPlatform()` (`agent_inject.go:31`). This returns the platform string recorded during agent injection.
2. **Check agent connectivity**. If disconnected (`agent_control.go:121` reports no live connection), fail with a clear error (`cove compact: guest agent not reachable; start the VM and wait for agent to come up`) rather than hanging on a vsock dial.
3. **Route to platform-appropriate command** via `UserExec` (`agent_client.go:34`):
   - **Linux guest** → `fstrim -v /` (and any additional mounted filesystems if configured).
   - **macOS guest** → `diskutil apfs eraseFreeVolumeSpace free /` (zero-fill pattern on the free space so host hole-punching sees zeros).
4. **Host-side punch**. After the guest command completes, the host sees newly-zeroed regions in `disk.img`. APFS forwards trim via the VZ framework, so in most cases the holes are punched automatically. As a fallback, we can scan and explicitly `fcntl(F_PUNCHHOLE)` any all-zero 64 KB aligned runs.

Failure modes:

| Condition | Behavior |
|---|---|
| Agent disconnected | Exit 1 with `guest agent not reachable` error; no side effects. |
| Unknown guest platform | Exit 1 with `unsupported guest platform for compact`; no side effects. |
| `fstrim`/`diskutil` nonzero exit | Surface stderr to user; exit 1 without attempting host-side punch. |

Documented as optional maintenance. Recommended after heavy churn or before a `cove push` to minimize uploaded bytes.

---

## CLI surface

```
# Pull / push (v0.1)
cove pull <ref>                       # pull image (writes disk.img.partial, renames on success)
cove pull <ref> --as <name>           # name the new VM
cove push <vm> <ref>                  # push VM disk as OCI image (cove-native annotations)
cove push <vm> <ref> --base <ref>     # delta push
cove push <vm> <ref> --dry-run        # chunk + compress locally, no upload
cove push <vm> <ref> --lume-compat    # emit dual cove-native + lume annotations (opt-in)

# Store (v0.2 — does not exist in v0.1)
cove store list                       # blobs, refs, disk usage
cove store gc                         # garbage collect (flock + 1h mtime guard)
cove store inspect <digest>           # show metadata for a blob
cove store verify                     # re-hash every blob, report mismatches

# Images (v0.1 for `list` / `rm`; `tag` in v0.2)
cove images list                      # all pulled/tagged images
cove images tag <src-ref> <new-ref>   # add a local alias (v0.2)
cove images rm <ref>                  # delete tag; GC removes unreferenced blobs (v0.2)

# Templates (UNCHANGED surface; v0.2 adds store-backed opt-in)
cove template save <vm> <name>        # compressed (default) or --fast (clonefile)
cove template save <vm> <name> --store  # v0.2: store-backed
cove template list
cove template inspect <name>          # shows manifest and dedup stats if store-backed

# Snapshots (UNCHANGED local semantics)
cove snapshot save <name>             # APFS clonefile (today's behavior)
cove snapshot tree                    # NEW: visualize chain
cove snapshot push <name> <ref>       # NEW: push as OCI tag

# Maintenance
cove compact <vm>                     # agent-driven free-space reclaim (hot, requires agent)
```

---

## Compatibility matrix

| Push side | Pull side | Works? | Notes |
|---|---|---|---|
| Cove push (cove-native annotations) | Cove pull | ✓ | Primary path. |
| Cove push | Lume pull | ✗ | Lume won't recognize `org.tmc.cove.*` annotations. |
| **Cove push with `--lume-compat`** | **Lume pull** | **✓** | **Dual annotations emitted; opt-in escape valve for mixed-tool teams.** |
| Cove push with `--lume-compat` | Cove pull | ✓ | Cove-native keys still present alongside lume keys. |
| Lume push (`lume push`) | Cove pull | ✓ | Translation table maps `org.trycua.lume.*` → internal form. |
| Lume push | Lume pull | ✓ | Unchanged; we're not in the path. |
| Plain OCI tar layers | Cove pull | ✗ | Out of scope in v0. |

### Why asymmetric

**Cove pulls lume images so migration is one command.** Cove pushes cove-native images by default for schema independence — our images stay standard OCI (readable by any registry, inspectable with `crane`/`skopeo`), and we own our schema rather than tracking lume's evolution. **If you need lume-compatible output (e.g., for mixed-tool teams), pass `--lume-compat`** on the push and the manifest carries both annotation sets.

So the defaults optimize for the common cases — frictionless migration in, independent evolution out — while `--lume-compat` is a one-flag escape valve for anyone who needs interop on the push side. No user is forced into a dialect they didn't ask for, and cove's forward trajectory isn't coupled to a sibling project's schema decisions.

---

## Risks & mitigations

| Risk | Mitigation |
|---|---|
| v0.1 pull interrupted, user must restart from zero | Atomic `disk.img.partial` rename prevents half-written disks; `cove run` refuses to boot VMs with a lingering partial; v0.2 store gives resumability |
| cove pull into a running VM would orphan inode via APFS rename-while-open | Control-socket probe with 500ms deadline at pull start; fatal if VM responds, cleanup-and-proceed if stale sock |
| Store blobs fill the disk (v0.2) | `cove store gc` + size-capped cache with LRU eviction; warn at 80% |
| APFS clonefile fails (non-APFS, cross-volume) | Fall back to `io.Copy` with warning; log volume info |
| Lume annotation schema drifts | Pin lume keys in `internal/ociimage/annotations.go` translation table; CI golden-test against a canonical lume image fixture |
| Large pull on bad network | Parallel range requests; integrity via digest; v0.2 adds resume |
| Zero-chunk false-positive | Explicit zero-digest layers; sparse `WriteAt` skips zero chunks correctly |
| **GC deletes in-flight blobs (v0.2)** | **flock + 1h mtime grace (P0 add)** |
| Guest trim doesn't reach host | Document APFS-only; provide `cove compact` fallback |
| `cove compact` hangs if agent is down | **Explicit connectivity check via `agent_control.go:121`; fail fast with clear error** |
| Clone.go API churn | `clone.go` stays as the local-snapshot primitive; new code calls into it |
| Credential helper missing | Docs call out setup; fall back to env vars with clear error |

---

## Scope

| Piece | LOC | Days | Milestone |
|---|---|---|---|
| `registry/oci_client.go` — push/pull via go-containerregistry | ~400 | 2 | v0.1 |
| `internal/ociimage/chunks.go` — fixed-size chunk metadata + zero-detect | ~200 | 1 | v0.1 |
| `internal/ociimage/annotations.go` — cove-native schema + lume translation table | ~200 | 0.75 | v0.1 |
| `registry/direct_writer.go` — streaming decompress + `WriteAt` into `disk.img` | ~150 | 0.5 | v0.1 |
| `images.go` — `cove images list/rm` subcommand | ~100 | 0.5 | v0.1 |
| `compact.go` — agent-aware `cove compact` | ~150 | 0.5 | v0.1 |
| Tests — v0.1 golden + lume-in translation + compact-agent-down | ~250 | 1.25 | v0.1 |
| Docs — `push-pull.md`, credential helper setup | ~100 | 0.5 | v0.1 |
| **v0.1 subtotal** | **~1350** | **~6** | |
| `store/store.go` — content-addressed store + **GC with flock/mtime** | ~450 | 2 | v0.2 |
| `registry/store_pull.go` — switch pull to store-backed + resume | ~200 | 0.75 | v0.2 |
| `template_store.go` — opt-in store-backed template path | ~150 | 0.5 | v0.2 |
| `snapshot_push.go` — push a clonefile snapshot as OCI | ~150 | 0.5 | v0.2 |
| `snapshot_tree.go` — lineage visualization | ~100 | 0.5 | v0.2 |
| `images_tag.go` — local tag alias | ~50 | 0.25 | v0.2 |
| Tests — concurrent GC, store verify, snapshot push, resume | ~300 | 1.5 | v0.2 |
| Docs — `store.md`, `snapshots.md` updates | ~100 | 0.5 | v0.2 |
| **v0.2 subtotal** | **~1500** | **~6.5** | |
| **Total** | **~2850** | **~12.5** | |

v0.2 additions from v3 review:
- **Signed `disk.provenance`** via Ed25519 key embedded in `aux.img`. Verifies VM directory integrity for compliance/CI use cases where an unsigned marker is insufficient.

v0.3 (out of scope): content-defined chunking, Apple-native lazy assembly (FileProvider/NFS), cross-host suspend-state transfer.

---

## Known gaps (tracked, not v0.1)

Separate from Risks (which we *have* mitigated). These are acknowledged holes we ship v0.1 with eyes open:

- **Agent upgrade path.** How does a user with 12 running VMs upgrade `vz-agent`? Today: reinstall each VM. Needs design by v0.2. Owner: TBD (MUST be assigned before v0.2 planning begins; unassigned gaps rot).
- **Signed disk.provenance.** v0.1 is unsigned/informational. Signed variant in v0.2.

---

## Open questions (post-round-2)

1. **Chunk size default.** 512 MB. Keep as default; expose `--chunk-size` for experimentation. Any appetite to bump to 1 GB for large base images?
2. **Delta push auto-detection.** `--base <ref>` is explicit. Auto-detect via `disk.provenance` + registry lookup when the user omits it?
3. **Credential helper on fresh macOS installs.** `docker-credential-osxkeychain` isn't always present without Docker Desktop. Ship a minimal stub in our brew cask, or require users to install it separately?
4. **Zero-chunk well-known digest.** Fixed digest for 512 MB zero buffer, or per-chunk-size? Per-size is more flexible but needs a tiny table of "known zero" digests.
5. **`cove images rm` GC trigger (v0.2).** Auto-run GC after image removal, or require explicit `cove store gc`? Auto is friendlier; explicit is safer on large stores.
6. **v0.2 store rollout.** Ship v0.2 as opt-in (`--store` flag) first, flip default in v0.3? Or default-on v0.2 with an escape hatch? Opt-in reduces blast radius.
7. **Compact on macOS guest.** `diskutil apfs eraseFreeVolumeSpace free /` can take a long time on large disks. Worth a `--timeout` flag and a progress surface through the agent?

---

## What this unlocks

- **One-way lume migration on `pull`** — day one. Users come to cove, their library comes with them.
- **Portable snapshots** via `cove snapshot push` — without regressing local `clonefile` restore speed.
- **CI reproducibility** — tag a golden VM image, pull fresh per job, tear down. No per-runner install.
- **Schema independence** — cove images are standard OCI, not tied to lume's evolution.
- **APFS `clonefile` stays a moat** — lume has no equivalent for 0-ms local snapshot restore.

Long-term frame: **cove is the filesystem layer between Apple's Virtualization.framework and the container registry ecosystem**, with APFS CoW as the local-performance superpower. Everything we build after this slots into that layer.
