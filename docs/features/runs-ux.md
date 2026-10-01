---
title: Runs UX
description: Where cove stores per-run artifacts and how to inspect run history.
icon: puzzle-piece
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
cove runs list [--limit N] [--since DURATION] [--status ok|fail|all] [--json|--ndjson]
cove runs show <run-id-prefix> [--json|--summary-json]
cove runs export <run-id-prefix> --format json|gha-summary|tar
```

`runs list`
: Prints recent runs. `--limit` caps the number of rows, `--since` filters by a
duration such as `24h` or `7d`, `--status` filters successful or failed runs,
`--json` emits one machine-readable array, and `--ndjson` emits one object per
line.

`runs show`
: Prints one run's lifecycle, result, fork provenance summary, network policy
summary, resource summary, and artifacts.
`--json` emits the raw event array for compatibility; `--summary-json` emits
the structured show object for scripts, including raw metrics and an optional
`task_record` with ordered steps, primary error, independent capture/cleanup
errors and artifact availability. Inspection never reruns recorded tasks.

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
| `status` | Legacy metric status, or task outcome; successful task bundles use `ok`, incomplete bundles use `interrupted`. |
| `total_duration_ms` | Total measured run duration in milliseconds. |
| `exit_code` | Process or guest command exit code, when recorded. |
| `started_at` | Run start timestamp. |

## Run Id Prefixes

Commands that take `<run-id-prefix>` match local directories under `~/.vz/runs`.
The prefix must identify exactly one run. If no run matches, the command fails.
If more than one run matches, the command fails with an ambiguous-prefix error
and the operator should pass more characters from the run id.

## Fork Summary

When a run contains a `fork_created` event, `runs show` folds it into a short
fork summary with the source image or VM, optional source registry manifest
digest, child name/path, materialization mode, disk reuse, ephemeral/keep
cleanup intent, verification status, fork duration, and any limitation. GitHub
summary exports include the same derived fork section.

The raw `fork_created` event remains in `runs show --json` and
`cove runs export <id> --format json`. The derived summary is available in
human `runs show`, `runs show --summary-json`, and GitHub summary export.

## Resource Summary

When a run contains `resource_sample` events, `runs show` folds them into a
short resource summary:

- sample count
- minimum guest memory available, with percentage when total memory was sampled
- peak guest 1-minute load average
- peak guest process count
- hottest sampled guest process by CPU, with PID, RSS, command, and sample phase
- peak owning host process CPU and RSS
- heuristic hints for low guest memory, high guest load, hot guest process, or
  unusually high process count

The raw `resource_sample` events remain in `runs show --json` and
`cove runs export <id> --format json`. The derived summary is available in
human `runs show`, `runs show --summary-json`, and GitHub summary export.

## Network Summary

When a run contains a `network_policy` event, `runs show` folds it into a short
network summary with the selected policy, effective mode, enforcement shape,
allowlisted domains/CIDRs, audit-log flag, and any limitation. GitHub summary
exports include the same derived network section.

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

`cove runs inspect PREFIX` reads versioned task evidence as text. `--json`
emits the same inspection projection; `--html` writes a standalone local
HTML timeline to standard output. For example:

```sh
cove runs inspect PREFIX --html > "$HOME/tmp/run-inspection.html"
cove runs compare LEFT RIGHT --json
```

Inspection shows the primary failure, ordered steps, capture availability,
cleanup errors, cancellation and incomplete publication separately. Guest name
and retention are recorded declarations; current guest existence is unverified.
The timeline does not run commands, rerun tasks, embed artifact contents or serve
files. Artifact names and paths identify recorded evidence; the bounded reader
checks present files and digests before rendering. Source lines, action details
and capabilities appear only when producers record supported metadata; this
projection currently displays step identity, route and backend, and omits
arbitrary event payloads and secret input provenance.

Comparison requires equal verified image, plan, source and nonsecret input
digests. Unknown identities, differing inputs and secret input equality produce
an explicit incomparable result. Outcomes are displayed without ranking runs.
Raw artifacts and error strings still require producer redaction; escaping HTML
does not redact secrets that a producer wrote into an error.
