# v0.3 cache-miss execution

**Status**: implemented (Slice 3 shipped on `origin/main` 8559c9a).
**Roadmap slice**: [017](017-v03-execution-roadmap.md) Slice 3.
**Branch**: `feat/v03-build-vm-execution` (merged to main)

## Purpose

Slice 3 was the point where `cove build` stopped being a planner only. It runs
missed vzscript steps inside a scratch VM, persists the resulting layer metadata,
and leaves final image state that the existing push path can consume.

All acceptance gates in this doc passed before the public dry-run-only gate
evolved into the local-base requirement; pushing registry-cache support remains
deferred.

## Execution contract

For each planned step:

1. If the step is a cache hit, validate the cache entry, layer manifest, and
   blobs before creating scratch state. Apply the layer to the current parent
   disk.
2. If the step is a cache miss, fork or materialize the current parent disk into
   a scratch VM.
3. Boot the scratch VM and wait for the guest agent.
4. Run the vzscript step through the existing guest-agent execution path.
5. Shut down cleanly. If shutdown fails, report the failure and keep scratch
   only when `--keep-intermediate` is set.
6. Diff the child disk against the parent disk.
7. Store changed blocks, write the layer manifest, and write the cache entry for
   the step key.
8. Use the child disk as the parent for the next step.

The second run with the same base, scripts, cache inputs, compact mode, and
agent protocol must take the cache-hit path without booting the guest for those
steps.

## Metadata

Each persisted cache entry records:

- step key
- parent digest used to compute the key
- script digest
- agent protocol version
- compact mode
- layer manifest digest
- creation time

The layer manifest is content-addressed separately from the key entry. A cache
entry is committed only after all changed blocks and the layer manifest are
stored successfully.

## Failure rules

- Invalid key, missing manifest, malformed manifest, or missing blob fails before
  scratch state is created.
- A cache miss that fails before guest execution leaves no cache entry.
- A guest execution failure leaves no cache entry and keeps scratch state only
  with `--keep-intermediate`.
- A diff or metadata-write failure leaves no cache entry for the failed step.
- Interrupted execution returns the context error where possible.

## Pushable result

At the end of a successful build, the final child disk must be represented in
the same image/export shape expected by `cove push`. Slice 3 may use the local
store only, but it must not invent a second image format for build output.

## Acceptance gates

- Cache misses execute in a VM.
- A second run with identical inputs hits cache and skips guest execution.
- Metadata survives process restart.
- Failed guest execution records a useful error and leaves scratch state only
  when `--keep-intermediate` is set.
- `go test ./...`
- A script-based CLI integration test builds one tiny local recipe and then
  proves the second run is a cache hit.

## Follow-on work

Secrets (Slice 4) and compaction (Slice 5) shipped after Slice 3, building on
its metadata hooks. No new public directives were added beyond what the planner
already accepted.
