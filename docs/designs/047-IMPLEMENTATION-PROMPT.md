# Implementation prompt — design 047 macOS GUI-agent benchmark harness

> Paste everything below the line into a fresh Claude Code session running in the
> cove repo. It is self-contained: it does not assume the design session's context.

---

You are implementing **design 047: macOS GUI-agent benchmark harness (CUA-bench-style)**
in the cove repo. The full design is at `docs/designs/047-gui-agent-benchmark-harness.md`
— **read it first, completely**, then the supporting docs it depends on:
`013-vm-fork.md`, `024-cove-runner-images.md`, `038-agent-sandbox-v2.md`, and
`bench/README.md`. The roadmap rows + gates are in `docs/designs/ROADMAP.md`
under "Benchmark horizon". The design was developed and adversarially
pressure-tested against NotebookLM notebook `7666f3d3`; you can chat with it
(`nlm chat 7666f3d3 "..."`) if you need to re-derive a decision, but the design
doc is canonical.

## What you are building, in one sentence

Turn `cove agent-sandbox` from something that *runs* a computer-use agent into
something that *scores* one against a corpus of verifiable native-macOS tasks —
the OSWorld pattern (declarative task + getter/metric verifier + per-task reset),
on macOS, using cove's RAM-overlay fork-per-task as the reset primitive.

## Ground rules (read before touching code)

1. **Verify the premise before every slice.** The design doc names files,
   packages, and `cove ctl`/`controlclient` symbols. Before you build on any of
   them, `grep`/`ls`/`go doc` to confirm they still exist and have the shape the
   doc claims — main moves. Do not trust the doc's line numbers; trust the
   current tree. If reality contradicts the doc, stop and reconcile (update the
   doc or adjust the plan) before writing code.
2. **This repo is PRIVATE and uses a coordinated commit-to-main flow.** Commit
   straight to the working branch; **do not open PRs**, and **do not push to any
   public infra** (homebrew tap, public OCI registries, public leaderboard). The
   public-leaderboard slice (6) is brand/privacy-gated — implement the mechanics
   but do not publish.
3. **Build + sign every time.** `make build` then re-sign:
   `codesign -s - -f --entitlements internal/autosign/vz.entitlements ./cove`.
   The binary will not run GUI/VM ops unsigned. Run `go build ./...`,
   `go vet ./...`, and `go test ./...` green before each commit.
4. **Russ-Cox style** (see the repo's CLAUDE.md): small focused packages, pure
   functions with table-driven tests, errors not panics, stdlib over deps, no
   "removed"-comment cruft. New package is `internal/guibench` (sibling of the
   existing `internal/bench`, which is the competitive-report normalizer — do
   **not** overload it).
5. **Commit atomically per slice**, golang-style message (50-char summary, blank
   line, body). If `~/bin/git-auto-commit-message --auto` fails (no API key in a
   worktree), hand-write the message in repo style — no "Claude Code" attribution.
   Add a git note after each commit per the repo convention.
6. **Tests that touch `~/.vz` must `t.Setenv("HOME", t.TempDir())` BEFORE any
   `vmconfig.BaseDir()` call** — otherwise they wipe the developer's real VMs.

## The substrate you build on (verify each, then use)

- Command dispatch: `command_registry.go` registers `bench` → `handleBenchCommand`
  (`bench_cli.go`) and `agent-sandbox` → `handleAgentSandboxCommand`
  (`agent_sandbox.go`). Add the new surface as `cove bench gui …` subcommands.
- Per-task reset: `cove run -fork-from <image> -ephemeral` (RAM-overlay fork,
  ~130–140 ms; `fork.go`, `fork_ephemeral.go`). One fresh fork per task; never
  reuse a fork; never soft-reset (design 015 closed that).
- Agent loop + replay: `agent_sandbox.go` already forks, runs the provider loop,
  and writes `~/.vz/runs/<id>/` (`events.jsonl`, `metrics.jsonl`, `manifest.json`,
  `screenshots/`, `replay/`). The provider `Result` today is just
  `{FinalAnswer string}` (`internal/agentsandbox/provider.go`) — you extend the
  *runner*, not necessarily that struct.
- Getter primitives via `internal/controlclient` (verify signatures):
  `AgentExecTyped(args, env, workDir)` → exit/stdout (execution + Tier-A/B/C
  command getters), `AgentReadFile(path)` (file getter), `Screenshot()` /
  `OCRAllText()` / `OCRClickText()` (screen getters). CLI equivalents are
  `cove ctl agent-exec | agent-read | ocr | detect | screenshot`.
- TCC/FDA: `cove doctor` TCC probe (`doctor_tcc.go`, `tcc_state.go`). Used to
  verify the base image's grants before saving it.
- Identity: `-recover-identity` flag (`macos.go`, `main.go`) regenerates SEP
  identity on first child boot.

## Failure modes that WILL bite you (front-loaded from the field)

These are the things that made OSWorld ship 300+ verifier fixes and that the
design's pressure-test surfaced. Build the defenses in from slice 1, not later:

- **Async-flush staleness (the #1 false-negative cause).** A GUI write to a
  preference is cached by `cfprefsd` and not on disk; app SQLite stores use WAL.
  Every persisted-state getter MUST flush before reading: `defaults` getter reads
  via `defaults read <domain> <key>` or `killall cfprefsd` first; `sqlite` getter
  runs `PRAGMA wal_checkpoint(FULL)` first. Put these in the `postconfig` hook. A
  getter that skips this reports "agents fail at macOS" when the verifier just
  read too early.
- **Getter privilege tiers** (design §5): Tier A = no grant (exec/file in
  user space/defaults/ocr/screenshot); Tier B = needs Full Disk Access baked into
  the base image (protected app SQLite stores, TCC-protected `~/Library`); Tier C
  = needs Apple Events + Accessibility grants baked in (applescript/JXA, AX-tree).
  FDA does NOT cover Apple Events — they are independent TCC services. A fresh VM
  has none of these; reads silently fail without the grant. **AX-tree is the
  reliable synchronous GUI-state probe**, which is why it (slice 3) comes before
  the corpus (slice 4).
- **SEP-identity sharing.** Cloned siblings share one iCloud/SEP identity unless
  `-recover-identity` is run per fork. v1 corpus excludes iCloud/Keychain/Apple-ID
  tasks; revisit later.
- **Contamination.** Never let gold-reference data be reachable from the guest
  (Berkeley RDI found a 73% exploit on OSWorld via `wget` of the gold file). Gold
  refs live host-side in the verifier; lock down guest egress during runs using
  the existing `network_policy.go` surface.
- **Crash handling + non-determinism.** A crashing task scores 0 and the suite
  continues (capture the exception); a long run resumes from a checkpoint;
  headline scoring is pass@1 over ≥3 runs with any >20%-variance cell flagged.

## Slices (build in this order; each is one reviewable commit, gate must pass)

**Phase 1 — internal eval (slices 1–4):**

1. **Task schema (parameterized) + verifier library, no VM.** New
   `internal/guibench`: the task record (`id`, `image`, `instruction`,
   `complexity`, `schema`+`params_seed` for parameterized variations, `config`
   setup steps, `evaluator{func, conj, result-getter, expected, options}`,
   `infeasible`). Metrics as **pure functions** (`exact_match`, `must_include`,
   normalized `fuzzy_match`, `file_exists`, `hash_equals`, `plist_equals`,
   `sqlite_row_matches`, `url_in`, `infeasible`) with table-driven tests. Tier-A
   getters as thin `controlclient` wrappers. Ships with zero tasks.
   **Gate:** every metric unit-tested with no VM; `go test ./internal/guibench/...`
   green.

2. **Runner + fork-reset proof + crash/checkpoint-resume.** Wire the engine to
   `cove run -fork-from -ephemeral` + the provider loop + the run bundle:
   fork → config setup → agent (step budget from `complexity`) → postconfig flush
   → getter → metric → discard fork. Crash → score 0 + continue; incremental
   `--checkpoint-dir`. Add `cove bench gui run --corpus <dir> --provider <p>
   [--task-id …] [--runs N]`, emit `score.json` beside the replay artifacts.
   **Empirically prove the RAM-overlay fork resets cfprefsd/LaunchServices/TCC/
   Spotlight/SQLite between tasks** (write the invariant test).
   **Gate:** one hand-authored task scores end-to-end; fork-reset invariant green.

3. **Pre-granted base image + Tier-C getters (AX-tree + AppleScript).** Curate a
   macOS base image with FDA + Apple-Events/Accessibility pre-granted, pinned
   clock, pinned app versions; verify grants with the `cove doctor` TCC probe
   before saving. Implement `accessibility` and `applescript` getters + the
   cfprefsd/WAL flush helpers. (Spike: AX via a one-shot `osascript`/Swift through
   `agent-exec` — no proto bump — vs a small guest-agent AX RPC; pick the helper
   path if it works.)
   **Gate:** AX getter reads a live window title; FDA `sqlite` getter reads a
   protected store without silent failure.

4. **Seed corpus v0 (10–20 tasks) + verifier self-check.** First cove-original
   tasks across Finder/Safari/System Settings/Notes/Preview/Terminal. Add a
   `--no-agent` self-check: run each task's setup + a known-good scripted solution
   and assert the verifier PASSES; plus a no-op that must score 0 (prove the
   verifier is correct, AndroidWorld-style). Add a manual-examine tool to inspect
   GUI-action-to-disk-state lag.
   **Gate:** self-check green for every seed task (good→1, no-op→0). This is the
   internal-eval milestone — it also regresses fork-reset + capture latency.

**Phase 2 — leaderboard (slices 5–6):**

5. **Corpus growth to ≥116–154 tasks + multi-provider scoring + variance.** Grow
   to parity-floor scale, parameterized for anti-memorization (templates with a
   seeded varying parameter; verifier computed from the same params). Run ≥3
   providers (Claude, GPT, Gemini computer-use) + a human baseline; per-domain +
   overall success-rate tables in the `bench/` citable format; pass@1 over ≥3
   runs; flag >20%-variance cells; record corpus + verifier versions.
   **Gate:** reproducible scored table at scale for ≥3 providers.

6. **Leaderboard + human-verified tier + contamination/version controls.**
   Versioned corpus, held-out split, in-guest egress lockdown during runs,
   result-submission format, and a **maintainer-run "verified" tier** (self-reported
   numbers get dismissed; only maintainer-executed runs are citable). Implement
   the mechanics; **do NOT publish publicly** (brand/privacy gate).
   **Gate:** a verified-tier run produces a citable result bundle; egress lockdown
   tested (a task agent cannot reach the gold reference).

**Phase 3 — reusable harness (slice 7):**

7. **Packaging (the wedge).** Stable task-schema spec doc, a `cove bench gui` spec,
   an example external corpus, the provider-interface abstraction so the VZ-fork
   backend is one swappable provider, and the fork-isolation + privilege-tier
   guarantees documented so third parties can run their own macOS corpus on cove.
   **Gate:** the example external corpus runs end-to-end via the documented spec.

## How to work

- Use a task list (TaskCreate/TaskUpdate) — one task per slice, plus a verify-premise
  subtask before each.
- After each slice: `go build ./... && go vet ./... && go test ./...`,
  `make build`, re-sign, run the slice's gate, then commit + git note.
- The hardware gates (live provider runs in slices 4–6, base-image build in slice
  3) need a real Apple-Silicon host with TCC grants and provider credentials
  (`OPENAI_API_KEY`/`ANTHROPIC_API_KEY`/`GEMINI_API_KEY`). If you're on a host
  without them, build the code + tests that don't need live runs, and clearly mark
  the gate as "blocked: needs operator hardware/credentials" rather than faking a
  pass (the repo's bench discipline: record `not_measured`, never infer).
- If a design decision is ambiguous, chat the notebook
  (`nlm chat 7666f3d3 "<question>"`) or ask the user — do not guess on the
  verifier-correctness or reset-isolation invariants; those are load-bearing.

Start by reading the design doc and confirming the substrate symbols exist, then
implement slice 1.
