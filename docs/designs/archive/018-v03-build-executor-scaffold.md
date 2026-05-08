# v0.3 build executor scaffold

**Status**: implemented (Slice 1 shipped).
**Source**: NotebookLM notebook `79a32e96-8e1c-4e89-9385-20193e3a8209`,
conversation `90dd1dda-c60b-4994-886f-547205ddf126`, plus local code review of
`build.go`, `build_cache.go`, `build_layer.go`, `fork.go`, and `clone.go`.
**Slice**: [017 Slice 1](017-v03-execution-roadmap.md#slice-1-build-executor-scaffold).

## Goal

Add the internal scaffolding that later `cove build` slices can use for real
execution, without exposing non-dry-run builds yet. This slice should make the
next branch easier to review by settling scratch directory layout, ownership,
lock files, stale cleanup, and tests.

The public command remained, until Slice 3 landed local-base execution:

```text
cove build: only --dry-run is implemented
```

until Slice 3 actually ran a missed step inside a VM and persisted a usable
layer.

## Internal shape

Keep the code concrete and small. The executor owns a plan, options, store, and
scratch root. Do not introduce a framework or broad interface hierarchy.

```go
type buildExecutor struct {
	plan        buildPlan
	opts        buildOptions
	store       store.Store
	scratchRoot string
	now         func() time.Time
	pid         int
}

type buildScratch struct {
	ID       string
	Dir      string
	PIDPath  string
	DiskPath string
	LogPath  string
	Created  time.Time
}

func newBuildExecutor(plan buildPlan, opts buildOptions, s store.Store) *buildExecutor
func (e *buildExecutor) Execute(ctx context.Context) error
func (e *buildExecutor) createScratch(ctx context.Context, parentDisk string) (buildScratch, error)
func (e *buildExecutor) cleanupScratch(sc buildScratch) error
func gcBuildScratch(root string, isLive func(int) bool) error
```

`Execute` in this slice was callable from tests only. It could create and
tear down scratch directories, but it returned a not-implemented error before
VM boot, cache application, block diffing, or metadata commit. (The
`errBuildCacheMissExecutionNotImplemented` sentinel still exists but only fires
when `runMiss == nil`, unreachable from production paths after Slice 3.)

## Scratch layout

Scratch VMs should not live under `~/.vz/vms`, because that would make list,
serve, MCP, and other VM-discovery code treat them as user-owned VMs.

Default layout:

```text
~/.vz/build-scratch/<id>/
  build.pid
  build.json
  disk.img
  build.log
```

Use an injected `scratchRoot` in tests. The production default should be derived
from `$HOME/.vz/build-scratch`, not from the store directory. The store remains
`~/.vz/store`; scratch state is transient VM state.

`build.json` should include only non-secret metadata:

```json
{
  "id": "20260430T033000Z-abcdef",
  "pid": 12345,
  "plan": "sha256:...",
  "created_at": "2026-04-30T03:30:00Z",
  "keep_intermediate": false
}
```

Do not write cache keys, cache-env values, or secret values to `build.json`
unless a later slice proves the need. `build.pid` exists for cheap stale-GC
checks; `build.json` is for debugging and future migration.

## Disk creation

Slice 1 only needs a parent disk clone helper, not full VM cloning.

- Use `ForkVMDisk(parent, child)` when the parent disk exists on an APFS volume.
- Do not fall back to byte-copy silently. If APFS clonefile fails, return the
  error so the build path knows it lost the cheap scratch invariant.
- Do not generate `machine.id`, copy `aux.img`, or mutate VM config in Slice 1.
  Those belong to Slice 3, when the scratch VM actually boots.

For tests that do not run on APFS, keep a pure metadata path:

- Unit-test scratch directory creation, pid files, and cleanup without requiring
  a real clonefile.
- Keep clonefile behavior covered by existing fork tests and add a small
  build-scratch test guarded by filesystem support if needed.

## Cleanup rules

Dry-run must stay side-effect free. Do not run scratch GC or create scratch
directories from the public `--dry-run` path.

Non-dry-run remained gated in `handleBuild` during Slice 1, so production users
could not trigger scratch creation. Tests could construct `buildExecutor` directly.

When Slice 3 removed the gate (now: local-base execution required), the executor path now does:

1. Run `gcBuildScratch` at the start of non-dry-run execution.
2. Create one scratch directory per active step attempt.
3. Remove scratch directories after successful step completion.
4. Remove scratch directories after failure unless `--keep-intermediate` is set.
5. Leave enough `build.json` and `build.log` context for debugging when
   `--keep-intermediate` is set.

Stale GC is conservative:

- If `build.pid` is missing or malformed, leave the directory and report it.
- If the PID is live, leave the directory.
- If the PID is dead, remove the directory.
- If removal fails, return an error that names the scratch directory.

The `isLive func(int) bool` hook keeps tests deterministic and avoids relying on
platform-specific signal behavior.

## Tests

Add tests before VM execution exists:

- `TestBuildScratchCreateWritesMetadata`: creates a scratch directory under
  `t.TempDir`, writes `build.pid` and `build.json`, and records no secret-bearing
  fields.
- `TestBuildScratchCleanupRemovesDir`: removes the scratch directory and treats
  an already-removed directory as success.
- `TestBuildScratchKeepIntermediate`: simulates a failing executor path with
  `KeepIntermediate: true` and verifies scratch state remains.
- `TestBuildScratchNoKeepIntermediateOnFailure`: same failure with
  `KeepIntermediate: false`, verifying cleanup.
- `TestGCBuildScratchLeavesLivePID`: uses an injected `isLive` function.
- `TestGCBuildScratchRemovesDeadPID`: uses an injected `isLive` function.
- `TestGCBuildScratchLeavesMalformedPID`: malformed or missing pid files are not
  deleted automatically.
- `TestHandleBuildNonDryRunStillGated`: preserves the current public behavior.
- `TestHandleBuildDryRunHasNoScratchSideEffects`: points `HOME` and scratch root
  at a temp directory and verifies dry-run does not create `build-scratch`.

The tests should stay table-driven where that keeps the setup clear. Avoid
network, registry, Virtualization.framework, and real VM dependencies.

## Non-goals

(Slice 1 non-goals — since superseded by Slices 2–4, except external secret-store URI work which is v0.4.)

- No VM boot.
- No guest-agent calls.
- No cache-hit layer application.
- No block-diff generation.
- No layer manifest writes.
- No `# secret:` tmpfs behavior.
- No external secret-store URI work.
- No public CLI promise beyond dry-run planning.
- No changes to public docs that imply non-dry-run `cove build` works.

## Handoff to Slice 2

Slice 2 built on this by replacing the "not implemented" executor step with
cache-hit materialization (see [019](019-v03-cache-hit-materialization.md)):

1. Resolve a cache entry.
2. Load and validate the layer manifest.
3. Create scratch state from the parent disk.
4. Apply the stored delta with `ApplyStoredDiskDelta`.
5. Commit no metadata until the apply succeeds.

The digest validation fixes already landed; Slice 2 should preserve that
fail-closed behavior before touching scratch disk state.
