---
title: Runs UX
---
# Runs UX

cove stores per-run artifacts under:

```text
~/.vz/runs/<run-id>/
```

The `cove runs` commands inspect and export those local run directories. Metrics
are read from:

```text
~/.vz/runs/<run-id>/metrics.jsonl
```

Each line in `metrics.jsonl` is one JSON event. The commands use the event
stream to summarize run status, duration, VM identity, image identity, and exit
code without requiring an external telemetry service.

## Commands

```bash
cove runs list [--limit N] [--since DURATION] [--status ok|fail|all] [--json]
cove runs show <run-id-prefix> [--json]
cove runs export <run-id-prefix> --format json|gha-summary|tar
```

`runs list`
: Prints recent runs. `--limit` caps the number of rows, `--since` filters by a
duration such as `24h` or `7d`, `--status` filters successful or failed runs,
and `--json` emits machine-readable output.

`runs show`
: Prints one run's summary and available metrics. `--json` emits the same data
as structured JSON for scripts.

`runs export`
: Writes one run in a requested format. `json` emits structured run data,
`gha-summary` emits Markdown suitable for `GITHUB_STEP_SUMMARY`, and `tar`
writes a gzip tar archive to stdout.

## List Fields

Human-readable `runs list` output includes:

| Field | Description |
|---|---|
| `run-id` | Short run-id prefix. |
| `image_ref` | Source image or VM ref, when known. |
| `vm_name` | VM name, when known. |
| `status` | `ok` or `fail`. |
| `total_duration_ms` | Total measured run duration in milliseconds. |
| `exit_code` | Process or guest command exit code, when recorded. |
| `started_at` | Run start timestamp. |

## Run Id Prefixes

Commands that take `<run-id-prefix>` match local directories under `~/.vz/runs`.
The prefix must identify exactly one run. If no run matches, the command fails.
If more than one run matches, the command fails with an ambiguous-prefix error
and the operator should pass more characters from the run id.

## Export Formats

`json`
: Structured run summary and metrics for automation.

`gha-summary`
: Markdown formatted for GitHub Actions step summaries:

```bash
cove runs export 20260505 --format gha-summary >> "$GITHUB_STEP_SUMMARY"
```

`tar`
: Gzip tar archive of the run artifact directory, written to stdout:

```bash
cove runs export 20260505 --format tar > cove-run.tar.gz
```

The tar format is intended for CI artifact upload and post-mortem transfer.
Because it writes binary gzip data to stdout, redirect it to a file or pipe it
to an artifact uploader.

## Benchmark Runs

`cove bench competitive` writes a normal run artifact containing benchmark
metrics:

```bash
cove bench competitive \
  --out docs/benchmarks/results-2026-05-cove.json \
  --markdown docs/benchmarks/competitive-2026-05.md
cove runs show bench-20260506
cove runs export bench-20260506 --format json
```

The checked-in JSON report is the table source. The run directory remains the
inspectable evidence bundle for `cove runs list/show/export`.
