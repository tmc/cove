# Design 042: Capture Latency Observability

Status: production capture sample wiring landed.
Author: Travis Cline
Date: 2026-05-10

## Problem

Design 041 Slice 2 measured SCKit latency in the spike binary, but the live
capture path now runs through `screenshots.go` behind `COVE_CAPTURE_BACKEND`.
After the default flips, regressions must be visible in normal run artifacts and
daemon metrics, not only in one-off spike output.

## Metric Events

Emit one event for every completed `captureDisplayImage` call:

`capture_latency`
: JSONL run event. `duration_ms` is wall-clock time from entering
`captureDisplayImage` until an image or error is returned.

Labels live under `extra`:

| Key | Values | Notes |
|---|---|---|
| `backend` | `sckit`, `cgwindow`, `framebuffer` | Backend that produced the final result. |
| `requested_backend` | `auto`, `sckit`, `cgwindow`, `framebuffer`, `window` | Operator request before fallback. |
| `fallback` | `true`, `false` | True when SCKit failed and CGWindow produced the final result. |
| `fallback_cause` | `tcc`, `window-missing`, `timeout`, `nil-image`, `other` | Same classifier as `sckitFallbackCause`. Omit when `fallback=false`. |
| `width`, `height` | integer pixels | Final image bounds when capture succeeds. |
| `run_id` | string | Present when a run bundle is active. |

`status` is `ok` on image success and `error` when capture returns an error
string. The error text goes in `extra.error`, truncated to 256 bytes.

## Prometheus

`coved` should aggregate `capture_latency` events from the existing event bus:

- `coved_capture_latency_ms_count{backend,requested_backend,fallback,fallback_cause}`
- `coved_capture_latency_ms_sum{backend,requested_backend,fallback,fallback_cause}`
- `coved_capture_latency_ms_max{backend,requested_backend,fallback,fallback_cause}`
- `coved_capture_errors_total{backend,requested_backend,fallback_cause}`

Do not label by VM name in Prometheus. VM-level detail remains in JSONL and
`cove runs export json`; low-cardinality daemon metrics are enough for fleet
regression alerts.

## Insertion Points

1. Add `internal/controlserver.CaptureMetrics` with one method:
   `EmitCaptureLatency(context.Context, CaptureLatencyEvent)`.
   The zero value does nothing.
2. Add a metrics sink field to the top-level `ControlServer` facade in
   `control_socket.go`; pass it to `internal/controlserver.Capture`.
3. Time `captureDisplayImage` in `screenshots.go`, after backend selection but
   before diff/OCR work. Populate the final backend from the branch that returns
   the image or error.
4. In run-bundle flows, adapt `RunBundle.EmitMetric` to the new capture metrics
   interface so captures append to `~/.vz/runs/<run-id>/metrics.jsonl`.
5. In daemon mode, publish the same event to `internal/coved.EventBus`; update
   `internal/coved/prometheus.go` to aggregate the low-cardinality series above.
6. Add `capture_latency` to `docs/features/metrics.md` and
   `docs/observability/runs-schema.md`.

## Tests

- `screenshots_sckit_test.go`: SCKit success, SCKit fallback, CGWindow default,
  and framebuffer branches each emit one event with the expected labels.
- `internal/coved/prometheus_test.go`: `capture_latency` events produce count,
  sum, max, and error series without VM labels.
- `internal/runs/show_test.go`: unknown `capture_latency` remains visible in
  verbose/export paths and does not enter the default lifecycle table.

## Non-goals

- Histograms. Prometheus summaries above keep the first implementation small;
  bucket design can follow once production distributions are known.
- Screenshot artifact upload. This only times capture and records metadata.
- Changing the SCKit fallback policy from Design 041.

## Implementation Notes

R111 wires production `captureDisplayImage` calls into run metrics. The
daemon-side Prometheus aggregation was already present; JSONL capture events now
carry the same low-cardinality labels for successful captures and capture
errors.
