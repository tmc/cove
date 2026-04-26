# Roadmap update — post-v0.1.0 ship

**Status**: draft v0
**Author**: cove team
**Date**: 2026-04-26
**Supersedes**: nothing — additive history on top of `012-product-roadmap-2026.md`
**Cross-references**:
- `012-product-roadmap-2026.md` — the strategic 12-month plan (2026 → 2027), drafted 2026-04-25 morning
- `011-beat-lume-roadmap.md` — the tactical 0.1 → 0.4 plan, drafted 2026-04-22
- `013-vm-fork.md` — the VM-fork design doc, shipped on `feat/cove-run-restore-snapshot` and merged for v0.1.0
- `~/go/src/github.com/tmc/tmclabs/products/cove/roadmap/` — the planner-strategist artifact set (positioning, gaps, safety posture, launch sequence, success metrics, skeptic pass) drafted 2026-04-25 afternoon

## Why this doc exists

`012-product-roadmap-2026.md` is the strategic engineer-maintainer roadmap.
It was drafted 2026-04-25 morning, before the v0.1.0 ship arc concluded
2026-04-26 with [tag v0.1.0](https://github.com/tmc/cove/releases/tag/v0.1.0).
The doc is fundamentally sound; this update applies post-ship deltas as
additive history rather than rewriting the original.

Two reasons not to rewrite 012:

1. **012 is dated input to other artifacts.** The planner-strategist set
   (`~/go/src/github.com/tmc/tmclabs/products/cove/roadmap/`) cites 012 v1
   verbatim. Rewriting 012 invalidates those citations.
2. **Atomic snapshots beat continuously-edited docs.** A future reader who
   wants to know "what was the state of the strategic roadmap 2026-04-25"
   can read 012. They read this doc to learn what changed between then and now.

When the next strategic refresh happens (likely after the Q2 2026 soft-reset
matrix runs), the right move is `015-product-roadmap-2026-Q3-refresh.md` —
a successor doc, not an in-place edit.

## What changed between 012 and 014

### v0.1.0 shipped 2026-04-26 (ahead of 012's Q2 2026 schedule)

The original 012 §Q2 2026 milestone called for shipping 0.2 *and* the F1
benchmark *and* the soft-reset isolation suite *and* the F3 v1 adapter *and*
the Tart-import best-effort path inside one calendar quarter. Open Question
1 in 012 acknowledged this was more than fits in Q2 and asked what gets
sacrificed. As of 2026-04-26, **most of what 012 scoped for the next 0.2
release plus parts of Q3/Q4 already shipped in v0.1.0**:

| 012 location | Item | Status (2026-04-26) | Commit |
|---|---|---|---|
| §F1 marquee (line 353) | `cove fork` CLI with fresh machine.id | **shipped** (foundation Phase 1+2+3) | `d250336` |
| §F1 marquee (line 353) | `ForkVMDisk` clonefile primitive + CoW divergence test | **shipped** | `27ee2d3` |
| §F1 marquee (line 353) | `cove run -restore <name>` semantics | **shipped** (reframe + design doc 013) | `dad6114`, `2a06bfb` |
| §Q2 2026 (line 502) | tart-format OCI image pull (was Q3 best-effort fallback in 012) | **shipped** (foundation + wiring) | `9feef25`, `7444dda` |
| §Q2 2026 (line 502) | lume tar-split format pull | **shipped** | merged via `next` |
| §Q2 2026 (line 502) | async snapshot save (LRO infra) | **shipped** | merged via `next` |
| §Q2 2026 (line 502) | TCC routing via user agent (the Gap 3 architectural fix) | **shipped** | merged via `next` |
| §Q2 2026 (line 502) | agent auto-upgrade + health monitor | **shipped** | merged via `next` |
| §Q2 2026 (line 502) | linux quality fixes | **shipped** | merged via `next` |
| §F2 marquee (line 390) | `cove build` (docker-build-for-macOS) | **not started** | — |
| §F1 marquee (line 353) | published F1 fork-time benchmark on M2/M3 | **not started** | — |
| §F1 marquee (line 353) | soft-reset 6-concern matrix | **not started** | — |
| §F3 marquee (line 407) | Agents-SDK + sandbox-runtime adapters v1 | **not started** | — |

### Implication: re-plan the next 60–90 days

The original 012 Q2 2026 plan assumed v0.1 → v0.2 inside the quarter. That
sequence collapsed: v0.1.0 already contains most of the v0.2 surface 012
imagined. The next milestone (call it 0.2 or 0.3, naming TBD) should be
defined by what *didn't* ship in v0.1.0:

1. **F1 fork-time benchmark, published.** The `bench/` harness, on M2/M3
   hardware, against the workload defined in 012 (4 GB RAM, 30 GB disk,
   macOS 15 image). Output: a single number with provenance, not "sub-3s"
   marketing copy. Per `positioning-skeptic-pass.md` O7: if it's 4s or 8s,
   the README updates and the headline metric becomes a different thing.
2. **Soft-reset 6-concern matrix.** Per 012 §F1 risks + planner artifact
   `04-product-gaps.md` Gap 1, this is the single biggest pivot trigger in
   the 12-month plan. A repeatable harness that produces per-concern
   pass/fail/limit results: TCC residue, System Keychain, AppleID
   throttling at N=50, GlobalPreferences leakage, FileVault SecureToken
   cycle (macOS 15+), orphaned-daemon residue. Without this, the throughput
   claim in §Executive Summary is rhetoric.
3. **F3 Agents-SDK adapter v1.** A single concrete adapter (proposal:
   OpenAI Agents SDK) with a working example. Captures the partner-list
   wedge while it lasts; the durable wedge per `positioning-skeptic-pass.md`
   O1 is privacy-sensitive cohort, but the adapter is the discoverability
   layer.
4. **Apple SLA disclosure on README + INSTALL.md** (per
   `positioning-skeptic-pass.md` O9). cove inherits the per-Mac 2-instance
   limit, same as every other Mac virtualization product. Cite the SLA
   clause. This is half a day of writing and removes a disqualify-on-sight
   compliance objection.
5. **License-arithmetic table on README front page** (per `04-product-gaps.md`
   Gap 5). The cove vs Tart vs Orchard vs tart-guest-agent vs Lume table
   from `01-positioning.md`. Currently buried in 012 §Competitive
   landscape; on the README front page it converts compliance officers.

Pick 1–3 for the v0.2/v0.3 ship. 4 and 5 are zero-cost wins to ship now
even before the next tag.

### What 012 didn't cover that the planner artifacts added

Three planner artifacts produced after 012 was drafted contain content that
should be referenced from 012 going forward. They live outside the cove
repo (per the PIVOT-DIRECTIVE: 012 = engineer-maintainer audience, planner
set = co-founder/contributor/investor audience). They are still authoritative
for the topics they cover.

#### `SAFETY-POSTURE.md` (195 lines, planner artifact)

The launch-day SAFETY.md that ships in the cove repo on launch day. 012
has no safety section at all. SAFETY-POSTURE.md establishes:

- 5 guardrails: host-bounded control plane (vsock-only), strict
  control-socket access (0600 + token), human-in-the-loop escalation
  (sudo only for one-shot disk injection), zero telemetry forever,
  auditability by default (`~/.vz/audit/host-cp.log`)
- 6 known limitations honestly disclosed: TCC + VirtioFS, Apple-platform
  release-cadence exposure, CI workload maturity vs Tart, macOS-guest-only
  scope, **the per-eval user-account soft-reset isolation primitive
  fragility** (the load-bearing claim)
- "What changes if cove gains a new capability?" — every PR touching the
  trust boundary updates SAFETY.md in the same PR. Hard rule.

**Action for the cove repo**: copy SAFETY-POSTURE.md → `SAFETY.md` on the
release branch before the next tag. The PR template should reference it.

#### `positioning-skeptic-pass.md` Apple SLA (O9)

The 9-objection skeptic pass has one objection (O9, 2-instance-per-Mac SLA)
that's not in 012 and not in any cove repo doc. Cited on a compliance
review, this is disqualify-on-sight. Cited proactively in INSTALL.md +
README + launch-day-FAQ Q13, this becomes a trust signal.

**Action for the cove repo**: add a one-paragraph "Apple SLA" section to
README.md and INSTALL.md citing the macOS Sequoia SLA's 2-instance-per-host
clause. cove inherits the limit; cove never invents a workaround.

#### `04-product-gaps.md` Gap 1 contingency

012 §Honesty notes notes the soft-reset primitive needs Q2 validation but
declines to commit a counter-positioning if the matrix fails. The planner
artifact `SESSION-STATE.md` "Counter-positioning fork-trigger inventory"
commits to it: per-artifact rewrite percentages if 4+ of 6 concerns hit
hard limits. Headline pivots to "macOS dev workstation as code." The
fork-trigger inventory is the contingency 012's Open Question 10 asked for.

**Action for the cove repo**: when the soft-reset matrix runs (whenever
that lands), cite the planner artifact's fork-trigger inventory rather
than re-deriving the contingency in this repo.

### Stale claims in 012 to flag for next refresh

Not blockers, but the next strategic refresh should re-verify or update:

- **§Honesty notes "Date claims that need re-verification"** — Cirrus CI
  shutdown date (2026-06-01), Daytona Series A timing (Feb 2026), OpenAI
  Agents SDK launch (2026-04-15), Anthropic Computer Use launch
  (2026-03-23). All forecast at 012-draft time; some have happened, some
  may have slipped. Re-verify before the next strategic doc.
- **§Threats T0** — "OpenAI ships a first-party macOS sandbox in the
  Agents SDK partner list." 012 forecast 3–6 months from 2026-04-25. As
  of 2026-04-26, no public announcement. Re-check at 6-week intervals.
- **§Q2 2026 priority trade (Open Question 1)** — answered by the v0.1.0
  ship: most of Q2 2026 already shipped, leaving F1 benchmark + soft-reset
  matrix + F3 adapter as the next concrete unit of work.

### Stale claims in 012 to retire

- "v0.1 to ship in Q2 2026" — already shipped 2026-04-26.
- "feat/cove-run-restore-snapshot is in flight" (line 193) — shipped as
  v0.1.0; design captured in 013-vm-fork.md.
- The "preview of 0.2" framing in `RELEASE-NOTES-v0.1.0.md` v1 was
  already corrected in v0.1.0's release-notes amendment cycle (commit
  `6dc681c` on `docs/release-notes-v0.1.0`). Mentioning here for the
  audit trail; the file on `main` is the corrected one.

## Open questions resolved by v0.1.0 ship

| 012 Open Question | Resolution |
|---|---|
| Q1 (Q2 priority trade) | Soft-reset matrix and F1 benchmark are the surviving Q2 work; everything else shipped in v0.1.0 ahead of schedule |
| Q4 (trademark / branding for `cove`) | Still open — USPTO search remains a Q2 mandatory hygiene item before signed images go to a public registry. Memory note `project_rename_tarn.md` references prior naming exercise |

## Open questions still open

The following 012 Open Questions were not resolved by the v0.1.0 ship and
remain on the maintainer's plate:

- Q2: hosted control plane (`coveconnect.dev`) — still deferred to Q1 2027
- Q3: Discord vs forum vs neither — still pending choice
- Q4: trademark/branding USPTO search — still pending
- Q5: agentkit registry hosting — GHCR through 1.0 stands
- Q6: deeper integrated partner ask — still pending warm intro
- Q7: license of adapters — Apache-2.0 for adapters per maintainer
  recommendation; pending implementation at F3 time
- Q8: "thousands per hour" claim — depends on soft-reset matrix outcome
- Q9: privacy-sensitive cohort messaging — confirmed day-1 per planner
  set; landing page deferred to Q3
- Q10: counter-positioning if soft-reset fails — committed in planner
  artifact `SESSION-STATE.md` "Counter-positioning fork-trigger inventory"

## Action items from this update

In priority order:

1. **(zero-cost win)** Add Apple SLA disclosure to README.md +
   INSTALL.md per `positioning-skeptic-pass.md` O9. ~½ day.
2. **(zero-cost win)** Add license-arithmetic table to README front page
   per `04-product-gaps.md` Gap 5. ~½ day.
3. **(launch-day artifact)** Copy `SAFETY-POSTURE.md` → `SAFETY.md` in the
   cove repo. Reference from README. Update the PR template to require
   SAFETY.md updates for trust-boundary changes.
4. **(measurement)** Stand up `bench/` harness for F1 fork-time on
   defined M2/M3 workload. Publish whatever number it produces.
5. **(measurement, biggest pivot trigger)** Stand up the soft-reset
   6-concern matrix. Per planner artifact, this is the load-bearing
   validation gate; per 012 §Honesty notes, this is the single biggest
   open question.
6. **(feature)** F3 Agents-SDK adapter v1 — pick one (OpenAI Agents SDK
   recommended for partner-list wedge timing).
7. **(refresh trigger)** When (5) lands, draft `015-product-roadmap-Q3-refresh.md`
   informed by the matrix outcome. If 4+ hard limits hit, the planner
   `SESSION-STATE.md` fork-trigger inventory drives positioning rewrite.

Items 1–3 are doc/disclosure; can ship without a release tag. Items 4–6
are the next release scope. Item 7 triggers when item 5 produces results.

## What this doc does NOT do

- Does not rewrite 012. 012 stays as historical input to the planner
  artifact set.
- Does not redefine the 12-month strategy. The strategic shape from 012
  (5 marquee features F1–F5, quarterly milestones, T0–T4 threats,
  three-track sustainability) all stand.
- Does not commit a counter-positioning. That's the planner artifact's
  job (`SESSION-STATE.md` fork-trigger inventory) and is contingent on
  the soft-reset matrix outcome.
- Does not address the macosworld harness scope decision. That branch
  is kept separate per maintainer call 2026-04-26.

## When this doc gets superseded

When the next strategic refresh happens (target: after the soft-reset
matrix runs), draft `015-product-roadmap-2026-Q3-refresh.md`. That doc
supersedes both 012 and 014; this doc becomes historical context the way
012 became historical context for 014.
