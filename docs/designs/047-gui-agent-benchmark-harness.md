# Design 047: macOS GUI-agent benchmark harness (CUA-bench-style)

**Status**: roadmap input, NotebookLM-backed (notebook `7666f3d3`). Not scheduled.
**Author**: cove team
**Date**: 2026-05-29
**Depends on**: design [038](038-agent-sandbox-v2.md) (provider abstraction),
design [013](013-vm-fork.md) (fork/restore), design [024](024-cove-runner-images.md)
(image store), the run-bundle replay artifacts in `run_bundle.go`.

## 1. Goal

Turn `cove agent-sandbox` from a thing that *runs* a computer-use agent into a
thing that *scores* one against a corpus of verifiable macOS tasks — the
OSWorld/AndroidWorld pattern, but on the one OS none of them cover: native
macOS on Apple Silicon.

The deliverable spans three phases the product owner asked for, in order:

1. **Internal eval** — regress cove's own substrate (capture latency, fork-reset
   determinism, control-socket reliability) while real agents run real tasks.
   The benchmark is first a dogfooding/QA tool.
2. **Provider/model leaderboard** — run Claude, GPT, and Gemini computer-use
   models against a macOS corpus and publish comparative, reproducible scores.
   The benchmark becomes a credibility asset.
3. **Reproducible harness for others** — ship cove as the *substrate other people
   run a macOS computer-use benchmark on*: fork-isolated, deterministic, MIT.
   Benchmark-running becomes a wedge.

The first corpus is **cove-original native macOS tasks** (Finder, Safari, System
Settings, Mail, Notes, Preview, Terminal), not a port. No existing corpus covers
native macOS apps, so there is nothing to port that would be comparable; the
differentiation is the corpus itself.

## 2. Why this is cove's wedge (the macOS-native gap)

Verified against primary sources (see §14 citations):

- **OSWorld** (arXiv 2404.07972, NeurIPS 2024): 369 Ubuntu tasks + 43 Windows
  tasks, 134 execution-based verifiers. The abstract says "Ubuntu, Windows, and
  macOS," but the shipped harness treats Darwin only as a *host* (Apple Silicon →
  VMware Fusion `vmrun -T fusion`); there is **no macOS guest image and no
  macOS-app task set**. The paper itself notes running macOS on non-Apple
  hardware is legally blocked.
- **WindowsAgentArena** (arXiv 2409.08264): 154 tasks, Windows 11 only.
- **AndroidWorld** (arXiv 2405.14573): 116 tasks, Android only.
- **WebArena / VisualWebArena** (arXiv 2307.13854 / 2401.13649): 812 / 910 tasks,
  browser-only — no desktop OS at all.
- **ScreenSpot / ScreenSpot-Pro** (SeeClick, arXiv 2401.10935; OS-Atlas):
  *includes* macOS, but only as **static screenshots scored by point-in-box
  geometry** — a grounding benchmark (click the right pixel), not an agentic
  multi-step task benchmark with live state and reset.

So: nobody scores execution-verified, multi-step *agentic* tasks on a *live
native macOS* desktop. Apple Silicon + Virtualization.framework is the legal and
technical answer, and cove already has the substrate (§4). This is a clean wedge,
and it is exactly the place the competitive matrix says Cua has the stronger
agent-facing story today (`docs/strategy/competitive-2026-05.md`) — a macOS-native
benchmark is how cove answers that on its own turf.

## 3. What exists vs. what is missing

**Exists** (verified in the worktree, 2026-05-29):

- `cove agent-sandbox run --provider openai|anthropic|gemini|vertex --image <ref>
  --task "<prompt>"` forks an isolated ephemeral VM, runs the provider's
  computer-use loop, and writes per-run replay artifacts
  (`agent_sandbox.go:325-442`).
- Per-run bundle at `~/.vz/runs/<id>/`: `events.jsonl`, `metrics.jsonl`,
  `manifest.json`, `screenshots/`, `replay/` with `final-answer.md` and
  `ocr-text.txt` (`run_bundle.go`, `agent_sandbox.go:498-593`).
- Verifier *primitives* via `cove ctl`: `agent-exec` (run a command in the guest →
  exit code + output), `agent-read`/`agent-write` (file state), `ocr` / `detect` /
  `click-text` / `screenshot` (screen state) — `ctl.go:139-140,405-624,707-733`.
- Deterministic per-task reset: `cove run -fork-from <image> -ephemeral`
  composing APFS-clonefile fork (`fork.go`, `fork_ephemeral.go`), ~130 ms
  stopped-VM fork measured (`bench/fork-time/`).

**Missing** (this is the whole design):

- The `agent-sandbox bench` command today measures only **median latency and
  error rate** of one fixed mechanical task ("take a screenshot, click button at
  coords, type hello") — `bench/agent-sandbox-providers/run.sh`. There is **no
  task corpus, no per-task success verifier, no task-completion scoring, no
  leaderboard**. The provider `Result` is just `{FinalAnswer string}`
  (`internal/agentsandbox/provider.go`).

The substrate is ~80% there. This design adds the missing 20%: a task schema, a
verifier runner, a scorer, and a leaderboard.

## 4. The harness pattern, distilled from OSWorld/AndroidWorld/WebArena

All three converge on the same shape. cove adopts it with macOS-native getters.

**Task = a declarative record.** OSWorld's is the cleanest:

```
{
  id:          UUID,
  base_image:  cove image ref to fork from (replaces OSWorld "snapshot"),
  instruction: natural-language goal given to the agent,
  source:      provenance URL/note,
  setup:       [ ordered setup steps run after fork, before the agent acts ],
  verifier:    {
    func:     metric name or [names],
    conj:     "and" | "or",          // and ⇒ mean(scores); or ⇒ max(scores)
    result:   getter spec (reads live end-state off the guest),
    expected: getter spec (optional reference value),
    options:  {...},
  },
  infeasible:  bool,   // if true, success = agent correctly answers FAIL
  max_steps:   int,    // step budget (AndroidWorld scales this by complexity)
}
```

**The load-bearing idea is the getter/metric split** (OSWorld
`desktop_env/evaluators/{getters,metrics}/`): a *getter* pulls an artifact off
the live VM (file bytes, a plist value, an accessibility-tree node, a screenshot);
a *metric* is a **pure function** `(result, expected) -> float in [0,1]` that
scores it. Metrics are OS-agnostic and unit-testable **without a VM** — this is
what keeps the verifier library trustworthy and is why OSWorld-Verified could fix
300+ verifier bugs without re-running agents.

**Verification is execution-based, never an LLM judge for the core score.**
AndroidWorld reads device state via `adb` (SQLite, filesystem, settings);
WebArena runs `program_html` probes; OSWorld runs Python metric functions over
getter output. A VLM-judge getter MAY exist as one getter type among many (for
genuinely visual goals), but the headline score must be reproducible and
deterministic, so it leans on state checks.

## 5. cove getters and metrics (macOS-native)

This is the part no other benchmark has. Each getter is implemented on top of an
existing `cove ctl` primitive — no new guest agent surface is required for v1.

### Getter privilege tiers (be honest about what a fresh fork can read)

The naive assumption that "non-Apple-Events getters need no grant" is wrong on
modern macOS: reading a protected app's SQLite store (Mail, Messages, Safari
history) or many `~/Library` paths requires **Full Disk Access**, a TCC grant a
fresh VM does not have, and cove's own `cove doctor` TCC/FDA probe exists
precisely because that access "silently fails" otherwise (the `~/.vz` VirtioFS
TCC finding in project memory). So getters are classified by the grant they
need, and the base image is provisioned accordingly.

| Tier | Grant needed | Getters | cove primitive | Example task verified |
|---|---|---|---|---|
| **A** | none | `exec` (exit/stdout), `file` (user-space paths), `defaults`, `screen_ocr`, `screen_region`, `screenshot` | `ctl agent-exec`, `ctl agent-read`, `ctl ocr`/`detect`/`screenshot` | "Create a folder `Project` on the Desktop" → `agent-read ~/Desktop/Project` |
| **B** | Full Disk Access (baked into base image; verified by `cove doctor` TCC probe) | `sqlite` (protected app stores), `file` on TCC-protected `~/Library` paths, `tccdb` | `ctl agent-exec sqlite3` | "Save a draft email to Alice" → query FDA-protected Mail SQLite store |
| **C** | Apple Events + Accessibility automation (baked into base image) | `applescript`/`jxa`, `accessibility` (AX-tree node) | `ctl agent-exec osascript`, `ctl agent-exec` AX helper | "Open Safari to example.com" → AppleScript reads the active tab URL, or AX-tree reads the window title |

**AX-tree is the reliable synchronous probe.** Tier-B SQLite/file reads are
FDA-gated *and* subject to async-flush staleness (§7); the Accessibility tree
reports live UI state directly and synchronously. That makes Tier C not a
"later, nice-to-have" — it is the verifier of first resort for GUI-state goals,
which is why the slice plan (§9) builds the pre-granted base image and the AX
getter *before* the seed corpus, not after.

### Metrics (pure functions, unit-testable without a VM)

Metrics mirror WebArena's evaluator taxonomy plus OSWorld's tolerant variants:
`exact_match`, `must_include`, `fuzzy_match` (string-normalized, **not** LLM by
default — per ServiceNow/webarena-verified, the audited re-release that *removed*
LLM-as-judge and substring matching for type-aware structural comparison),
`file_exists`, `hash_equals`, `plist_equals`, `sqlite_row_matches`,
`url_in` (gold-in-pred), `infeasible` (success iff agent answered FAIL), and an
optional `vlm_judge` for explicitly visual tasks (flagged non-deterministic,
excluded from the headline reproducible score).

## 6. Reset / determinism: cove fork/restore vs. the field

| Benchmark | Reset mechanism (corrected against primary sources) | cove mapping |
|---|---|---|
| OSWorld | VM snapshot revert to `init_state` (`revert_to_snapshot`), skipped when `is_environment_used` is false (clean cloud start) | **direct**: a fresh fork *is* the clean-start case OSWorld optimizes for; the revert is unnecessary because the child is always fresh |
| WindowsAgentArena | 30 GB golden-image VM + per-task deterministic init script (golden-snapshot restore — **not** a full VM restart per task; the "restart per task" claim is unsupported — its cited UFO URL 404s) | golden image = a curated cove base image |
| AndroidWorld | emulator fresh `-no-snapshot` boot + per-app **data-dir** snapshot (`/data/data/<pkg>`) + fixed clock (`2023-10-15T15:34Z`) — per-app, not whole-OS | cove forks are whole-OS fresh-from-base; pin guest clock in the base image |
| WebArena | **per-task browser-context reset only**; stateful server containers are **batch** reset (docker stop/remove/redeploy) after all 812 — so server state leaks across tasks (`require_reset` false for all 812; issue #98) | cove per-task whole-OS fork has no cross-task leak — a strict improvement over the leakiest baseline |

cove's advantage is structural and quantitative. *Structural:* every baseline
has a weak reset — WebArena leaks server state across tasks, AndroidWorld's
per-app snapshot is not whole-OS, WAA reverts a 30 GB image, OSWorld recreates a
cloud instance. cove gives true per-task whole-OS hermetic state. *Quantitative:*
OSWorld-Verified spent a ~10-person, ~2-month effort plus an AWS migration to get
a full run from 10+ hours to ~20 minutes via 50× parallelism on 25 GB images
(image size and IOPS were the dominant levers, 50 GB → 25 GB). cove forks a
stopped macOS VM in ~130–140 ms via APFS clonefile and already fans out parallel
ephemeral forks (`bench/parallel-fork/`). Per-task isolation that is expensive
elsewhere is the default here. **The design must use this: every task gets a
fresh fork; no task ever sees another task's state.**

### Fork model and the SEP-identity hazard

cove has two fork models (design 013): **cold clonefile fork** (clone `disk.img`,
child diverges on its own disk) and **RAM-overlay ephemeral fork** (writes go to
a RAM overlay and vanish on shutdown). The benchmark uses **RAM-overlay ephemeral
per task** — the overlay's throw-everything-away property is exactly the reset
guarantee we want, and it is the cheapest.

But design 013 records a hazard the benchmark must respect: a cloned sibling's
`auxiliary-storage` carries the SEP keys that bind to the iCloud account, so
**siblings share the same SEP/iCloud identity** unless regenerated with the
existing `-recover-identity` flag on first child boot. Any task that touches
Apple ID sync, iCloud Drive, Find My, Keychain, or FairPlay would be corrupted by
duplicate identities across concurrent forks (sync conflicts, push-notification
confusion, account lockouts). **v1 rule:** exclude iCloud/Keychain/Apple-ID tasks
from the corpus, or run `-recover-identity` per fork for any task class that
needs a distinct identity. This is a corpus-scoping constraint, not a blocker.

### What the fork must be proven to reset (do not assume)

soft reset was empirically rejected as an isolation primitive (design 015), which
is *why* this design forks per task. But before the benchmark relies on
"fork = hermetic," Slice 2 must empirically verify the fork actually resets the
state a verifier reads: `cfprefsd`-cached preferences, Launch Services database,
TCC.db, Spotlight index, and app SQLite/WAL files. The RAM-overlay model should
make this hold by construction (all writes vanish), but the design treats it as a
*tested invariant*, not an assumption.

Determinism rules adopted from the field:
- Pin guest clock per task (AndroidWorld fixes `2023-10-15T15:34:00Z`).
- Bake all task data into the base image or the `setup` steps; **never** fetch
  gold-reference data over the network from inside the guest (OSWorld's
  contamination bug — see §8).
- Hold a fixed idle window after fork before the agent acts (the churn-bench
  doc, design 004, already established a 90 s macOS settle window; benchmark
  tasks can use a much shorter one because the base image is pre-booted).

## 7. Scoring

Per-task score is a float in `[0,1]` (binary for most tasks; fractional only
when an `and`-conjunction averages sub-metrics) — OSWorld's contract exactly.
Aggregate to **success rate %** per domain (Finder, Safari, Settings, …) and
overall. `infeasible` tasks score 1 only if the agent's terminal action is FAIL.

Run protocol follows the OpenAI CUA convention so numbers are comparable in
spirit: **pass@1, temperature 0.6, a fixed max-step budget** (CUA used 200;
cove tasks are shorter — budget per task via an AndroidWorld-style `complexity`
field). Report human-baseline alongside (OSWorld human 72.4%, WebArena human
78.2% — agent-vs-human gap is the headline framing).

A result is citable only under the existing `bench/README.md` rule: it records
host hardware, cove commit, timestamp, the exact corpus version + task ids, the
model id and provider, and **the verifier version** (verifier brittleness means
the corpus must be versioned and the verifier hash recorded).

### Pre-read flush discipline (the single most likely source of false negatives)

OSWorld's biggest verifier-bug class was async-vs-sync timing — it had to convert
`open_file` from async to blocking so a getter did not read state before the OS
flushed it. macOS has the same trap, sharper: a GUI write to a preference goes
through `cfprefsd` and is *not* immediately on disk, and app SQLite stores use
write-ahead logging, so a verifier that reads the `.db` file sees stale data.
Every getter that reads persisted state must flush first:

- **`defaults`/plist getter:** read through `defaults read <domain> <key>`
  (which goes through `cfprefsd` and returns the live value), or
  `killall cfprefsd` before reading the plist file directly. Never parse a
  `.plist` off disk without one of these.
- **`sqlite` getter:** run `PRAGMA wal_checkpoint(FULL)` against the live db
  before querying, or query through the app's own engine — never parse a stale
  `-wal`-backed `.db` offline.
- **`postconfig`** (OSWorld's pre-read setup hook) is where these flushes run, so
  the getter sees settled state.

A getter that ignores this will reject correct agent actions and the benchmark
will read as "agents fail at macOS" when it is actually "the verifier read too
early." This is the failure mode §8 calls out as most probable.

## 8. Risks (verified failure modes of the field, not hypotheticals)

- **Contamination.** A Berkeley RDI analysis found OSWorld embeds gold-reference
  files at public HuggingFace URLs *inside the task config*; because the guest
  has internet, an exploit agent can `wget` the gold file into the exact path the
  evaluator checks (gold-vs-gold → score 1.0), reaching a reported 73% exploit
  score. **Rule:** gold references live host-side in the verifier, never reachable
  from the guest; the benchmark network policy denies egress except where a task
  explicitly needs it. cove already has named network policy
  (`network_policy.go`).
- **Verifier brittleness.** OSWorld shipped, then OSWorld-Verified fixed 300+
  verifier issues (flaky timing, async-vs-sync file opens, format intolerance).
  **Rule:** every metric is a pure function with table-driven unit tests; the
  verifier library has its own test suite that runs without a VM; corpus + verifier
  are versioned together.
- **Reset non-determinism.** macOS boot churn (design 004) and TCC state vary per
  boot. **Rule:** the base image is pre-booted, pre-TCC-granted, and settled
  before being saved; every task forks from that exact image; record any
  cell with >20% score variance for manual inspection rather than re-rolling.
- **TCC / FDA / Apple Events (split correctly — see §5 tiers).** Three distinct
  TCC services are involved and the design must not conflate them: (a) Tier-A
  getters need no grant; (b) Tier-B `sqlite`/protected-`file` getters need **Full
  Disk Access** — a fresh VM lacks it and reads *silently fail* (cove's `cove
  doctor` TCC probe exists for exactly this); (c) Tier-C `applescript`/`accessibility`
  getters need **Apple Events + Accessibility** automation grants, which are
  *independent* TCC services from FDA (project memory: FDA pre-grant does NOT
  cover Apple Events). **Rule:** the base image is provisioned with FDA and
  AE/AX pre-granted and the grant state is verified by the `cove doctor` TCC probe
  before the image is saved; grants are baked into the image, never done per run.
  Whether `runs-on: terminal` (host-streamed, no AE prompt) vs a TCC-granted GUI
  path is used is a per-task setup choice.
- **Cost.** Live provider runs cost real money per task × corpus × models ×
  pass@k. **Rule:** the corpus has a `test_small` subset (OSWorld pattern) for CI;
  full-matrix live runs are gated like `RUN_LIVE=1` is today.

## 9. Slices

Each slice is independently shippable and reviewable. The order is the one the
NotebookLM pressure-test corrected the first draft into: parameterization and
crash/resume belong in the *foundation* (retrofitting them is a rewrite), the
pre-granted base image + the reliable AX getter must exist *before* the corpus
(or every task is written against brittle async CLI probes), and a leaderboard
needs corpus scale *before* it is published (a 10–20-task leaderboard is
statistically meaningless next to AndroidWorld's 116 / WAA's 154 / OSWorld's 369).

Phase 1 (internal eval) = Slices 1–4. Phase 2 (leaderboard) = Slices 5–6.
Phase 3 (reusable harness) = Slice 7.

1. **Task schema (with parameterization) + verifier library (no VM).** Define the
   task schema in a new `internal/guibench` package — instruction, `image`,
   `config` setup steps, `evaluator{func, conj, result-getter, expected, options}`,
   `infeasible`, `complexity`, **and a `schema`+`params_seed` for AndroidWorld-style
   parameterized variations** (foundational so tasks aren't authored statically
   then thrown out). Implement metrics as pure functions with table-driven tests;
   implement Tier-A getters as thin wrappers over `controlclient`. Ships with zero
   tasks. Gate: every metric unit-tested without a VM.
2. **Runner with fork-reset proof + crash/checkpoint-resume.** Wire the engine to
   `cove run -fork-from -ephemeral` + the provider loop + the run bundle:
   fork → config setup → agent (step budget from `complexity`) → postconfig flush
   → getter → metric → discard fork. A crashing task scores 0 and the suite
   continues (AndroidWorld's try/except → exception-info), with an incremental
   checkpointer so a long run resumes (`--checkpoint-dir`). **Also in this slice:
   empirically prove the fork resets `cfprefsd`/LaunchServices/TCC/Spotlight/SQLite
   state between tasks (§6 invariant).** Add `cove bench gui run --corpus <dir>
   --provider <p> [--task-id …] [--runs N]`; emit `score.json` alongside replay
   artifacts. Gate: one end-to-end task scores; fork-reset invariant test green.
3. **Pre-granted base image + Tier-C getters (AX-tree + AppleScript).** Build a
   curated macOS base image with FDA and Apple-Events/Accessibility pre-granted,
   a pinned system clock, pinned app versions, and the grant state verified by the
   `cove doctor` TCC probe before save. Implement the `accessibility` and
   `applescript` getters (and the cfprefsd/WAL flush helpers from §7). This comes
   before the corpus because AX-tree is the only reliable synchronous GUI-state
   probe; authoring the corpus first would force brittle OCR/pixel verifiers.
   Gate: AX getter reads a live window title; FDA `sqlite` getter reads a protected
   store without silent failure.
4. **Seed corpus v0 (10–20 tasks) + verifier self-check (internal-eval milestone).**
   Author the first cove-original tasks across Finder/Safari/Settings/Notes/
   Preview/Terminal. Add a `--no-agent` self-check that runs each task's setup + a
   known-good scripted solution and asserts the verifier passes; plus a negative
   check that a no-op scores 0 (the AndroidWorld "is the validator correct"
   discipline). Add a manual-examine tool (OSWorld's `run_manual_examine` shape)
   so a human can inspect the GUI-action-to-disk-state lag. This milestone also
   regresses fork-reset determinism and capture latency as a side effect — Phase-1
   internal-eval value is realized here, before any external numbers.
5. **Corpus growth to ≥116–154 tasks + multi-provider scoring + variance.**
   Grow the corpus to parity-floor scale, parameterized for anti-memorization.
   Run the matrix across ≥3 providers + a human baseline; report per-domain and
   overall success rates in the `bench/` citable format, **pass@1 over ≥3 runs
   with any >20%-variance cell flagged for manual inspection** (model behavior is
   non-deterministic even in a deterministic VM). Record corpus + verifier
   versions. Gate: a reproducible scored table at scale for ≥3 providers.
6. **Leaderboard with a human-verified tier + contamination/version controls.**
   Publish a versioned corpus, a held-out split, in-guest network-egress lockdown
   during runs (gold references stay host-side), a result-submission format, **and
   a mandatory maintainer-run "verified" tier for headline numbers** (the XLANG
   model: self-reported numbers get dismissed; only maintainer-executed runs are
   citable as verified). Gated by the ROADMAP privacy/brand decision before any
   *public* leaderboard.
7. **Reusable-harness packaging (the wedge milestone).** Stable task-schema spec,
   a `cove bench gui` spec doc, an example external corpus, the provider-interface
   abstraction so the VZ-fork backend is one swappable provider, and the
   fork-isolation + privilege-tier guarantees documented so third parties can run
   their own macOS corpus on cove.

## 10. Anti-memorization (parameterized task templates)

A static corpus of N fixed tasks is memorizable; AndroidWorld defends with
parameterized templates seeded at task start to produce millions of variations.
cove adopts the same: each task is a template with a `schema` of typed parameters
drawn from a controlled pool by `params_seed`. The verifier is computed from the
same parameters, so the gold answer is never fixed. Examples:

- **Notes/Safari:** "Search Wikipedia for `{TOPIC}`, copy the first paragraph,
  paste it into a new Note titled `{NOTE_TITLE}`." — `{TOPIC}` from a topic pool;
  verifier reads the Notes SQLite store (Tier B) for a note with that title whose
  body contains the fetched paragraph.
- **System Settings:** "Set the display appearance to `{THEME}` and the accent
  color to `{COLOR}`." — verifier reads `defaults` (Tier A, with cfprefsd flush).
- **Finder/Terminal:** "Move every file in `~/Documents` containing `{KEYWORD}`
  to `{TARGET_DIR}`." — `{KEYWORD}`/`{TARGET_DIR}` seeded; verifier is a Tier-A
  `exec`/`file` check over the resulting tree.

## 11. Minimum credible launch (Phase-2 exit bar)

The headline numbers must clear the bar the field is judged by, or they get
dismissed as vendor self-reports:

- **Task count ≥ 116–154** (parity floor with AndroidWorld 116 / WAA 154; OSWorld
  is 369). A 10–20-task leaderboard is not publishable.
- **≥ 3 providers** (Claude, GPT, Gemini computer-use) **+ a human baseline**
  reported alongside (the agent-vs-human gap is the framing).
- **pass@1 over ≥ 3 runs**, per-domain + overall success rate, every >20%-variance
  cell flagged.
- **A maintainer-run "verified" tier.** Self-reported scores are routinely
  dismissed (OSWorld aggregators carry vendor self-reports, not XLANG-verified
  numbers); headline claims must come from maintainer-executed runs of submitted
  agent code, with corpus + verifier versions pinned.

## 12. Decision rules

- **Verifier default is execution/state-based, not LLM-judge.** A `vlm_judge`
  metric exists only for tasks with no programmatic end-state, flagged and
  excluded from the headline reproducible score.
- **One fresh RAM-overlay fork per task, always.** No soft reset (design 015).
- **Getters are classified by privilege tier (§5); the base image carries
  exactly the grants its corpus needs**, verified by `cove doctor` before save.
  "Needs no grant" is true only for Tier A.
- **Every persisted-state getter flushes before reading** (§7 cfprefsd / WAL).
- **Corpus and verifier are versioned together**; a result cites both.
- **No gold reference reachable from the guest** (network-egress lockdown).
- **Exclude iCloud/Keychain/Apple-ID tasks in v1** unless `-recover-identity` is
  run per fork (shared-SEP hazard, §6).
- **No public leaderboard until the privacy/brand gate clears** (ROADMAP).

## 13. Open questions

- Step-budget policy: fixed (CUA's 200) vs. AndroidWorld's complexity-scaled?
  Lean complexity-scaled; macOS tasks are mostly short.
- AX-tree getter: a clean `ctl agent-exec` AX helper (osascript/Swift one-shot)
  vs. a small guest-agent AX RPC? Slice-3 spike decides; the helper path needs no
  proto bump. **Resolved in [048](048-guest-ax-snapshot-over-socket.md):** the
  XML osascript helper shipped first (no proto bump); a structured `AXSnapshot`
  RPC (geometry + enabled/settable + actions + stable element index, reusing
  axmcp's `appstate` semantics) is the fidelity follow-on.
- Partial credit: adopt OSWorld's `and`-mean fractional scoring or stay strictly
  binary for v0? Lean binary for v0 to shrink verifier surface.
- How much of OSWorld's *metric* code (Apache-2.0 pure functions) ports directly
  vs. re-implements in Go? String/file/table metrics port cleanly in spirit;
  macOS getters are all-new.
- Does the RAM-overlay fork fully reset `cfprefsd`/LaunchServices/TCC/Spotlight
  between tasks? Slice-2 invariant test answers this before the corpus relies on
  it.

## 14. Verified 2026-05-29

cove-substrate claims (against the worktree at `b14fdb6a`):

- 047 is the next free design number (`docs/designs/` tops out at 046).
- `agent-sandbox` run flow, replay bundle, and `Result{FinalAnswer}` confirmed in
  `agent_sandbox.go`, `run_bundle.go`, `internal/agentsandbox/provider.go`.
- Verifier primitives confirmed in `ctl.go` (`agent-exec`, `agent-read`,
  `agent-write`, `ocr`, `detect`, `click-text`, `screenshot`).
- Existing `agent-sandbox bench` measures latency/error-rate only
  (`bench/agent-sandbox-providers/run.sh`); no task-completion scoring exists.
- Fork/ephemeral reset primitives confirmed in `fork.go`, `fork_ephemeral.go`;
  ~130–140 ms fork measured in `bench/fork-time/`.
- RAM-overlay ephemeral fork + shared-SEP-identity hazard + the `-recover-identity`
  remedy are from design 013 (§Model B, lines ~108–111, ~230–233); the flag is
  real (`macos.go:502`, `main.go:162`).
- TCC/FDA pre-auth surface confirmed (`tcc_state.go`, `doctor_tcc.go`); FDA does
  not cover Apple Events (independent TCC services) per project memory.

Benchmark-field claims (§2/§4/§5/§6/§7/§8) are from primary sources, verified in
NotebookLM notebook `7666f3d3` and adversarially cross-checked (the research
synthesis refuted, e.g., a "WAA restarts the VM per task" claim and an
"OSWorld-Verified reduces contamination" over-claim): OSWorld arXiv 2404.07972 +
github.com/xlang-ai/OSWorld + os-world.github.io; WebArena 2307.13854 +
github.com/web-arena-x/webarena (issue #98 state-leak); VisualWebArena 2401.13649;
AndroidWorld 2405.14573 + github.com/google-research/android_world; WindowsAgentArena
2409.08264; SeeClick/ScreenSpot 2401.10935 + ScreenSpot-Pro; OpenAI CUA
(cdn.openai.com/cua/CUA_eval_extra_information.pdf — eval methodology PDF; the
openai.com/index/computer-using-agent page is Cloudflare-gated); Anthropic computer
use (anthropic.com/news/3-5-models-and-computer-use); ServiceNow/webarena-verified;
Berkeley RDI contamination analysis (rdi.berkeley.edu, 73% exploit score).

## 15. Provenance

This design was developed with a NotebookLM notebook (`7666f3d3`) seeded with the
above primary benchmark sources plus cove's own substrate docs, then
adversarially pressure-tested against that notebook in two rounds. The
pressure-test corrected the first draft's baseline-reset misstatements, the naive
"non-AE getters need no grant" framing (→ the §5 privilege tiers), the missing
cfprefsd/WAL flush discipline (§7), the slice order (AX getter + pre-granted base
image moved before the corpus; parameterization and crash/resume moved into the
foundation), and added the §11 minimum-credible-launch bar and the §6 SEP-identity
constraint. Every NotebookLM claim about cove's own substrate was re-verified
against the filesystem before adoption.

## 16. trycua/cua eval surface vs. 047 (2026-05-29 pressure-test)

After the design was drafted, the eval coverage of the most direct competitor —
**trycua/cua** (17.3k★, "Sandboxes, SDKs, and benchmarks … macOS, Linux, Windows")
— was synced into the same NotebookLM notebook (`7666f3d3`, source
`trycua-cua-bench-and-eval-coverage`) and pressure-tested against 047. trycua's
surface, from their docs:

- **cua-bench** (their own framework): Python decorator tasks
  (`@tasks_config`/`@setup_task`/`@evaluate_task`/`@solve_task`), an **oracle
  reference solution** per task, a **2-container Docker** architecture, a
  **Playwright "simulated desktop"** provider (HTML/CSS `win11`/`macos` *themes*,
  not a real OS) for the bulk of tasks plus a thin `native` provider, **macOS app
  helpers via `osascript`** (Notes/Reminders/Calendar), **GenAI task generation**
  (`cb task generate`), an **RL training dataloader** + HuggingFace-schema
  trajectory export, and agent-trace telemetry.
- **HUD integration**: they don't run OSWorld themselves — they wrap hosted
  **HUD** (hud.ai) to run **OSWorld-Verified (369)** and **SheetBench**, with a
  live streamed trace viewer.
- **ScreenSpot-v2 / ScreenSpot-Pro**: static click-prediction grounding scripts;
  plus a plan-vs-coordinate-vs-composed agent distinction.
- **Adapters roadmap**: Windows Arena (173, available), OSWorld/WebArena/
  AndroidWorld ("coming soon"), and **macOSWorld** (`macos-lume`, 100+, "coming
  soon") — the head-to-head with 047's macOS-native wedge.

### macOSWorld head-to-head (where 047 is structurally stronger)

On the four dimensions that decide benchmark credibility, the notebook found 047
stronger on all four, and trycua **silent** on three:

| Dimension | trycua | cove 047 |
|---|---|---|
| Reset / isolation | Lume + 2-container; no hard whole-OS per-task fork contract | APFS-clonefile RAM-overlay fork-per-task, ~130 ms, whole-OS hermetic (§6) |
| macOS state fidelity | `osascript` app-helpers (Notes/Reminders/Calendar) | privilege-tier getters A/B/C incl. **FDA sqlite + AX-tree** synchronous probe (§5) |
| Async-flush (cfprefsd/WAL) staleness | **silent** | explicit pre-read flush discipline (§7) |
| Contamination / egress | **silent** | host-side-only gold refs + egress lockdown (§8) |

trycua's silence on verifier rigor is itself the finding: their evaluations are
likely susceptible to exactly the async-flush false-negatives and gold-reference
contamination that 047 defends against by construction.

### What 047 adopts, defers, and skips

Of the categories trycua covers that 047 did not, the notebook (challenged on its
first ranking) settled on:

- **ADOPT-NOW — oracle-trajectory export (HF schema).** Slice 4 already authors
  known-good solutions and the run bundle already captures screenshots/actions/
  events, so emitting a HuggingFace-schema trajectory is near-zero marginal cost
  and yields a **native-macOS UI-grounding dataset no competitor has** (none run
  real macOS at scale). → ROADMAP Slice **4b**.
- **ADOPT-NOW — "verified-rigor" leaderboard metadata.** Publish per-task
  isolation, egress-lockdown status, privilege tier, and flush-discipline as
  first-class leaderboard columns — the OSWorld-Verified "300+ fixes" brand play,
  turned on trycua's silences. → ROADMAP Slice **6b**.
- **ADOPT-NOW — local interactive trace viewer.** An HTML timeline over the
  existing `~/.vz/runs/<id>/` bundles (merging the Slice-4 examine tool), closing
  trycua's `cb trace view` / app.hud.ai product-clarity lead with **no cloud
  dependency**. → ROADMAP Slice **8**.
- **ADOPT-LATER — GenAI task generation; third-party-benchmark adapters
  (OSWorld/WAA/WebArena/AndroidWorld as macOS-native getters); external
  eval-platform (HUD) streaming.** Valuable for corpus scale and reach, but only
  after the human-authored macOS-native baseline and verifier infra (Slices 1–4)
  are stable.
- **DELIBERATELY-SKIP — RL training dataloader, simulated/web-themed desktop
  provider, universal cross-OS single task spec, composed planning+grounding
  agent architecture.** Each is parity-chasing that dilutes the wedge or trades on
  ML/web infrastructure cove should not own: cove is the *real-macOS execution
  substrate*, not an ML framework, and a browser-faked desktop directly
  undermines the "native macOS on Apple Silicon" moat (and risks false positives
  where an agent solves the HTML sim but fails the real OS).

The throughline: **deepen the macOS-native verifier wedge and weaponize rigor;
do not chase feature-parity with a 17k★ general-purpose framework.**

### Deepening the AX-tree probe (the verifier-wedge follow-on)

The §5 "macOS state fidelity" row above is the wedge trycua is silent on, and a
deep pass over [`github.com/tmc/axmcp`](https://github.com/tmc/axmcp) (a mature
macOS AX-automation toolkit on the same `apple/x/axuiautomation` dep cove pins)
shows how to widen it further. The shipped `accessibility` getter emits a
four-attribute XML tree via a one-shot JXA dumper; axmcp's `computeruse/appstate`
builds a richer structured snapshot (per-element **geometry, enabled/settable,
available actions, and a stable element index**) — the Codex Computer Use
`get_app_state` shape. [048](048-guest-ax-snapshot-over-socket.md) specs adopting
that as an in-guest `AXSnapshot` RPC surfaced over the control socket (mirroring
the OCR-over-socket pattern), reusing axmcp's snapshot semantics; it rejects
proxying *host*-run axmcp because `axuiautomation` is a local-OS binding and the
guest AX tree can only be read in-guest. This resolves the §13 AX-getter open
question and raises `accessibility_match` precision beyond what the XML dumper
allows. → ROADMAP Slice **3b**.
