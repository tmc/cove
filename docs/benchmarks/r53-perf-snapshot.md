---
title: R53 perf snapshot — cove run startup, fork-time, ctl roundtrip
status: Snapshot, host-load uncalibrated
date: 2026-05-07
---

# R53 perf snapshot

Tracker: R53-PERF-AUDIT (`E91C8CCE`).
Anchor: `origin/main` `a4b39d7` ("docs: close out design 039 facade-late move").
Host: darwin/arm64, macOS 26.4.1, Apple M4 Max.

This snapshot exercises the existing bench harness against post-design-039
HEAD. **It does not establish a clean baseline**: the host was under
extreme load while measurements ran (load average 192.79 / 222.70 / 243.85
1m/5m/15m), with multiple concurrent multi-pane sessions, a running
`com.apple.Virtualization.VirtualMachine.xpc` for an unrelated VM, and active
user-session UI processes.

The R53 brief explicitly says "if host is loaded, document and either skip or
note in summary". The numbers below are recorded in that spirit: as
**uncalibrated under load** rather than as a regression claim. Re-run on a
quiet host before drawing release-gate conclusions.

## Setup

- Cove binary: built at `a4b39d7`, signed with
  `internal/autosign/vz.entitlements`. `cove version` reported
  `cove a4b39d755613 (commit a4b39d755613, built 2026-05-08T01:23:47Z)`.
- Parent VM: `hermes-mlx-go-60g-v10` (60 GiB logical, 39.2 GiB stat-blocks,
  stopped). This is the same parent used for the v0.1.1 baseline at
  `bench/fork-time/results-20260427.md`.
- Bench harness: `cmd/fork-bench` (existing). No new bench code added.

## Measurements

### Fork-only wall time (`cove fork`)

```
go run ./cmd/fork-bench -cove ./cove -parents hermes-mlx-go-60g-v10 \
    -runs 5 -max-fork 250ms
```

| Run | Fork wall (ms) |
|-----|----------------|
| 1   | 278            |
| 2   | 788            |
| 3   | 423            |
| 4   | 219            |
| 5   | 309            |

Median: 309 ms. Min: 219 ms. Max: 788 ms.

Baseline (`bench/fork-time/results-20260427.md`, 2026-04-27, M4 Max,
v0.1.1 follow-up, three runs against same parent): 132 / 136 / 140 ms.

Apparent delta: ~2.2x slower at the median. **This is not a regression
claim.** The 4x spread between min and max within a single 5-run sample
(219 ms .. 788 ms), and the absence of variance that wide on a quiet host
in `results-20260427.md` (132-140 ms, 8 ms total spread), is consistent
with host-load contention rather than a code regression. Run 4 (219 ms) is
within ~50% of the baseline upper bound; it suggests the underlying
clonefile path is intact.

### CLI roundtrip (`./cove version`, `./cove --help`)

5 runs each, single-shot wall time via `/usr/bin/time -p`:

| Command           | Run 1 | Run 2 | Run 3 | Run 4 | Run 5 | Median |
|-------------------|-------|-------|-------|-------|-------|--------|
| `./cove version`  | 0.94  | 0.40  | 0.70  | 0.81  | 0.49  | 0.70 s |
| `./cove --help`   | 0.24  | 0.27  | 0.38  | 0.34  | 1.76  | 0.34 s |

Brief target: sub-500 ms for `cove version`. Observed median 700 ms.
**Cannot be attributed to a code regression** under load average 200+.
`cove --help` median 340 ms is closer to target and tracks process-launch
overhead.

### Control socket roundtrip (`cove ctl status`)

`./cove ctl status -vm default`, 5 runs:

| Run | Wall (s) |
|-----|----------|
| 1   | 0.86     |
| 2   | 0.93     |
| 3   | 0.43     |
| 4   | 0.64     |
| 5   | 0.62     |

Median: 0.64 s. Brief target: sub-50 ms.

The brief target measures the RPC roundtrip alone; this measurement
includes Go process launch + cobra cmd parse + control-socket dial
+ unary RPC. Process-launch alone has been observed at 240-340 ms median
under load (see `cove --help` row), so the RPC component is bounded above
by `0.64 s - 0.34 s = ~300 ms`. That is still above the 50 ms brief
target, but cannot be cleanly attributed without a quiet host.

## Verdict

| Metric                   | Baseline      | Observed (under load) | Verdict on regression                            |
|--------------------------|---------------|-----------------------|--------------------------------------------------|
| 60 GiB fork wall (median) | 132–140 ms   | 309 ms                | inconclusive — re-measure on quiet host          |
| 60 GiB fork wall (min)    | 132 ms       | 219 ms                | within 1.66x; suggests clonefile path intact      |
| `cove version` startup    | not on file  | 700 ms median         | cannot test against undocumented baseline        |
| `cove --help` startup     | not on file  | 340 ms median         | cannot test against undocumented baseline        |
| `cove ctl status`         | <50 ms target | 640 ms median        | inconclusive — process-launch dominates          |

No code regression is asserted by this snapshot. The single durable signal
worth flagging is the **wide intra-sample variance** (219 ms .. 788 ms) on
fork-only wall time. The 2026-04-27 baseline showed an 8 ms spread in three
runs; the current 5-run sample shows 569 ms. Some of that is host load,
but if the variance persists on a quiet host, that is itself a regression
worth investigating.

## Recommendations

1. **Re-run on a quiet host** before any v0.5 tag cut. The fork bench is the
   load-bearing measurement for the v0.1.1 / v0.3 fork-only claim; it deserves
   a clean repro.
2. **Add a startup-time baseline.** Brief flagged sub-500 ms `cove version`
   as "something's wrong" but no checked-in baseline exists. After a quiet
   re-run, drop the median into `bench/fork-time/` (or equivalent) so future
   audits have a comparison point.
3. **Hold v0.5 perf claims to evidence on file**, not this snapshot. The R50
   readiness audit (`docs/release/r50-readiness.md`) does not cite perf, so
   this snapshot does not block any tag decision.

## Constraints honored

- Single doc commit (this file).
- No new benches added; existing `cmd/fork-bench` exercised.
- No code changes.
- Host load explicitly recorded; numbers labelled as uncalibrated.
- No regression-finding filed because the premise (clean host) was not met.
