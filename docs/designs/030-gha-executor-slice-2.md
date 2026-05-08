# Design 030: GHA Executor Slice 2 Cross-run Cache Reuse

**Status:** Implemented (MVP)  
**Author:** Travis Cline  
**Date:** 2026-05-05

**Implementation:** T77 shipped this spec on `origin/main` at `9e6253a` (action: add local image cache restore), `f06d554` (image: expire local action caches), and `c0a1433` (docs: describe action image cache). The header previously cited `3199d58`, `1444d5f`, and `d78a853`; those are off-main rebase duplicates of the same three commits and are not on `origin/main`. ROADMAP row "GitHub Actions executor Slice 2 cache reuse" tracks the canonical SHAs.

Follow-on work that touches the same surface but lands under other designs: `4e0a0aa` (metrics: emit image gc and run cache eviction events) and `ab7f159` / `c9940f6` / `c9df361` (R63 cove-action `secrets:` parser, scoped under design 025 / cirrus-secrets-fix-2026-05-08, not design 030).

Spec-vs-code drift surfaced during this refresh (left for a separate follow-up — out of scope for status):

- `action.yml` exposes `cache-key`, `cache-paths`, (R76, `ece1169`) `cache-mode`, and (R77) `cache-scope`. The MVP § here also lists `cache-ttl` and `cache-save-on`; those two remain unwired. `cache-paths` is now permitted as informational-only metadata (resolved below).
- The `cache-primary-key` output named in the API Surface § is not emitted; the other three outputs (`cache-hit`, `cache-image`, `cache-saved`) match.
- Default save policy and TTL behavior reflected in code should be reconciled against the MVP § wording.

## Problem

The v0.4 GitHub Actions executor Slice 1 starts every job from a fresh local cove image fork, runs one command or script through the guest agent, emits logs and metrics, then tears the fork down. That is the right isolation boundary, but it also means every run starts from the same cold parent image. Language package caches, build artifacts, checked-out dependencies, and installed CI tool state disappear after each run.

Slice 2 adds cross-run cache reuse without weakening the Slice 1 security model. The cache must stay local to the trusted self-hosted runner host, work when no registry is configured, preserve current behavior when no cache key is supplied, and avoid turning the executor into a shared mutable VM.

## Decision Summary

- Add an optional `cache-key` surface to the private cove GitHub Action. Empty key keeps the current Slice 1 behavior exactly: fork from `image`, run, stop, delete.
- Model the cache as a local cove image snapshot, not as a persistent VirtioFS volume. A cache hit forks from the cached image; a miss forks from the requested base image and may save the stopped fork as a new cache image after a successful job.
- Make the cache key name a whole-VM cache state, not a list of host paths. Users should include lockfile hashes in the key, as they do with `actions/cache`; an optional `cache-paths` input is informational only (kept for `actions/cache` shape parity) and does not drive partial directory extraction.
- Use first-writer-wins save semantics. If two jobs miss the same key, both may run from the base image, but only one saves the cache image; the other treats the duplicate image as a benign cache-save race.
- Keep caches local-only under `~/.vz/images/`. The action does not push caches to OCI registries, and no public registry surface is introduced in this slice.

## Existing Ground

Design 024 already defines the local image store and fork-from-image model: images live under `~/.vz/images/<name>/<tag>/`, `cove run -fork-from <image-ref>` materializes a fresh VM bundle, and `-ephemeral` destroys the child at job end. It also records the runner security rule that every CI job gets a fresh fork and soft reset is never the runner isolation path.

The current private action in `.github/actions/cove-action/action.yml` and `cmd/cove-action/` accepts `image`, `command`/`args`/`script`, `env`, `timeout`, `cove-bin`, `vm-name`, and `keep`. It launches:

```text
cove run -fork-from <image> -fork-name <vm> -ephemeral -headless
```

then waits for agent readiness, runs the guest command, records metrics, and stops the fork. Slice 2 should be an extension of this flow, not a second executor.

The image store already has useful guardrails for this design. Materialized children record `ParentImage` in `config.json`, and image removal/GC checks for forks before deleting an image. Concurrent reads from one image are compatible with the model because each run gets a unique child bundle. Cache writes need separate first-writer handling because `cove image build` refuses an existing tag.

## Proposal

### API Surface

Add optional inputs to `.github/actions/cove-action/action.yml`:

| Input | Required | Default | Description |
|---|---:|---|---|
| `cache-key` | no | empty | Whole-VM cache key. Empty disables Slice 2 and preserves Slice 1 behavior. |
| `cache-mode` | no | `restore-save` | One of `restore-save`, `restore-only`, `save-only`, or `off`. |
| `cache-scope` | no | repository slug | Local namespace used to separate caches from different repos on the same runner host. |
| `cache-ttl` | no | `168h` | Maximum age for a restore candidate. Expired caches are ignored; deletion remains a separate GC action. |
| `cache-save-on` | no | `success` | Save only on guest exit 0 in MVP. `always` can be a later polish option if needed. |

Add outputs:

| Output | Description |
|---|---|
| `cache-hit` | `true` when the exact cache key restored a local image. |
| `cache-image` | Local cove image ref used for the run when cache is enabled. |
| `cache-saved` | `true` when this run created the cache image. |
| `cache-primary-key` | Normalized key string used to compute the local image ref. |

Mirror these as `cmd/cove-action` flags and environment variables:

```text
-cache-key
-cache-mode
-cache-scope
-cache-ttl
-cache-save-on
COVE_ACTION_CACHE_KEY
COVE_ACTION_CACHE_MODE
COVE_ACTION_CACHE_SCOPE
COVE_ACTION_CACHE_TTL
COVE_ACTION_CACHE_SAVE_ON
```

Example workflow:

```yaml
- uses: ./.github/actions/cove-action
  id: cove
  with:
    image: agentkit/linux-base:latest
    cache-key: linux-go-${{ hashFiles('go.sum') }}
    cache-scope: ${{ github.repository }}
    script: |
      go test ./...
```

A `cache-paths` input is permitted as informational metadata only. The saved object is a full stopped VM image, so the list does not drive partial restore or extraction; it documents which guest paths the whole-VM cache is expected to benefit, mirroring the `actions/cache` shape so migrating workflows port cleanly. Path-aware invalidation still belongs in `cache-key` (e.g. `hashFiles('go.sum')`). Cove must not start interpreting `cache-paths` as a selection filter without a follow-up design.

### Cache Key Model

A cache key maps to a deterministic local image ref:

```text
cove-action-cache/<scope>/<sha256(cache-key)>:latest
```

The action should store the original scope, key, base image, cove version, and creation time in cache metadata, either by extending the local image manifest or by writing a small sidecar JSON in the image directory. The ref should use the hash for filesystem-safe names; the metadata keeps the operator-facing key inspectable.

Restore flow:

1. If `cache-key` is empty or `cache-mode=off`, use the configured `image` directly.
2. Compute the cache ref from `cache-scope` and `cache-key`.
3. If `cache-mode` allows restore and the ref exists and is not older than `cache-ttl`, run from the cache ref and set `cache-hit=true`.
4. Otherwise run from the configured base `image` and set `cache-hit=false`.

Save flow:

1. Save only when `cache-key` is non-empty, `cache-mode` allows save, the restore was a miss, and the guest command succeeded.
2. Stop the fork, wait for the `cove run` process to exit, then build an image from the stopped fork:

   ```text
   cove image build -from <vm-name> -tag cove-action-cache/<scope>/<key-hash>:latest
   ```

3. If build succeeds, set `cache-saved=true`.
4. If build fails because the tag already exists, treat it as a nonfatal concurrent writer win and set `cache-saved=false` with a clear log line.
5. If build fails for any other reason, fail the action unless a later `cache-save-failures=warn` polish option is added.

Because cache saving needs the stopped child bundle, cache-enabled runs must keep the child long enough to snapshot it. The current action cleanup should be split into phases:

1. start fork with `-keep` internally when cache save might run
2. run readiness and guest command
3. stop fork and wait for the run process
4. save cache image if eligible
5. delete the child VM unless the user set `keep: true`

This preserves the user's visible `keep` behavior while giving the action a temporary stopped VM to snapshot.

### Image vs Volume Reasoning

Use image snapshots for Slice 2.

A full image cache captures the real CI speedup surface: package manager state, compiler caches, installed tools, generated source trees, and system-level changes. It also composes with the existing image store, fork-from-image tests, `ParentImage` tracking, and local image GC. Most importantly, every future job still starts from a fresh fork of an immutable parent image, so the executor does not become a shared mutable runner.

A persistent VirtioFS volume is the wrong MVP primitive. It would only cache selected directories, require new mount policy, expose host filesystem permissions and cleanup decisions, and risk carrying secrets or workspace residue across jobs outside the VM image isolation story. A volume cache could be useful later for narrow dependency directories, but it needs its own security design and should not block Slice 2.

### Cache Invalidation

Primary invalidation is key change. Workflow authors should include the inputs that define cache validity, for example lockfile hashes, compiler versions, or image version tags:

```yaml
cache-key: linux-go-${{ runner.arch }}-${{ hashFiles('go.sum') }}
```

TTL is a restore filter, not a silent delete. If a cache is older than `cache-ttl`, the action ignores it and runs from the base image. Deletion stays explicit through existing image cleanup tooling:

```text
cove image gc -older-than 168h
cove image rm cove-action-cache/<scope>/<hash>:latest
```

The action should not run GC automatically in the MVP. Automatic deletion inside CI makes concurrency harder and creates surprising latency. Operators can schedule GC outside active job windows.

Explicit opt-out is `cache-mode=off` or empty `cache-key`. `restore-only` supports read-only cache trials, and `save-only` supports prewarming a key without restoring an older image.

### Concurrency

N parallel CI runs may read the same cache image. That is compatible with design 024's model because fork-from-image materializes unique child VM bundles and treats the image directory as a read-only parent. Existing `ParentImage` tracking also prevents normal image deletion once children have been materialized.

The two concurrency edges are cache save and GC:

- Save: two cache misses for the same key can race. Use first-writer-wins. The winner creates the image tag; the loser sees the duplicate tag error and continues successfully without overwriting the cache.
- GC/delete: existing checks protect images that already have child configs pointing at them, but there is still a small race if an operator deletes or GCs the image between restore lookup and child materialization. MVP guidance should be: do not run cache GC concurrently with CI jobs. A follow-up can add a core image-store lock under `~/.vz/images/.locks/` around materialize/delete if this becomes a real operational problem.

The action must generate unique VM names for every run attempt. The current generated name path already does this; cache mode should not change it.

### Backwards Compatibility

No cache key means no cache behavior. The command sequence, outputs, guest exit-code handling, metrics path discovery, and cleanup should remain compatible with Slice 1. Existing tests for no-cache runs should keep their expected `cove run -fork-from <image> -fork-name <vm> -ephemeral -headless` shape.

Cache outputs may be empty or `false` when disabled. Existing workflows that do not read them are unaffected.

### Privacy Gate

Caches are local-only. The cache ref is a local cove image ref under `~/.vz/images/`; the action must not call `cove image push`, must not accept a registry cache target in Slice 2, and must not upload cache images as workflow artifacts.

This keeps the executor aligned with the current private-repo posture: registry/public image distribution remains a separate operator decision, not an automatic CI behavior.

## File-level Change Estimate

- `.github/actions/cove-action/action.yml`: add cache inputs, outputs, and environment plumbing, about 35-50 LOC.
- `cmd/cove-action/main.go`: add config fields, parsing, key normalization, cache restore selection, phased cleanup/save flow, and outputs, about 180-260 LOC.
- `cmd/cove-action/main_test.go`: add no-cache compatibility, cache hit, cache miss save, duplicate-save race, TTL-expired, and nonzero-exit-no-save cases, about 180-260 LOC.
- `docs/features/gha-executor.md`: document cache inputs, local-only privacy rule, examples, and concurrency notes, about 70-110 LOC.
- Optional core follow-up: image-store locking around materialize/delete, about 80-140 LOC plus tests. Not required for MVP if GC remains operator-scheduled.

No change is required to `cove image build` for the MVP if duplicate-tag errors are detectable and treated as first-writer-wins by the action wrapper. If the error is not currently distinguishable enough, add a small helper or error string in the image package rather than adding a broad overwrite flag.

## MVP

MVP should include:

- exact `cache-key` restore only
- local image ref derived from `cache-scope` and key hash
- `cache-mode` with `restore-save`, `restore-only`, `save-only`, `off`
- save on guest success only
- first-writer-wins duplicate save handling
- no automatic GC
- no registry push/pull
- `cache-paths` accepted but informational only (no partial restore)
- no restore-key prefix matching

This is enough to prove the speedup story with two sequential runs on the same self-hosted runner: first run misses and saves; second run hits and starts from the cached image.

## Polish-up

Follow-ups after MVP:

- `cache-restore-keys` multiline prefix matching, backed by cache metadata that records original keys.
- `cache-save-on=always` or `cache-save-failures=warn`, if real workflows need failed-run diagnostics or partial cache saves.
- Core image-store lock around materialize/delete/build promotion.
- A `cove image tag` or `cove image promote` command for immutable candidate images plus atomic stable-ref promotion.
- Optional volume-backed directory caches, only after a separate security and mount-policy design.
- Metrics fields for `cache_hit`, `cache_ref`, `cache_save_ms`, and `cache_restore_source` in the action metrics stream.

## Test Plan

Unit tests with the existing fake cove runner should cover:

- no `cache-key` preserves the current Slice 1 command sequence and outputs
- cache hit runs from `cove-action-cache/<scope>/<hash>:latest` and sets `cache-hit=true`
- cache miss runs from the configured base image, then stops, builds the cache image, deletes the child, and sets `cache-saved=true`
- nonzero guest exit propagates the guest exit code and does not save by default
- duplicate cache image during save is nonfatal and reports `cache-saved=false`
- expired cache image is ignored and treated as a miss
- `cache-mode=restore-only` never saves
- `cache-mode=save-only` does not restore but saves after success
- generated VM names remain unique across cached runs
- `action.yml` exposes every new input and output expected by `cmd/cove-action`

Live verification:

1. Prepare a local base image on a trusted self-hosted macOS runner.
2. Run a workflow with `cache-key` and a script that creates an obvious guest-side cache marker.
3. Confirm first run reports `cache-hit=false`, `cache-saved=true`, and creates a local image under `~/.vz/images/cove-action-cache/...`.
4. Run the same workflow again and confirm `cache-hit=true` and the marker is present.
5. Run two jobs with the same cold key in parallel and confirm one saves while the other handles duplicate save cleanly.
6. Confirm no registry commands are issued and no cache image is uploaded as a GitHub artifact.

## Risks

- Full-image caches can grow quickly. Operators need disk monitoring and scheduled `cove image gc` until richer quota management exists.
- A bad cache key can preserve stale dependencies or broken system state. This mirrors `actions/cache`: key quality is the workflow author's responsibility.
- Saving a full VM image after every miss adds latency to the miss path. The design should emit cache save duration in metrics so the tradeoff is visible.
- The GC/delete race is acceptable for MVP only if automatic GC stays out of the action. If operators need concurrent GC, image-store locking becomes required.
- Cache images may contain secrets written by the guest. The docs must say plainly that workflows should not write credentials to disk unless they are comfortable caching them on the trusted runner host.

## Open Questions

1. Should the default `cache-scope` come from `GITHUB_REPOSITORY`, or should the action require it when `cache-key` is set to avoid accidental cross-repo sharing on a self-hosted runner?
2. Is `168h` the right default TTL, or should the MVP avoid a default TTL and only expire when the user opts in?
3. Does `cove image build` currently return a stable enough duplicate-tag error for the wrapper to classify first-writer-wins, or should the image package expose a typed error?
4. Should cache metadata extend `manifest.json`, or should cache images carry a sidecar `cache.json` to avoid perturbing the core image manifest contract?
5. Should cache save failure fail the action by default? The strict answer surfaces real image-store problems, but a warning mode may be friendlier for early adoption.

## Spec drift / open work

Verified against `.github/actions/cove-action/action.yml` at `4150a02` (R71 finding R71-DESIGN-030-STATUS `0b774c4`). The header note at lines 11-15 flagged drift in passing; the items below are the resolved list.

1. Missing inputs: `cache-mode`, `cache-scope`, `cache-ttl`, `cache-save-on` from API Surface § (lines 54-57) are not wired in `action.yml`. Status: `cache-mode` resolved at `ece1169` (R76); `cache-scope` resolved at R77 — adds an optional namespace prefix joined as `<scope>:<key>` before normalization, default empty (per-repo behavior preserved). Open Question §1 (default source) is intentionally settled minimally: caller supplies `${{ github.repository }}` or any other namespace explicitly. The remaining two (`cache-ttl`, `cache-save-on`) stay deferred to v0.7.
2. Extra input: `cache-paths` is wired and is now spec-permitted as informational-only metadata (R74). Status: resolved — API Surface § respec'd to keep the shipped surface; removing it would have broken `docs/migrations/from-cirrus.md`, `docs/migration/cirrus-to-cove.md`, `docs/landing/migration-walkthrough.md`, and `docs/features/gha-executor.md` examples.
3. Missing output: `cache-primary-key` from API Surface § (line 66) is not emitted. The other three cache outputs (`cache-hit`, `cache-image`, `cache-saved`) match. Status: deferred to v0.7 — small wrapper change, blocked on input decisions above.
4. Open Questions §§ 1-5 (lines 259-263) remain unanswered: `cache-scope` default, TTL default, duplicate-tag error shape, manifest-vs-sidecar, save-failure policy. Status: needs design discussion before the input surface lands.
