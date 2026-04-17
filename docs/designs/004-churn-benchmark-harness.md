# cove build — churn benchmark harness

**Status**: draft v1 (post-second-opinion review)
**Author**: cove team
**Date**: 2026-04-16
**Target**: run before cove build v0.3 implementation begins

## 1. Goal

Pick the default `compact_mode` for `cove build` v0.3 using measured data instead
of intuition. `cove build` produces cached OCI layers by block-diffing
successive snapshots of `disk.img`. macOS guests emit gigabytes of boot-time
churn (unified logs, fseventsd, swap, diagnostics reports, Spotlight indexing)
even when the triggering vzscript is a no-op. Without compaction, every cached
layer carries that noise, which balloons storage and cache miss rates. We need
one default — `fast`, `targeted`, or `thorough` — and we need to justify it.

## 2. Hypotheses

- **H1**: `targeted` (zeroing known-churn paths) captures 70–85% of the size
  reduction of `thorough` at roughly 5–15% of its latency. Most macOS boot
  churn lives in a handful of well-known paths (`/var/log`, `/var/folders`,
  `/System/Volumes/Data/private/var/db/diagnostics`, swap).
- **H2**: `thorough` (`diskutil apfs eraseFreeVolumeSpace` on macOS, `fstrim`
  on Linux) adds 30–60s per step on macOS, materially hurting
  `cove build` pipeline wall time when chains exceed ~5 steps.
- **H3**: For small deltas (≤10MB legitimate work), boot churn dominates layer
  size by 100–1000x. For 1GB installs, churn is a rounding error and `fast`
  becomes acceptable.
- **H4**: Linux guests show far less churn than macOS; `fstrim` is cheap
  (≤2s) and probably wins outright on Linux. The interesting decision space
  is macOS only.

## 3. Measurement protocol

### Matrix

| axis | values |
|------|--------|
| compact_mode | `fast`, `targeted`, `thorough` |
| payload | noop script, 10MB install, 1GB install |
| guest | macOS 15, Ubuntu 24.04 ARM64 |

3 × 3 × 2 = 18 cells. Each cell is run **5 times** up-front (paired via
clonefile — see "Pairing methodology" below). Cold-boot between runs is
required — warm caches hide churn asymmetrically.

### Pairing methodology

Each compact_mode variant (`fast`, `targeted`, `thorough`) runs against the
SAME starting-state VM via clonefile pairing. We clonefile the base VM three
times (pre-mode variants), apply the same vzscript, then vary only the
compaction step. This removes per-boot macOS unified-log variance, Spotlight
indexing variance, and clock-drift-sensitive daemons from confounds.
Signal-to-noise improves ~3x at no additional wall-clock cost.

### Payloads

- **noop**: vzscript that runs `guest-exec true`. Exists solely to measure
  baseline boot churn.
- **10MB install**: `guest-exec brew install jq` (macOS) / `apt install jq`
  (Linux). Small, real-world install.
- **1GB install**: `brew install --cask google-chrome` (macOS) /
  `apt install build-essential` (Linux).

### Per-run metrics

For each run we record:

| metric | source |
|--------|--------|
| (a) pre-diff compaction latency | harness wraps compaction call |
| (b) block-diff wall time | existing diff path, timed |
| (c) uncompressed delta size | bytes written to temp file |
| (d) LZ4-compressed layer size | post-compression bytes |
| (e) total step wall time | (a)+(b)+(c)+(d) + fixed overhead |
| (f) guest compaction exit code | agent response |
| (g) host SSD free space delta | `df` pre/post |

### 19th cell — realistic workstation

Run the workstation chain — `homebrew → golang → claude-code` — end-to-end
under each tier. Record full-pipeline wall time and final layer-chain size on
disk. This is the only cell that reflects user-observable cost. It is what
ultimately decides the default. Run at n=5, paired via clonefile (same base
snapshot per tier triplet).

### 20th cell — cross-run layer-digest stability

Take a VM at a fresh-pulled base state. Run the same vzscript twice from
identical starting-state clonefile snapshots. Compute the LZ4+chunked layer
digest after each run. Compare. Run at n=5 pairs (10 independent runs) —
this cell is *unpaired* across pair members by construction since the
question is whether two independently-executed runs produce byte-identical
layers. Cover both macOS and Linux guests; the noop payload is sufficient
(churn-dominant case is the hardest for digest stability).

Output: digest-equality percentage across the 5 pairs. This feeds the
PASS/FAIL gate in §5.

## 4. Implementation sketch

- New tool at `./cmd/churn-bench/`. Thin wrapper — no new VM lifecycle code.
  Reuses VM start/stop from `runtime_lifecycle.go`, snapshot APIs from
  `snapshots.go`, and talks to the guest via `agent_client.go`.
- Compaction commands are driven from the host using a new `Compact(mode)`
  RPC on the existing vz-agent (`cmd/vz-agent/`). Agent injection via
  `agent_inject.go` is unchanged.
- Block-diff reuses whatever `cove build` v0.3 lands on — the harness should
  link that code as a library, not shell out. If the build path is not yet
  extracted, we extract it as part of this work (natural refactor; fits the
  existing pattern in `disk_bench.go`).
- Output: one JSON file per run under `./bench-results/<date>/<cell>-<n>.json`
  and a generated `results.md` summary table at the end.
- Runs nightly in CI on a dedicated M-series runner. Results archived to a
  GCS bucket; most recent dump symlinked into the repo for review.

A `disk_bench.go`-style narrow CLI is fine; we do not need a framework.

## 5. Decision rules

Applied after the matrix runs. Evaluated against the **19th cell** (workstation
chain) primarily, with 18-cell matrix as supporting evidence.

1. If `targeted` achieves ≥80% of `thorough`'s layer-size reduction at
   ≤25% of its per-step latency → **default `targeted`**.
2. Else if `thorough` is ≤10s per step in absolute terms on the test
   hardware → **default `thorough`** (size wins, latency tolerable).
3. Else if both `targeted` and `thorough` give <5% delta vs `fast` on the
   workstation chain → **default `fast`** (not worth the complexity).
4. Otherwise → **default `targeted`** as the safe middle.

Tiers remain user-selectable regardless of default. This only picks the
zero-config choice.

### PASS/FAIL gate (Cell 20): Cross-run layer-digest equality

- **PASS (≥95% equality across 5 pairs):** `--cache-from` cross-runner dedup story is VALID. Ship `cove build` with the documented promise.
- **FAIL (<95% equality):** `--cache-from` cross-runner semantics must be REFRAMED as "cache population only" (layers uploaded by Runner A cannot be re-used byte-for-byte by Runner B; Runner B pulls the layer but must locally verify content). Update `cove-build-design.md` docs accordingly and consider:
  - (a) Abandon block-diff for filesystem-aware diffing (major rework)
  - (b) Normalize block placement via APFS `fsck -F` + `diskutil apfs defragment` pre-diff
  - (c) Accept reframing and position cross-runner as local-population caching

## 6. Deliverables

- `./cmd/churn-bench/` tool, ~300–500 LOC, plus unit tests for metric capture.
- Agent-side `Compact(mode)` RPC in `cmd/vz-agent/` with three implementations.
- `bench-results/<date>/results.md` with the matrix table and the workstation
  chain numbers.
- ADR `docs/architecture/adr-0003-cove-build-compact-default.md` citing the
  numbers and the rule that fired.
- Nightly CI job definition.

## 7. Timeline

- **Day 1**: Harness scaffolding, agent-side `Compact()` RPC, JSON emitter.
  Validate against a single cell manually.
- **Day 2**: Full matrix run (unattended, ~6 hours wall on one M2 Pro).
  Budget: 18 cells × 3 paired samples = 54 paired samples + 5 paired
  samples for Cell 19 (workstation) + 10 independent runs for Cell 20
  (digest equality, 5 pairs × 2) ≈ 69 paired samples + 10 independent
  runs. Wall-clock is equivalent to the v0 plan (original: 54 + 15 = 69
  runs) because clonefile pairing amortizes the base-state prep cost.
  Results aggregation.
- **Day 3**: Analysis, ADR, CI wiring, review.

Total: **2–3 engineering days**. Bench run wall time is larger but unattended.

## 8. Open questions / confounders

- **APFS version variance**: `eraseFreeVolumeSpace` behavior has changed
  between APFS revisions. Pin a specific macOS patch version for the run
  and record it in every JSON blob.
- **Host SSD wear and thermal state**: repeated large writes on one device
  warp latency numbers. Run on a known-cool machine, log
  `powermetrics --samplers smc` snapshots, and interleave tiers rather than
  batching by tier.
- **Background macOS processes on the host**: Spotlight, Time Machine,
  iCloud Drive, Photos — all can steal IO. CI runner should have Spotlight
  disabled on the VM storage volume and iCloud signed out.
- **Guest Spotlight indexing**: first-boot indexing is non-deterministic.
  Hold the cold-boot idle window at a fixed 90s before measurement to let
  it stabilize.
- **Randomness in macOS unified log volume**: log verbosity varies by boot.
  n=5 paired via clonefile (see §3 "Pairing methodology") collapses most of
  this as a confound. Report variance per cell; flag any cell with residual
  variance > 20% for manual inspection rather than blind re-runs.
- **Cache interactions with `cove build`'s own layer cache**: must disable
  or clear between runs so we measure diff cost, not cache lookup.
- **Snapshot format assumptions**: `cove build` v0.3 may switch from raw
  block diffs to APFS snapshot deltas. If that lands mid-experiment, rerun
  the matrix — the hypothesis space shifts materially.

If any confounder can't be controlled to within ~10% noise, the decision
rule defaults to `targeted` rather than chasing a false signal.

## 9. Changelog

- **v1 (2026-04-16)**: post-second-opinion review. Added Cell 20
  (cross-run layer-digest equality) as PASS/FAIL gate for `cove build`'s
  `--cache-from` cross-runner dedup story. Changed default run count to
  n=5 up-front and introduced paired clonefile methodology (each tier
  triplet shares a base snapshot), collapsing per-boot variance as a
  confound. Wall-clock budget unchanged (~69 runs total on the test
  hardware).
- **v0 (2026-04-16)**: initial draft for Council review.
