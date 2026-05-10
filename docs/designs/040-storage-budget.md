# Design 040: Storage Budget for `~/.vz/`

Status: Shipped. Phases 0-5 landed during R57; PiB-scale overflow fix at
`c7865e1` (R67); test coverage strengthened at `61c2ca5` (R66). SHA chain
in ROADMAP row "Design 040 storage budget (Phases 0-5)".
Verified 2026-05-10 (R365): `cove storage <census|budget|prune>` dispatch
in `storage.go`; `internal/storagecensus` package backs the walk; SHAs
`c7865e1` and `61c2ca5` reachable on main.
Author: Travis Cline
Date: 2026-05-07

## Problem

`~/.vz/` grows without an overall ceiling. The four largest residents share the
tree but do not share a budget:

- `~/.vz/vms/<name>/` — VM bundles (50–200 GB each, including disk images,
  vmstate, lineage, snapshot files).
- `~/.vz/images/<name>/<tag>/` — local image store (50–100 GB per image).
- `~/.vz/runs/<run-id>/` — per-run bundles (metrics, logs, artifacts; small
  individually but accumulate).
- `~/.vz/cache/`, `~/.vz/build-scratch/`, `~/.vz/store/` — content-addressed
  layer cache, scratch build directories, OCI store.

Each subsystem has its own ad-hoc cleanup story:

- `cove image gc` and `cove image prune` apply per-image TTL
  (`CACHE-TTL` files, default 7 days) but never look at total disk pressure.
- VM bundles are deleted only by explicit `cove vm delete`.
- Run bundles have no prune command; they grow forever.
- Snapshot lineage and orphan vmstate files have no central reaper.
- Build scratch is cleaned per build but a crashed `cove build` can leave
  `build-scratch/<id>/` behind.

The result is that "out of disk" arrives mid-fork. APFS sparse images delay
the cliff but do not prevent it, and they make `du`-based diagnosis
unreliable. There is also no single number an operator can ask for ("how
much is cove using?") and no dial they can turn ("cap cove at 500 GB").

Issue #246's per-VM disk quota (design 032) caps a single VM's directory but
does not cap cove as a whole.

## Goals

- One operator-visible disk budget for the full `~/.vz/` subtree.
- One command that reports current usage and the active budget.
- Eviction policy that is predictable (LRU within a category, never deletes
  protected items), preempts disk pressure, and is opt-in for destructive
  actions.
- No new on-disk format; reuse existing per-subsystem metadata (mtime, last
  use, lineage).

## Non-goals

- Cross-host quota enforcement. This design is single-host only.
- Live disk compaction or sparse-file reclamation beyond what
  `cove compact` already does.
- Per-user quotas in multi-user installations. cove still treats `~/.vz/` as
  a single-operator tree.
- Real-time eviction during a running operation. Eviction runs on operator
  command and at scheduled daemon ticks; mid-fork failures still surface as
  ENOSPC and the existing error paths.
- A second policy engine. The disk budget composes with `vmpolicy` and
  `vmquota`; it does not replace them.

## Storage census

The implementation walks `~/.vz/` once and produces a `StorageReport`:

```go
type StorageReport struct {
    Root       string                  // ~/.vz
    BudgetGB   int64                   // 0 means "no budget"
    UsedGB     int64
    Categories []CategoryUsage
    Generated  time.Time
}

type CategoryUsage struct {
    Name     string                    // "vms", "images", "runs", "cache",
                                       // "build-scratch", "store"
    UsedGB   int64
    Items    []ItemUsage               // newest first
    Evictable int64                    // GB the policy considers safe to evict
}

type ItemUsage struct {
    Path        string                 // absolute
    SizeGB      int64
    LastUsed    time.Time              // mtime fallback
    Pinned      bool                   // see "Protection"
    Reason      string                 // when Pinned, why
}
```

The walk is bounded: per-category top-N items are kept, but
`UsedGB`/`Evictable` totals are exact. Disk-image sizes use the APFS-reported
allocated size (sparse-aware), not the logical file size.

`LastUsed` is derived per category:

| Category | LastUsed source |
|---|---|
| `vms/<name>/` | `runtime.json` last `started_at`, falling back to bundle mtime |
| `images/<name>/<tag>/` | `metadata.json` `last_used_at` (already updated on `fork-from`) |
| `runs/<run-id>/` | `metrics.jsonl` `run_complete.timestamp`, fallback to dir mtime |
| `cache/` | mtime |
| `build-scratch/<id>/` | dir mtime |
| `store/` | layer file mtime |

## Budget model

Persist budget in `~/.vz/storage.json`:

```json
{
  "budget_gb": 500,
  "last_run": "2026-05-07T12:00:00Z"
}
```

`budget_gb` zero or absent means "no budget"; eviction commands still work
but only when called explicitly.

The budget is a soft watermark. When `UsedGB > BudgetGB`, the daemon and
explicit commands evict the highest-LRU items in the eligible categories
until usage is below the budget. There is no hard kernel-level enforcement;
cove cannot prevent third-party tools from using the disk.

## Eviction policy

Categories are evicted in this order, oldest-first within each:

1. `build-scratch/<id>/` — always safe; scratch is per-build.
2. `runs/<run-id>/` older than 30 days, unless the run is referenced by a
   `cove image build` cache entry.
3. `cache/` entries past their existing TTL.
4. `images/<name>/<tag>/` past their existing `CACHE-TTL`, never with
   `keep=true` set.
5. `store/` layers no longer referenced by any image manifest.

VM bundles (`vms/<name>/`) are never evicted automatically. They are the
operator's primary asset; their lifecycle is governed by `cove vm delete`
plus the existing lifecycle and quota policies.

Within each category, the LRU order uses `LastUsed`. Items at the same
timestamp tie-break by path.

## Protection

Items can be pinned out of eviction with a `keep` annotation:

| Category | How to pin |
|---|---|
| `images/<name>/<tag>/` | `cove image keep <name>:<tag>` writes `KEEP` file in the image dir |
| `runs/<run-id>/` | `cove runs keep <prefix>` writes `KEEP` file in the run dir |
| `cache/` | not pinnable; cache is content-addressed and rebuildable |
| `build-scratch/` | not pinnable |

Pinned items appear in `cove storage status` with `pinned=true` and a reason
("explicit keep" or "active fork-from parent"). The fork-from parent rule is
already enforced by image lineage in design 024; this design surfaces it.

## CLI

```text
cove storage status              # report usage and budget, table form
cove storage status -json        # report usage and budget, JSON form
cove storage budget set <gb>     # update ~/.vz/storage.json
cove storage budget clear        # remove the budget
cove storage prune               # interactive: print plan, prompt, evict
cove storage prune -dry-run      # print plan, no changes
cove storage prune -yes          # non-interactive eviction
cove storage prune -only=runs    # restrict to one category
```

Existing per-category commands (`cove image gc`, `cove image prune`,
`cove vm delete`) continue to work and emit the same metrics events; the
new `cove storage prune` is a coordinator that calls them in order.

`cove image keep` and `cove runs keep` are new sub-commands; they write a
zero-byte `KEEP` file. Removal is `cove image keep -unset` /
`cove runs keep -unset`.

## Daemon integration

`coved` already runs scheduled `image gc` (design 033). Extend it:

- New section in `coved.toml`:

  ```toml
  [storage]
  budget_gb = 500
  prune_interval = "12h"
  ```

- On each tick, the daemon runs `storagePruneOnce` if the latest report
  shows `UsedGB > BudgetGB`. Eviction is logged through the existing
  metrics bus as `storage.prune.evict` events, one per item.
- The daemon never deletes pinned items, never deletes VM bundles, and
  never deletes a run while a `cove run` referencing it has an active
  `run.lock`.

## Metrics

Three new event types under the existing metrics schema:

| `event_type` | Source | Extra |
|---|---|---|
| `storage.prune.start` | `cove storage prune` and daemon tick | `budget_gb`, `used_gb_before` |
| `storage.prune.evict` | each evicted item | `category`, `path`, `size_gb`, `last_used` |
| `storage.prune.end` | end of a prune pass | `evicted_count`, `used_gb_after`, `still_over_budget` |

Schema additions land in `docs/observability/runs-schema.md` alongside the
existing event-type table.

## Phased delivery

1. **Phase 0**: storage census. Walk `~/.vz/` and produce `StorageReport`.
   `cove storage status` and `cove storage status -json`. No mutation.
   Tests: golden-file walks against a fixture tree.
2. **Phase 1**: budget persistence. `cove storage budget set/clear`,
   `~/.vz/storage.json`, surfaced in `status` output.
3. **Phase 2**: prune coordinator. `cove storage prune` driving existing
   per-category cleanups in the documented order. Dry-run first; explicit
   `-yes` for destructive run.
4. **Phase 3**: pinning — shipped (R57). `cove pin <object>`,
   `cove unpin <object>`, and `cove pins list` persist operator-supplied
   keep markers in `~/.vz/pins.json` (atomic tempfile + rename). Object
   refs are typed: `vm:<name>`, `image:<ref>`, `run:<id>`, `cache:<sha>`.
   Census output surfaces `pinned: true` in JSON and a `Pinned:` block
   plus `(★N)` row markers in `-human`. The eviction loop reaches the
   pin set via `internal/storagepins.File.IsPinned(category, id)`.
   Implementation simplified the original sketch — single typed file
   rather than per-category `KEEP` markers — to keep one source of truth.
5. **Phase 4**: daemon integration — shipped (R57).
   `internal/coved/storage.go` adds `StoragePollScheduler`, wired into
   `cmd/coved/main.go` behind `-storage-poll-interval` (default 1h,
   override via `COVE_STORAGE_POLL_INTERVAL`). Three new event types
   (`storage_budget_warn`, `storage_budget_hard`, `storage_prune_run`)
   catalogued in `docs/observability/runs-schema.md`. The hard tripwire
   now invokes the Phase 5 prune coordinator with pin-aware eviction; it
   never touches VM bundles or pinned items.
6. **Phase 5**: prune coordinator — shipped (R57). `cove storage prune`
   drives the documented eviction order with `-dry-run` / `-yes` /
   `-only=<category>`. The daemon hard tripwire calls the same
   coordinator. PiB-scale budgets stay overflow-safe after `c7865e1`
   (R67); regression coverage for budget overwrite, invalid on-disk
   state, and threshold edges landed at `61c2ca5` (R66).

Each phase shipped behind a single CLI surface and was reviewable on its
own.

## Open questions

- Should `runs/` retention default to 30 days or to the same TTL as the
  associated image? Initial slice uses a flat 30-day floor and revisits if
  CI users complain.
- How to count APFS clonefile-shared blocks. Two images sharing 30 GB of
  cloned blocks should not show up as 60 GB used. Phase 0 reports the
  per-file allocated size; a follow-up should switch to a one-pass
  reference-counted walk if the discrepancy proves user-visible.
- Should the budget cover only `~/.vz/` or include `~/.cache/cove/` if it
  ever lands? This design keeps the boundary at `~/.vz/`. Anything else is
  out of scope until a second cache location actually exists.

## Cross-references

- [`docs/designs/024-cove-runner-images.md`](024-cove-runner-images.md)
  for the image store and its existing `CACHE-TTL` per-image policy that
  the storage budget composes with.
- [`docs/designs/032-vm-quotas.md`](032-vm-quotas.md) for the per-VM disk
  cap that bounds the largest single contributor to `~/.vz/` usage.
- [`docs/designs/033-cove-daemon.md`](033-cove-daemon.md) for the daemon
  loop that hosts scheduled prune ticks alongside the existing image GC.
- [`docs/observability/runs-schema.md`](../observability/runs-schema.md)
  for the metrics schema the new `storage.prune.*` events extend.
