# v0.3 cache-hit materialization

**Status**: implemented (Slice 2 shipped).
**Source**: NotebookLM notebook `79a32e96-8e1c-4e89-9385-20193e3a8209`,
conversation `90dd1dda-c60b-4994-886f-547205ddf126`, synced source
`049fafcb-5297-482a-aacf-53c2ec416568`
(`cove-current-v03-slice2-planning`), plus local code review of `build_layer.go`,
`build_cache_entry.go`, `block_diff.go`, and `internal/store/store.go`.
**Slice**: [017 Slice 2](017-v03-execution-roadmap.md#slice-2-cache-hit-materialization).

## Goal

Materialize a cached `cove build` layer into a scratch disk without booting a
guest. This slice wires the persistent build-cache metadata to the Slice 1
scratch lifecycle and the existing disk-delta functions:

- `loadBuildCacheEntry`
- `loadBuildLayerManifest`
- `ApplyStoredDiskDelta`

At the time of this slice, the public command remained dry-run-only until Slice
3. Slice 2 code was intended to be reachable from unit tests and internal
executor tests, not from the public non-dry-run CLI path. Slice 3 has since
landed local-base execution.

## Internal shape

Keep the cache-hit path as a small method on the executor from [018](018-v03-build-executor-scaffold.md).

```go
type buildApplyResult struct {
	Step        string
	Key         string
	LayerDigest string
	Scratch     buildScratch
	DiskPath    string
}

func (e *buildExecutor) applyCacheHit(ctx context.Context, step buildPlanStep, parentDisk string) (buildApplyResult, error)
func validateBuildLayerBlobs(ctx context.Context, s store.Store, manifest buildLayerManifest) error
```

`applyCacheHit` should do one thing: validate cache metadata, create scratch
state, apply the stored disk delta, and return the scratch disk path. It should
not decide whether the next step is a hit or miss; the executor loop owns that
decision.

## Validation order

All metadata must be validated before touching scratch disk state. The order is
part of the contract:

1. Validate `step.Key` with `splitStoreDigest`.
2. Load the cache entry with `loadBuildCacheEntry(e.store, step.Key)`.
3. Verify `entry.Key == step.Key`.
4. Validate `entry.LayerDigest` with `splitStoreDigest`.
5. Load the layer manifest with `loadBuildLayerManifest(e.store, entry.LayerDigest)`.
6. Verify `manifest.Digest == entry.LayerDigest`.
7. Validate every block reference by opening each blob with
   `store.OpenVerified(block.Digest, block.Size)` and closing it immediately.
8. Only after steps 1-7 succeed, create the scratch directory and apply the
   delta.

The earlier review finding on malformed manifest digests is closed; this slice
must preserve the same fail-closed behavior. A malformed key, layer digest, layer
manifest digest, or block digest should fail before `disk.img` exists in the
scratch directory.

## Disk apply

Use the existing delta application function:

```go
err := ApplyStoredDiskDelta(ctx, e.store, parentDisk, sc.DiskPath, manifest)
```

`ApplyStoredDiskDelta` already opens each block through `OpenVerified`, builds a
`diskDelta`, and calls `ApplyDiskDelta`. `ApplyDiskDelta` clones or copies the
parent to the child path, truncates to the manifest disk size, and writes changed
blocks.

The Slice 2 wrapper still performs up-front blob validation because the failure
mode is cleaner: if a blob is missing or malformed, no scratch disk should be
created at all.

## Failure atomicity

Cache-hit materialization should not corrupt permanent state.

- Do not write or update build-cache entries in this slice.
- Do not write final image metadata in this slice.
- Do not mark a build step complete until `ApplyStoredDiskDelta` returns nil.
- If validation fails before scratch creation, no scratch directory should be
  created.
- If apply fails after scratch creation, the scratch directory is tainted and
  should be removed unless `--keep-intermediate` is set.
- If cleanup fails, return an error that includes both the apply failure and the
  cleanup failure.

The permanent store is read-only on the hit path. The only writable location is
the scratch directory created by Slice 1.

## Tests

Add tests before any VM execution exists:

- `TestApplyCacheHitValidatesEntryBeforeScratch`: malformed `step.Key` or missing
  cache entry returns an error and does not create scratch state.
- `TestApplyCacheHitValidatesLayerBeforeScratch`: malformed `LayerDigest` or
  missing layer manifest returns an error and does not create scratch state.
- `TestApplyCacheHitValidatesBlocksBeforeScratch`: a layer manifest that
  references a missing or wrong-size block fails before scratch creation.
- `TestApplyCacheHitMaterializesDisk`: a small parent disk plus a stored delta
  produces the expected child disk bytes.
- `TestApplyCacheHitSkipsGuestExecution`: a fake executor hook records that no
  VM boot or guest execution function was called on a hit.
- `TestApplyCacheHitFailureCleansScratch`: injected apply failure removes scratch
  when `KeepIntermediate` is false.
- `TestApplyCacheHitFailureKeepsScratch`: the same failure keeps scratch when
  `KeepIntermediate` is true.
- `TestHandleBuildNonDryRunStillGated`: preserves the public CLI gate.

Use `t.TempDir` for store and scratch roots. Avoid registry, network,
Virtualization.framework, and real VM dependencies.

**Note (post-ship)**: 6/8 named tests landed verbatim. `TestApplyCacheHitSkipsGuestExecution` is structurally covered by `TestExecuteCacheHitsChainsLayers` (uses nil runMiss). `TestHandleBuildNonDryRunStillGated` is covered by the local-base gate test under a different name. Cosmetic.

## Docs

This was true while Slice 2 was in progress: public CLI docs should continue to
say that non-dry-run `cove build` is not implemented. Slice 3 has since landed
local-base execution, so current public docs now describe the local-base
execution path and keep registry bases planning-only.

An internal changelog note is optional; do not imply user-visible build
execution is available.

## Non-goals

(Slice 2 non-goals — since superseded by Slice 3 and follow-ons.)

- No VM boot.
- No guest-agent calls.
- No cache-miss execution.
- No block-diff creation for new layers.
- No build-cache entry writes.
- No final image export.
- No registry push.
- No `# secret:` tmpfs behavior.
- No compaction integration.
- No public CLI behavior change.

## Handoff to Slice 3

Slice 3 built on this by adding the miss path (see [020](020-v03-cache-miss-execution.md)):

1. Materialized or forked the parent disk into scratch state.
2. Booted the scratch VM.
3. Ran the missed vzscript step through the guest agent.
4. Shut down cleanly.
5. Diffed parent and child with `DiffDisks`.
6. Stored the delta with `StoreDiskDelta`.
7. Saved the layer manifest and cache entry only after the full miss path
   succeeded.

That is also the slice where the dry-run-only gate evolved into a local-base requirement (registry-cache support remains deferred).
