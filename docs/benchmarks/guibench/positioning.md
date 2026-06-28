# guibench positioning: the native-macOS verifier wedge

Why guibench exists and where it is structurally stronger than the field. This
is the thesis the harness is built to deliver on; every claim below is backed by
shipped, tested code in `internal/guibench`, not a roadmap aspiration. It
complements [`guarantees.md`](guarantees.md) (the two load-bearing guarantees a
third-party corpus inherits) and design
[047 §16](../../designs/047-gui-agent-benchmark-harness.md) (the trycua/cua
pressure-test).

> **Privacy / brand gate.** This document is internal positioning. Public
> leaderboard *publication* and any public corpus/URL remain gated on the
> ROADMAP privacy/brand decision (design 047 §9 slice 6, §12). Nothing here
> publishes anything.

## The gap

Computer-use-agent benchmarks cluster around three substrates, and none of them
verifies **native macOS application state in a live environment**:

- **Web** (WebArena, VisualWebArena) — DOM/URL checks; no desktop OS.
- **Android** (AndroidWorld, 116 tasks) — per-app data-dir snapshots; not macOS,
  not whole-OS reset.
- **Windows / open OS** (Windows Agent Arena 154 tasks; OSWorld) — the right
  shape, wrong OS; macOS coverage is absent or a thin afterthought.
- **Grounding-only** (ScreenSpot, SeeClick) — static macOS *pixels*, but no live
  environment to act in and no outcome verification.

The unfilled quadrant is **verifiable, native macOS tasks an agent performs in a
live OS, scored against real on-disk / accessibility state**. That is guibench's
wedge: not "another OS-agent benchmark" but the macOS-native one with a verifier
the others are silent about.

## The moat (all shipped and tested)

What makes the wedge defensible is verifier rigor — the dimension trycua and the
GUI-agent leaderboards do not publish. Each item below is a pure, VM-free-tested
capability in the library:

1. **Per-task whole-OS fork isolation.** Every task runs in a fresh RAM-overlay
   fork; the discard *is* the reset (no roll-back step to get wrong). Stronger
   than WebArena (leaks across all tasks), AndroidWorld (per-app dir), WAA
   (golden-image revert). See [`guarantees.md`](guarantees.md) §1.
2. **Honest privilege tiers.** Every getter is classified Tier A (no grant) / B
   (Full Disk Access) / C (Apple Events + Accessibility), and the report publishes
   the per-task tier so a reader knows exactly what each number assumed.
3. **Flush discipline.** Persisted-state getters checkpoint before reading —
   cfprefsd for defaults, `PRAGMA wal_checkpoint(FULL)` for every SQLite store
   *including TCC.db* — so a correct agent action is never graded a false negative
   from an unflushed WAL (the largest OSWorld verifier-bug class).
4. **Verifier self-calibration.** Each task's verifier is checked against its own
   gold solution (must score 1) and a no-op (must score 0); the report carries a
   corpus-level [calibration rollup](../../designs/047-gui-agent-benchmark-harness.md)
   — the "is the validator correct" claim that underwrites every agent score.
5. **Corpus-shape gate.** The 116-task corpus is held to a distribution policy —
   every domain ≥2, a real complexity spread, per-tier floors, ≥10 infeasible
   tasks, and a ≥20% held-out split — enforced as a hard CI assert, so the corpus
   cannot silently unbalance as it grows.
6. **Anti-memorization.** Tasks are parameterized templates drawn from a seed, so
   a memorized answer to one instance does not transfer.
7. **AX-tree verification depth.** The accessibility metric reads structured AX
   state with role/title/identifier/value/description and the design-048
   enabled/settable state flags, so a task can assert logical UI state ("the Save
   button is enabled") rather than only pixels or async-flushed plists.

## The corpus

116 native-macOS tasks (the AndroidWorld parity floor) across 14 first-party app
domains — Finder, Safari, Settings, Terminal, Preview, Notes, Mail, Reminders,
Calendar, Contacts, TextEdit, Numbers, Maps, Calculator — with no foreign-app
installs. The mix spans four complexity (step-budget) levels, all three privilege
tiers, 10 infeasible tasks (the agent must correctly decline), and a 30-task
held-out split for generalization. Distribution is enforced by
`TestCorpusDistribution`, not asserted in prose.

## What is deliberately deferred

The wedge is the verifier and the corpus, not parity-chasing breadth. Three
capabilities are intentionally gated, not missing:

- **Live gold-solution sweep.** The structural self-check shape ships with every
  task; running the gold-solution-scores-1 / no-op-scores-0 sweep on a real macOS
  VM (`SelfCheckCorpus`) needs operator hardware and is the first thing to run on
  it.
- **In-guest AX snapshot RPC.** Design 048's structured in-guest snapshot is the
  follow-on; the metric-side selectors (item 7) ship now so the verifier is ready
  for it. The RPC needs vendor sign-off and a live host.
- **Public leaderboard.** Mechanics are built; publication waits on the
  privacy/brand gate (design 047 §12).

The throughline, per design 047 §16: **deepen the macOS-native verifier wedge and
weaponize rigor** — do not dilute it chasing cross-OS or cloud-product parity.
