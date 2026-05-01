# v0.3 execution roadmap

**Status**: accepted planning input.
**Source**: NotebookLM notebook `79a32e96-8e1c-4e89-9385-20193e3a8209`, synced
source `ec2f0144-789a-446a-bb58-f0ce75492796`
(`cove-current-roadmap-v03-planning`).

## Purpose

This doc turns the v0.3 roadmap into reviewable implementation slices. It is
not a marketing plan. Every branch should be based directly on `origin/main`,
not on another speedrun branch, and should be small enough to review without
dragging unrelated roadmap work into the diff.

This roadmap began while `cove build` was dry-run-only. Slices 1-6 have since
landed on the roadmap build branch for local VM bases: cache hits skip guest
execution, misses run in scratch VMs, secrets mount through guest-only tmpfs or
RAM disk, compaction runs before diffing, layer manifests and cache entries are
verified before apply, and adapter/benchmark hardening is checked in. Registry-
base execution and registry cache import/export remain deferred — see the
defer list at the bottom for the full RC boundary.

## Slice 1: build executor scaffold

**Landed**: yes; the dry-run-only gate described here was removed later by
Slice 3 for local VM bases.

**Branch**: `feat/v03-build-executor-scaffold`
**Detailed plan**: [018](018-v03-build-executor-scaffold.md)

**Scope**:

- Add the internal execution types that turn a `buildPlan` into ordered work.
- Add scratch VM directory naming, locking, cleanup, and `--keep-intermediate`
  behavior.
- Keep `handleBuild` returning `cove build: only --dry-run is implemented` for
  non-dry-run invocations.

**Likely files**:

- `build.go`
- new `build_execute.go`
- new `build_scratch.go`
- `build_cache.go`
- `build_cache_test.go`

**Acceptance gates**:

- Unit tests cover scratch naming, cleanup, stale lock handling, and
  `--keep-intermediate`.
- Dry-run output is unchanged except for intentional wording changes.
- Non-dry-run still fails with the existing dry-run-only error.

**Docs**:

- No public CLI maturity change.
- Add a short changelog note only if internal scaffolding is worth tracking.

## Slice 2: cache-hit materialization

**Landed**: yes; public docs were updated later by Slice 3 when local-base
execution became supported.

**Branch**: `feat/v03-build-cache-hit-apply`
**Detailed plan**: [019](019-v03-cache-hit-materialization.md)

**Scope**:

- Load a persisted build-cache entry.
- Materialize the parent disk into a scratch VM.
- Apply a cached layer without booting the guest.
- Report cache-hit restoration in execution logs.

**Likely files**:

- `build_layer.go`
- `build_cache_entry.go`
- new `build_apply.go`
- new `block_delta.go` or extension of the existing block-delta code
- `internal/store`

**Acceptance gates**:

- Tests prove a cache hit skips guest execution.
- Layer digest validation rejects malformed or missing layer metadata before any
  scratch disk is modified.
- Interrupted cache-hit apply leaves no committed build metadata.

**Docs**:

- Keep public docs dry-run-only.
- Update the 003 design doc only if the implementation intentionally diverges
  from its block-delta format.

## Slice 3: cache-miss VM execution

**Landed**: yes; the public non-dry-run gate was removed for local VM
directory bases. Registry-base execution still fails with
`cove build: non-dry-run requires local VM base directory`.

**Branch**: `feat/v03-build-vm-execution`
**Detailed plan**: [020](020-v03-cache-miss-execution.md)

**Scope**:

- Fork or materialize the parent into a scratch VM.
- Boot the scratch VM.
- Run each missed vzscript step through the guest agent.
- Shut down cleanly, diff the child disk against the parent, save the layer
  manifest and cache entry, and leave final image state that `cove push` can
  consume.
- Remove the non-dry-run gate only in this slice.

**Likely files**:

- `build.go`
- `build_execute.go`
- `build_layer.go`
- `fork.go`
- `vzscript_apply.go`
- `agent_control.go`
- `push.go` or the image export path used by `cove push`

**Acceptance gates**:

- Cache misses execute in a VM.
- A second run with the same inputs hits cache and skips guest execution.
- Metadata survives process restart.
- Failed guest execution records a useful error and leaves scratch state only
  when `--keep-intermediate` is set.
- `go test ./...` plus a script-based CLI integration test for one tiny local
  recipe.

**Docs**:

- Update `docs/reference/cli.md` and `dump_docs.go` to describe supported
  non-dry-run behavior.
- Update `docs/reference/release-checklist.md` with build execution checks.
- Move `cove build --dry-run` from "only implemented mode" to "planning mode."

## Slice 4: tmpfs build secrets

**Landed**: yes; `# secret:` directives validate names early, mount through a
guest tmpfs or macOS RAM disk, fail closed on Linux when swap cannot be
disabled, and unmount before compaction and the layer diff. External secret
stores remain deferred to v0.4.

**Branch**: `feat/v03-build-secrets-tmpfs`

**Scope**:

- Implement `# secret:` for host environment variables.
- Mount guest tmpfs or macOS RAM disk before script execution.
- Disable or verify guest swap before secrets are materialized.
- Unmount and zero in-memory host buffers on completion.
- Keep `# cache-env:` explicitly non-secret.

**Likely files**:

- `build_cache.go`
- `vzscript.go`
- `cmd/vz-agent/server.go`
- `cmd/vz-agent/info_darwin.go`
- `cmd/vz-agent/info_linux.go`
- `proto/agent.proto`
- `internal/agent/version.go`

**Acceptance gates**:

- Missing secret environment variables fail before guest execution.
- Linux no-swap verification fails closed.
- A test fixture proves secret values are not written to cache-key JSON, layer
  manifests, or persisted build metadata.
- If `proto/agent.proto` changes, `AgentProtocolVersion` is bumped in the same
  change.

**Docs**:

- Add `# secret:` examples to the build CLI docs.
- Keep the warning that `# cache-env:` is not for secrets.
- Note that external secret-store URIs are v0.4, not v0.3.

## Slice 5: build compaction integration

**Landed**: yes; `fast`, `targeted`, and `thorough` compaction run between
guest execution and the layer diff, the compact mode is part of the cache
key, and `targeted` is the documented default.

**Branch**: `feat/v03-build-compaction`

**Scope**:

- Run the existing agent-aware `cove compact` behavior between guest execution
  and block diff.
- Honor `--compact` and per-script `# compact:` consistently.
- Measure enough local data to choose a default for v0.3.

**Likely files**:

- `compact.go`
- `build.go`
- `build_execute.go`
- `build_cache.go`
- `bench/soft-reset` only if reusable measurement helpers are needed

**Acceptance gates**:

- `fast`, `targeted`, and `thorough` modes are tested in build context.
- The compact mode is part of the cache key.
- Build diffs omit zeroed blocks after compaction.
- The chosen default is backed by a checked-in result note, not intuition.

**Docs**:

- Document build compaction modes in `docs/reference/cli.md`.
- Update `docs/designs/003-cove-build-oci-caching.md` if default selection
  changes.
- Add release-checklist coverage for secret plus compaction combinations.

## Slice 6: benchmark and adapter hardening

**Landed**: yes; fork-only and boot-to-agent fork benchmark results are
checked in under `bench/fork-time/`, the OpenAI Agents SDK adapter has
documented live-smoke and package-check instructions, and adapter examples
remain fork-first. `cove-sandbox` is not yet published as a package.

**Branch**: `feat/v03-benchmark-adapter-hardening`

**Scope**:

- Publish boot-to-agent fork benchmark results on named hardware.
- Add live-smoke instructions for `adapters/openai-agents-python`.
- Keep adapter examples fork-first for privacy-sensitive evals.
- Do packaging checks for `cove-sandbox`, but do not publish a package as part
  of this slice unless the release owner explicitly approves it.

**Likely files**:

- `cmd/fork-bench/main.go`
- `bench/fork-time/README.md`
- `bench/fork-time/results-*.md`
- `adapters/openai-agents-python/README.md`
- `adapters/openai-agents-python/tests`
- `docs/examples/openai-agents.md`

**Acceptance gates**:

- Benchmark output includes fork-only and boot-to-agent timings, host hardware,
  guest OS, cove commit, and failures if any.
- Slower-than-target runs are published instead of hidden.
- Python adapter tests pass locally.
- At least one documented live-smoke path uses `CoveSandbox.from_fork`.

**Docs**:

- Replace placeholder timing language with measured numbers only.
- Keep adapter docs explicit that VM state remains local.
- Do not add public registry or signed image instructions.

## Defer list (RC boundary)

The following are explicitly deferred for this RC. Public docs must keep this
list visible and consistent:

- Registry-base `cove build` execution. Non-dry-run builds require a local VM
  directory base; registry refs stay planning-only.
- Registry cache import/export (`--cache-from`, `--cache-to`). The flags are
  reserved and fail before planning if used.
- Public curated `cove` image registry and signed agentkit image channels until
  trademark counsel clears the name or a rename lands.
- External secret stores such as 1Password, Vault, SOPS, and age. v0.3 secrets
  are host environment variables mounted through tmpfs only.
- BuildKit-style parallel step execution. v0.3 build execution is sequential.
- Packer plugin shim work unless `cove build` execution is already stable and a
  maintainer explicitly reorders the roadmap.
- Product-name resolution before any public registry or signed channel ships.
- Soft-reset throughput claims for privacy-sensitive evals.
- Tart maintenance claims that have not been freshly verified.
