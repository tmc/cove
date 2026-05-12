---
title: Run Metrics
---
# Run Metrics

cove records structured metrics for forked runs. The default sink is a local
JSONL file in the run artifact directory:

```text
~/.vz/runs/<run-id>/metrics.jsonl
```

Each line is one JSON object. The file is append-only during the run and remains
with the other run artifacts after teardown. Operators can archive the run
directory, tail the file while a run is active, or convert the stream into their
own telemetry backend.

## Event Schema

All events use the same envelope:

| Field | Type | Description |
|---|---|---|
| `timestamp` | string | RFC3339 timestamp. |
| `event_type` | string | Event name. |
| `vm_name` | string | VM name, when known. |
| `image_ref` | string | Parent VM, image ref, or output image ref, when known. |
| `duration_ms` | number | Elapsed time for completed events. Omitted when not measured. |
| `status` | string | `start`, `ok`, or `error`. |
| `extra` | object | Event-specific values, including `run_id`. |

Event-specific fields go under `extra` so readers can keep using the common
envelope as new measurements are added.

## Events

`vm_create`
: Emitted when cove creates or materializes VM state for a run. `extra` may
include `command`, `guest_os`, and `source_vm`.

`fork_created`
: Emitted when a child VM has been forked from a parent VM or local image.
`extra` may include `child_name` and `child_path`.

`vm_start`
: Emitted around boot. `duration_ms` is the host-side time spent starting the
VM process and waiting for Virtualization.framework to accept the start.

`agent_ready`
: Emitted when the guest agent is reachable. `duration_ms` is measured from run
start.

`capture_latency`
: Emitted for each screenshot capture. `duration_ms` is the wall-clock capture
time before diff/OCR work. `extra` includes `backend`, `requested_backend`,
`fallback`, optional `fallback_cause`, optional `width` and `height`, and a
truncated `error` string when `status` is `error`.

`resource_sample`
: Best-effort guest resource snapshot emitted when the guest agent is reachable.
`extra.phase` is `start` or `end`; memory fields use byte counts from guest
agent info when available.

`build_step`
: Emitted for each `cove build` step. `extra` includes `step`, `cache_hit`,
and `key` when available.

`run_complete`
: Final run record. `extra.command` is set for `up`, `build`, and `image build`
entrypoints.

`benchmark_result`
: Emitted by `cove bench competitive` for each normalized benchmark cell.
`extra` includes `workload`, `tool`, `source`, `methodology`, and `reason` when
the cell is `not_measured`.

## OpenTelemetry

Local JSONL is always available. OpenTelemetry export is optional and enabled by
the standard OTLP endpoint environment variable:

```bash
OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318 cove run -fork-from macos-15:dev -ephemeral
```

When `OTEL_EXPORTER_OTLP_ENDPOINT` is unset, cove does not attempt network
export. When it is set, cove may export the same run events to that OTLP
collector while still writing `metrics.jsonl`. Export failures must not hide the
local file or change the VM run result; they should be reported as telemetry
errors and the run should continue.

Use a local or operator-controlled collector for private workloads. Do not point
OTLP export at a third-party service unless the event attributes are acceptable
for that service to receive.

## Examples

A minimal forked run:

```json
{"timestamp":"2026-05-05T18:30:01.124000000Z","event_type":"fork_created","vm_name":"macos-15-dev-fork-1","image_ref":"macos-15:dev","duration_ms":142,"status":"ok","extra":{"run_id":"a12f9c01","child_name":"macos-15-dev-fork-1","child_path":"/Users/ci/.vz/vms/macos-15-dev-fork-1"}}
{"timestamp":"2026-05-05T18:30:04.912000000Z","event_type":"agent_ready","vm_name":"macos-15-dev-fork-1","image_ref":"macos-15:dev","status":"ok","duration_ms":3610,"extra":{"run_id":"a12f9c01"}}
{"timestamp":"2026-05-05T18:30:08.004000000Z","event_type":"run_complete","vm_name":"macos-15-dev-fork-1","image_ref":"macos-15:dev","status":"ok","duration_ms":6880,"extra":{"run_id":"a12f9c01"}}
```

A build with one cache miss and one cache hit:

```json
{"timestamp":"2026-05-05T19:04:22.448000000Z","event_type":"build_step","vm_name":"agentkit-base","image_ref":"ubuntu-runner","status":"ok","duration_ms":18342,"extra":{"run_id":"b73c91aa","step":"developer-tools","cache_hit":false,"key":"sha256:7b41..."}}
{"timestamp":"2026-05-05T19:04:22.711000000Z","event_type":"build_step","vm_name":"agentkit-base","image_ref":"ubuntu-runner","status":"ok","duration_ms":263,"extra":{"run_id":"b73c91aa","step":"golang","cache_hit":true,"key":"sha256:a10e..."}}
{"timestamp":"2026-05-05T19:04:23.006000000Z","event_type":"run_complete","vm_name":"agentkit-base","image_ref":"ubuntu-runner","status":"ok","duration_ms":20558,"extra":{"run_id":"b73c91aa","command":"build"}}
```

Reading the local stream:

```bash
tail -f ~/.vz/runs/20260505-183001-a12f/metrics.jsonl
jq -r 'select(.event_type=="agent_ready") | .duration_ms' ~/.vz/runs/*/metrics.jsonl
```

## Privacy and Security

Metrics are operational metadata, not a secret store. Event attributes should
avoid command lines, environment variables, file contents, tokens, and raw secret
URIs. Record stable identifiers such as image refs, step names, cache status,
durations, exit codes, and artifact names.

Run ids, VM names, image refs, and step names can still reveal project or
customer information. Treat `~/.vz/runs/<run-id>/metrics.jsonl` like the rest of
the run artifact directory: keep it on trusted storage, upload it only to
approved artifact systems, and apply the same retention policy as stdout,
stderr, screenshots, and control events.

OTLP export sends metadata off-host. Prefer loopback collectors for local
development, TLS-enabled collectors for network export, and short retention for
CI workloads that may run untrusted code inside ephemeral forks.
