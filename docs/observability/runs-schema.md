# Runs schema

`cove runs list/show/export` reads `metrics.jsonl` files written by VM runs.
This document describes the schema. The authoritative type is
`internal/metrics.Event`.

## Per-event shape

Each line in `metrics.jsonl` is one JSON object:

```json
{
  "timestamp": "2026-05-07T12:34:56.789Z",
  "event_type": "run_complete",
  "vm_name": "demo",
  "image_ref": "macos-15:latest",
  "duration_ms": 12345,
  "status": "ok",
  "extra": {
    "run_id": "20260507-123456-abcd",
    "exit_code": 0,
    "command": "up"
  }
}
```

| Field | Type | Notes |
|---|---|---|
| `timestamp` | RFC3339Nano string | UTC; auto-set by the JSONL sink if empty |
| `event_type` | string | one of the values in [Event types](#event-types) |
| `vm_name` | string | optional; the VM the event refers to |
| `image_ref` | string | optional; image (`name:tag`) or `fork-from` source for the run |
| `duration_ms` | int64 | optional; phase or wall-clock duration in ms |
| `status` | string | `ok`, an error string, `tripped`, `exceeded`, or empty |
| `extra` | map[string]any | optional; phase-specific fields, see below |

`extra` always includes `run_id` when emitted from a run bundle or standalone
metrics run. Other keys are event-specific.

## Event types

These are the event types emitted on `origin/main` as of the v0.5 release.
The catalog is gathered from grep-able `EventType:` literals plus the
parameterized helpers `emitMetricEvent` (package main) and
`lifecyclePolicyEventType` (`runtime_lifecycle.go`).

### Run lifecycle

| `event_type` | Emitted by | Meaning |
|---|---|---|
| `vm_create` | `up.go`, `image_cli.go` | New VM directory created |
| `vm_start` | `macos.go` | VM started |
| `agent_ready` | `run_metrics.go` | Guest agent came up; emitted at most once per run |
| `vm_stop` | (sink test only) | VM stopped |
| `fork_created` | `image_fork.go`, `runtime_lifecycle.go` | Fork-from VM materialized |
| `run_complete` | `up.go`, `build.go`, `runtime_lifecycle.go`, `run_bundle.go`, `image_cli.go` | Terminal event; carries the final `status` and `extra.exit_code` |

### Build / benchmark

| `event_type` | Emitted by | Meaning |
|---|---|---|
| `build_step` | `build_execute.go` | One step of `cove image build` |
| `benchmark_result` | `internal/bench/competitive.go` | Per-run benchmark sample |

### Lifecycle policy / budget

| `event_type` | Emitted by | Meaning |
|---|---|---|
| `lifecycle.budget.exceeded` | `runtime_lifecycle.go` | Run rejected because the VM's `vmpolicy` run budget is exhausted |
| `lifecycle.idle.tripped` | `runtime_lifecycle.go` (via `lifecyclePolicyEventType`) | Idle deadline reached |
| `lifecycle.maxage.tripped` | `runtime_lifecycle.go` | Max-age policy stopped the VM |
| `lifecycle.policy.stop` | `internal/coved/lifecycle.go` | Cove daemon enforced a policy stop |
| `vm_policy_stop` | `runtime_lifecycle.go` | Fallback when `lifecyclePolicyEventType` cannot classify the reason |

### Image / cache GC

| `event_type` | Emitted by | Meaning |
|---|---|---|
| `image_gc_evict` | `image_gc.go`, `image_prune.go` | A staged image was reclaimed |
| `image_gc_keep` | `image_gc.go` | An image was retained after a GC pass |
| `image.gc.run` | `internal/coved/image_gc.go` | Daemon-side GC sweep summary |

### Agent sandbox

| `event_type` | Emitted by | Meaning |
|---|---|---|
| `agent_sandbox_start` | `agent_sandbox.go` | Agent sandbox session began |
| `agent_sandbox_complete` | `agent_sandbox.go` | Agent sandbox session terminated |

### Storage budget (design 040 Phase 5)

| `event_type` | Emitted by | Meaning |
|---|---|---|
| `storage_budget_warn` | `internal/coved/storage.go` | Census crossed `warn_pct` of the configured budget. `extra` carries `used_bytes`, `state="warn"`, `target_bytes`, `warn_pct`, `hard_pct`. |
| `storage_budget_hard` | `internal/coved/storage.go` | Census crossed `hard_pct`. Same `extra` fields as warn, with `state="hard"`. |
| `storage_prune_run` | `internal/coved/storage.go` (Phase 5 stub) and the prune coordinator (Phase 3, not yet shipped) | One prune pass. `extra` carries `category`, `bytes_freed`, `dry_run`, `used_bytes`. While Phase 3 is unshipped the daemon emits this with `dry_run=true` and `reason="phase-3-not-shipped"` whenever it sees `state=hard`. |

## Lifecycle subset for `cove runs show`

`cove runs show` highlights a subset of events as "Lifecycle". The set is
defined in `internal/runs/show.go` (`lifecycleEvents` map):

`fork_created`, `vm_create`, `vm_start`, `agent_ready`, `build_step`,
`benchmark_result`, `lifecycle.budget.exceeded`, `lifecycle.idle.tripped`,
`lifecycle.maxage.tripped`, `run_complete`.

Events not in this list (image GC, agent sandbox, daemon-side
`lifecycle.policy.stop`, `vm_policy_stop` fallback) still appear in `runs
show -v` and `runs export` JSON, but are not rendered in the default
lifecycle phase table.

## Export formats

`cove runs export` emits three formats:

| `--format` | Source | Output |
|---|---|---|
| `json` (default) | `internal/runs/export.go:ExportJSON` | Indented JSON array of all `Event`s for the run |
| `gha` | `internal/runs/export.go:ExportGHASummary` | GitHub Actions step-summary markdown table covering only Lifecycle events |
| `tar.gz` | `internal/runs/export.go:ExportTarGz` | gzip tarball of the entire run directory (`metrics.jsonl` plus any artifacts) |

## OTLP export

When `OTEL_EXPORTER_OTLP_ENDPOINT` is set, `internal/metrics.NewSink`
returns a `MultiSink` that fans events to JSONL and an OTLP HTTP exporter
(`internal/metrics.NewOTLPSink`). The on-disk JSONL schema is unchanged.

## Stability

Adding new `event_type` values is backwards-compatible: `cove runs show`
ignores unknown types in the lifecycle subset and `runs export json` passes
them through as opaque records. Removing or renaming a type is a breaking
change to log readers; do not do it without a release note.
