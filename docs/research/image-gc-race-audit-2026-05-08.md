# `cove image gc` race audit (2026-05-08)

Scope: concurrency between `cove image gc` (CLI, `image_gc.go`) and the
coved scheduler (`internal/coved/image_gc.go`) versus `image build`,
`run -fork-from <image>`, and other gc invocations. Audit only — no
fixes in this round (per R76 brief).

## Reference set protocol

Both gc paths derive the live-fork set from `vmconfig.Load(<vmDir>)`
reading `cfg.ParentImage`:

- CLI: `VMsForkedFromImage` at image.go:464 — `os.ReadDir` of
  `vmconfig.BaseDir()`, load each config, match `ParentImage`.
- coved: `referencedImages` at internal/coved/image_gc.go:229 — direct
  `json.Unmarshal` of `parentImage`.

The materialization writer is `MaterializeImage` at image.go:535. It
order-of-operations:

1. `os.MkdirAll(childDir)` (line 550)
2. `materializeImageFiles` clonefile/copy disk + aux (line 560)
3. `vmconfig.Load(childDir)` then mutate `cfg.ParentImage` (line 565-570)
4. `vmconfig.Save(childDir, cfg)` (line 576)

`ParentImage` is only visible to gc readers AFTER step 4. Steps 2-3
can take seconds for a multi-GB clonefile + aux byte-copy.

## Identified races

### R1. fork-from materialization invisible to gc — P1

CLI `GCImages` (image_gc.go:76) calls `VMsForkedFromImage`, then again
at line 100 just before `os.RemoveAll`. Both calls only see VMs whose
`config.json` is fully written with `ParentImage` set. A `cove run
-fork-from <ref>` started between line 100 and the RemoveAll at line
126, OR already in steps 1-3 of `MaterializeImage`, will not appear.
gc then `os.RemoveAll(ref.Path())` succeeds. The child's clonefile'd
disk inodes survive (CoW), so the running VM keeps booting, but:

- `manifest.json` is gone → `cove image verify`/`inspect` for the
  child report missing parent → provenance chain broken.
- A subsequent `image build` from the same child cannot record
  `BaseImage`/`SourceImage` accurately.
- No data loss, but silent corruption of audit chain.

Severity: P1. Recommended fix: file lock on `<image>/refcount.lock` or
on the image dir itself, acquired by both `MaterializeImage` (held
through Save) and gc (held through recheck+RemoveAll). Alternatively,
write a sentinel `materializing.<pid>` into the child dir at step 1
and have gc enumerate child dirs (not just configs) for unfinished
materializations.

### R2. coved scheduler has no recheck — P1

`runOnceLocked` (internal/coved/image_gc.go:95) reads `referencedImages`
once at line 105, then sweeps without re-reading. The window between
read and `os.RemoveAll` (line 120) covers the entire `listImages`
walk plus per-image work. Same failure mode as R1, larger window.

Severity: P1. Recommended fix: identical to R1, plus port the CLI's
recheck-immediately-before-remove (image_gc.go:100) into the loop.

### R3. CLI vs coved concurrent gc — P2

CLI `runImageGC` does NOT acquire `~/.vz/image-gc.lock`; coved does
(internal/coved/image_gc.go:177). A user-invoked `cove image gc`
running while the coved scheduler tick fires can both target the same
ref. Whichever loses the RemoveAll race gets ENOENT, lands in
`Skipped` with "remove failed" — benign, but emits misleading
metrics.

Severity: P2. Fix: have CLI gc take the same `image-gc.lock`.

### R4. Stale `image-gc.lock` after coved crash — P2

`acquireLock` uses `O_CREATE|O_EXCL` with no PID-liveness check on
existing lock (internal/coved/image_gc.go:182). If coved crashes mid-
sweep, the lock file persists and every future scheduled run silently
returns `Skipped: true`. No alerting. gc effectively dies until host
reboot or manual cleanup.

Severity: P2. Fix: write PID to lock (already done), on EEXIST read
the PID and use `kill -0` / `os.FindProcess` to check liveness; if
dead, claim the lock.

### R5. No manifest fsync ordering — P2

`writeImageManifest` (image.go:324) does `os.WriteFile(tmp)` then
`os.Rename(tmp, path)` with no `fsync(tmp)` and no `fsync(parentDir)`.
On host crash mid-build the rename can be visible while file contents
are zero-length / truncated. `LoadImageManifest` then errors, so
`ListImages` quietly drops the entry → gc never sees it → image leaks
forever (orphan disk under `~/.vz/images/<name>/<tag>/`).

Severity: P2. Fix: open tmp with O_SYNC, or fsync(tmp) + fsync(dir)
before/after rename. Independently, gc should sweep image dirs
without a readable manifest after a generous TTL.

### R6. coved scheduler ignores cache/* TTL — P2

CLI gc honors `cache/*` ref prefix and `CACHE-TTL` (image_gc.go:53,
182). coved gc has no such logic — it deletes any unreferenced image
on the first scheduled tick regardless of `cache/` semantics.
Divergent policy: `cove pull` cache images get evicted faster than
documented when coved runs them.

Severity: P2 (policy bug, not a race per se, but surfaced by audit).
Fix: factor TTL+cache check into a shared helper used by both paths.

### R7. coved gc deletes whole tree without manifest re-stat — P2

`runOnceLocked` does not re-stat `manifest.json` before RemoveAll. If
a concurrent `image tag` (image_tag.go:53) is rewriting the manifest
via tmp+rename in the SAME image dir, the gc remove can race the
rename and leave a half-tree. APFS makes this less likely (RemoveAll
walks then unlinks; rename is atomic) but the windows overlap.

Severity: P2 (paper race; haven't reproduced). Fix: same dir lock as
R1.

### R8. `image build` partial-dir invisible to gc — informational

`BuildImage` `os.MkdirAll(imgDir)` at image.go:218 happens BEFORE
manifest write at line 283. `ListImages` requires `manifest.json` to
exist, so the half-built dir is correctly invisible to gc. On
`cleanup()` failure path the dir is removed. No race here. (Documented
to confirm not a hole.)

## Severity summary

- P0: 0
- P1: 2 (R1 fork-from invisible to CLI gc; R2 coved no recheck)
- P2: 5 (R3 CLI vs coved concurrency; R4 stale lock; R5 fsync; R6
  cache TTL divergence; R7 tag-vs-gc paper race)
- Informational: 1 (R8)

## Cross-cutting recommendation

The gc/build/materialize trio currently coordinates only via
`vmconfig` config-file presence. A single per-image `flock`
(`<image>/.lock` advisory) acquired write-side by `BuildImage`, gc
delete, and `image tag`, and read-side by `MaterializeImage` (held
through `vmconfig.Save`) would close R1, R2, R3, R7 in one stroke.
This matches the existing `run.lock` pattern already used for VM
bundles (see `acquireRunLockHook` references in image.go:203 and
image_fork.go:97).
