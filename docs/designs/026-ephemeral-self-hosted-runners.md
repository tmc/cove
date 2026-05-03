# Custom ephemeral self-hosted runners

**Status**: planning input, v2.5 (2026-05-03 round-2-folded). Owns the bridge between the
existing long-lived `vzscripts/github-runner.vzscript` (registration-mode
runner inside a permanent VM) and the v0.4 `cove-gha-runner` GHA wrapper
from design [021](021-v04-ci-executors-tracks.md). Ships the slices that
arrive BEFORE 021's full GHA wrapper, so users with a working manual
runner today can switch to fork-per-job ephemeral runners without
waiting on v0.4.

**Scope-source**: user prompt 2026-05-03 — "I have a VM that has manual
runners running in it right now but would like to get it to be faster /
more ephemeral." This is the on-ramp design 021's Slice 1 + design 024's
Slice 1 collectively imply but do not document end-to-end.

**v2 changes**: Slice 0 in v1 (vzscript-only) was killed by three
verified flaws (no `-env` flag on `cove run`, `# runs-on: daemon` runs
sequentially-blocking through `rsc.io/script`, `# secret:` directive
ships in `build_cache.go` for `cove build` only — NOT in
`vzscript_apply.go`'s `parseScriptMeta`). v2 replaces Slice 0 with `cove
runner job`: a one-shot Go subcommand that does `acquire JIT → fork →
inject token → run → teardown` atomically. Same LOC class (~150 LOC),
different layer, sidesteps every flaw. See "Lessons learned in v2"
below.

## Problem statement

Today's path:

1. User installs `vzscripts/github-runner.vzscript` into a long-lived
   cove macOS VM. The runner registers with `./svc.sh install` as a
   LaunchDaemon, persists across reboots, and accepts back-to-back jobs
   in the SAME guest.
2. Each job inherits residue from the previous job (per
   [015](015-soft-reset-empirical.md): 0 pass / 3 fail on warm-guest
   isolation probes — System Keychain, GlobalPreferences, orphan
   LaunchDaemons).
3. There is no fork-per-job path the user can opt into without writing
   custom orchestration on top of `cove image build` +
   `cove run -fork-from -ephemeral` themselves.

The desired state:

1. The user runs ONE command per job: `cove runner job --image
   cove-runner-macos:14.5 --repo tmc/cove --token <GH_PAT>`.
2. That command:
   a. acquires a JIT runner config from GitHub
      (`POST /repos/{owner}/{repo}/actions/runners/generate-jitconfig`);
   b. forks an ephemeral child from the named image via the existing
      `cove run -fork-from <ref> -ephemeral` codepath (subprocess);
   c. waits for the agent to come up in the child, writes the JIT token
      to a guest path via `WriteFile` RPC, then `Exec`s
      `./run.sh --jitconfig "$(cat /tmp/jit-config)"` and blocks until
      the runner exits;
   d. tears the child down on exit (success, runner failure, signal).
3. For multi-job behavior: shell loop. `while true; do cove runner job
   ...; done`. No daemon. No state files. No orphan-cleanup logic.
4. Every job sees a fresh guest. No residue. Throughput limited only by
   parent-image fork rate (~140 ms per fork-only on M4, per
   [bench/fork-time/results-20260427.md](../../bench/fork-time/results-20260427.md))
   plus boot-to-agent (~6-10 s on a vsock-reachable base image).

## Why this is its own design (not just 021)

Design 021's Slice 1 is `cove-gha-runner`: a thin GitHub Actions
**Action** wrapper invoked from `action.yml`. It expects to run as a
job step on an already-running runner. It does NOT provide the
runner-registration daemon itself.

Design 024 ships the image surface (`cove image build`, `fork-from
<image-ref>`, `-ephemeral`). It explicitly defers the "where does the
github-runner.vzscript live once images ship" question (open question
#4 in [024](024-cove-runner-images.md)).

This design closes the gap: a `cove runner` subcommand that wraps the
fork-per-job loop with GitHub's runner registration API, so users can
operate a Cirrus-tier ephemeral runner fleet on a single Mac without
writing orchestration glue.

## Lessons learned in v2

The v1 Slice 0 was vzscript-only. Three verified flaws:

1. **`-env` flag does not exist on `cove run`.** `grep '"-env"' main.go`
   → zero. The cookbook syntax `cove run -fork-from … -env JIT_CONFIG=…`
   was dead syntax.
2. **`-vzscripts` flag is install-only on `cove run`.** `main.go:622`
   wires `installVZScripts` only inside `case "install":`; `case "run":`
   silently ignores the flag value (`main.go:629`). The cookbook one-liner
   was broken on TWO counts, not one.
3. **`# runs-on: daemon` is sequential-blocking through `rsc.io/script`.**
   Confirmed at `vzscript_apply.go:810-816` (`cfgForRecipe` sets
   `cfg.daemon = true`) and `vzscript_apply.go:467-469` (`runVZScript`
   blocks synchronously on the goroutine). A `guest-exec ./run.sh
   --jitconfig` step would block the script engine indefinitely because
   `./run.sh --jitconfig` IS the runner listener loop. The existing
   `github-runner.vzscript:103` works because `./svc.sh install &&
   ./svc.sh start` creates a LaunchDaemon and returns immediately;
   launchd owns the listener process. JIT mode has no equivalent escape:
   GitHub's runner does not support service-mode + JIT config
   simultaneously.
4. **`# secret:` directive lives in `build_cache.go`, not
   `vzscript_apply.go`.** `parseBuildScriptMeta` recognizes
   `case "secret":` at `build_cache.go:123`; `parseScriptMeta` at
   `vzscript_apply.go:849-925` does not. `cove vzscript run` cannot
   mount secrets via `# secret:`. Task #74 was completed correctly for
   `cove build`; vzscript-side secret injection remains unimplemented.
5. **`AgentExecCommand.env` is never populated by vzscript.go.**
   `vzscript.go:479-483` constructs `AgentExecCommand{Args: args}` with
   no `Env` field. Per `project_vzscript_env_passthrough.md`, env
   pass-through is purely host-side substitution via `os.Environ()`
   before the args are sent. Any future "pass JIT_CONFIG through
   vzscript args" path would substitute base64 bytes into an unquoted
   token, which breaks on shell metachars (`+/=`).

v2's Slice 0 (`cove runner job`) sidesteps all five by living entirely
in Go — no script engine, no env substitution, no `# secret:` directive.
JIT bytes flow: HTTP response body → in-process `[]byte` →
`AgentClient.WriteFile()` (control-socket plumbing already shipped in
[023](023-cove-shell-exec-ux.md) Slice 1) → guest path → `bash -c
"./run.sh --jitconfig $(cat /tmp/jit-config)"`. The token never touches
a host shell, never appears in process args visible to `ps`, never
expands through `os.Environ()`.

**v2 round-2 review (2026-05-03)**: NotebookLM critique against the cove
design corpus (31 sources scoped) returned **SHIP-AS-IS-WITH-CQ-FIXES**.
The five follow-up questions surfaced two refinements folded into v2.5
patches below: (a) JIT-bytes write-then-exec leaves a 0600 file on disk
between RPCs — Slice 0 ships acceptable as-is, but `tmpfs` mount + the
v0.3 `ExecAttach` stdin pipe (per [023](023-cove-shell-exec-ux.md)) closes
the gap; (b) the image-residue mitigation now has its own §"Image
authoring contract" subsection so authors discover it without reading
the runtime-risks list. CQ3 (Slice 1 LOC budget) and CQ4 (failure-rule
addendum) were already in v2 and confirmed sound.

## Slices

### Slice 0 (v0.2.2 candidate, ~150-180 LOC Go): `cove runner job`

**Goal**: one-shot ephemeral runner command. Replaces v1's Slice 0
vzscript edit. Acquires a JIT config, forks the named image as
ephemeral, injects the token via in-process agent RPC, runs ONE job,
tears down. Exit code = runner exit code.

**Files**:

- `runner.go` (new, ~30 LOC) — subcommand dispatch (`cove runner
  <verb>`); v0.2.2 only registers `job`.
- `runner_job.go` (new, ~150 LOC) — full one-shot:
  ```
  parseFlags(--image, --repo, --token, --labels, --name, --workdir, --timeout)
  jitConfig, err := acquireJITConfig(ctx, repo, token, name, labels)
    → POST /repos/{owner}/{repo}/actions/runners/generate-jitconfig
    → returns 201 with { encoded_jit_config: "<base64>" }
  child := forkEphemeral(ctx, image)
    → exec.CommandContext("cove", "run", "-fork-from", image, "-ephemeral")
    → capture child VM name from structured stdout (NEW; see "open question 5")
    → spawn in goroutine; pipe stdout/stderr to host
  agent := waitForAgent(ctx, child, 60*time.Second)
    → poll control-socket until agent connect succeeds
  err = agent.WriteFile(ctx, "/tmp/jit-config", jitConfig, 0600)
  err = agent.Exec(ctx, []string{"bash", "-c",
        "./run.sh --jitconfig \"$(cat /tmp/jit-config && rm /tmp/jit-config)\""},
        timeout)
    → blocks until runner exits
  defer teardown(child) → cove vm delete <child>
  return runnerExitCode
  ```
- `runner_job_test.go` (new, ~70 LOC) — table-driven:
  - `TestRunnerJob_HappyPath` — stub HTTP server returns JIT config;
    stub `cove` on PATH echoes a fake child name; verify WriteFile +
    Exec invocations + teardown.
  - `TestRunnerJob_JITAcquireFailure` — stub returns 403; verify exit
    code 1, no fork attempt.
  - `TestRunnerJob_AgentTimeout` — agent never appears within 60s;
    verify teardown still runs, exit code 4.
- `main.go` (+10 LOC) — `case "runner":` → `handleRunnerCommand(args)`.
- `cli_help.go` (+5 LOC) — `cove runner` to subcommand index.
- `docs/reference/cli.md` (+15 LOC) — `cove runner job` reference.
- `docs/examples/ephemeral-github-runner.md` (new, ~40 LOC) — cookbook:

  ```
  # One-shot:
  cove runner job \
    --image cove-runner-macos:14.5 \
    --repo tmc/cove \
    --token "$GH_PAT" \
    --labels self-hosted,cove-vm,macos-14

  # Multi-job loop (replace svc.sh install pattern):
  while true; do
    cove runner job \
      --image cove-runner-macos:14.5 \
      --repo tmc/cove \
      --token "$GH_PAT" \
      --labels self-hosted,cove-vm,macos-14 \
      --timeout 6h
    sleep 2  # GitHub queue backoff
  done
  ```

**Why ship this first**:

- Atomic command, shell-loopable. The user's actual ask ("faster / more
  ephemeral") is met without a daemon.
- Sidesteps every v1-Slice-0 flaw by living in Go, not vzscript.
- Same LOC class as v1 (~150 vs ~80) but actually correct.
- Slice 1's daemon becomes a thin wrapper around the same primitive:
  `runner serve` is a goroutine pool of `runner job` invocations + a
  GitHub queue poller + state-file management. No re-architecture.

**Risks** (Slice 0 specific):

- JIT config TTL: GitHub's JIT config tokens expire ~60 seconds from
  the API call (not formally documented, observed in practice).
  Slice 0's full path (acquire → fork → boot → agent → exec) is
  8-12 s on a warm pre-baked image, comfortably inside the TTL.
  But: if the parent image needs cold-boot rather than fork (no
  pre-baked image yet), add 30-60 s — likely TTL violation.
- Subprocess control: `cove run -fork-from -ephemeral` currently does
  not emit structured stdout (child VM name, control-socket path).
  Slice 0 either adds a `--output-format json` flag to `cove run` (~30
  LOC), or scrapes the unstructured log lines (fragile). See open
  question 5.
- JIT-bytes window-of-disclosure: `WriteFile(/tmp/jit-config, bytes,
  0600)` then `Exec(bash -c "cat && rm")` leaves the file on disk for
  the round-trip duration (typically <100 ms). The guest is an
  ephemeral fork with only the runner user present, so 0600 + ephemeral
  context is acceptable for v0.2.2. **Tighter v0.3 path**: when
  `ExecAttach` lands (per [023](023-cove-shell-exec-ux.md) Slice 3 +
  v0.3 proto bump), pipe JIT bytes via stdin and skip the disk
  round-trip entirely. Alternative for v0.2.2 if disk-window is
  unacceptable: write to a `tmpfs` mount (per design [005](005-v04-secrets-architecture.md)
  / [021](021-v04-ci-executors-tracks.md) §secrets) so the bytes never
  hit the APFS-backed fork delta.

### Slice 1 (v0.3 candidate, ~250-300 LOC Go + tests): `cove runner serve`

**Goal**: long-running host-side daemon that polls GitHub's queue,
forks-per-job (one at a time), and tears down ephemeral children
automatically. Wraps Slice 0's `runner job` primitive in a poll loop +
state-file management.

**LOC budget revision from v1**: v1 estimated 350 LOC. The agent
review found that the "reuse `runImageForkFromWithConfig` directly"
claim is aspirational — that function reads package-level globals from
`flag.StringVar` (`runtime_lifecycle.go:225`, `main.go:210-213`), so
the only safe library call is subprocess. Real budget: 250-300 LOC for
the daemon proper, since Slice 0 already absorbs the
subprocess-management LOC.

**Files**:

- `runner.go` (modify, +30 LOC) — register `serve` verb alongside `job`.
- `runner_serve.go` (new, ~200 LOC) — main loop:
  1. Poll `GET /repos/{owner}/{repo}/actions/runs?status=queued` (or use
     long-poll registration if available).
  2. When a job is queued for one of our labels, call into
     `runOneJob(ctx, image, repo, token)` — same code path as Slice 0
     but as a library function, not the CLI handler.
  3. State file: `~/.vz/runners/<runner-name>/state/<job-id>.json` —
     atomic temp+rename writes per `project_lro_pattern.md` (design 001
     LRO precedent: `~/.vz/operations/<op_id>.json`).
  4. SIGTERM handling: drain currently-running fork to completion
     (configurable `--drain-timeout`, default 10m), then exit.
- `runner_serve_test.go` (new, ~80 LOC) — table-driven (see Tests below).
- `cli_help.go` (modify, +5 LOC) — extend `cove runner` help with
  `serve`.
- `docs/reference/cli.md` (modify, +20 LOC) — `cove runner serve`
  reference.

**Concurrency model**: Slice 1 = ONE fork at a time. The host process
serializes fork attempts. Slice 2 adds a `--max-parallel N` flag.

**State on disk**:

- `~/.vz/runners/<runner-name>/config.json` — image ref, repo, token
  reference (URI per design 005, NOT the literal token), labels,
  max-parallel.
- `~/.vz/runners/<runner-name>/state/<job-id>.json` — per-job state
  (started_at, child_vm_name, exit_code, finished_at). Atomic
  temp+rename.

**Failure invariants** (faithful inheritance from design 021
[§"Failure rules" lines 149-163](021-v04-ci-executors-tracks.md)):

| Failure mode | Slice 1 behavior | Source |
|---|---|---|
| JIT acquisition fails | log + exponential backoff (1s, 2s, 4s, … cap 60s); do NOT spawn a fork | 026-only (021 assumes pre-job setup is platform-managed) |
| Fork failure (parent missing, APFS clonefile, scratch root unwritable) | bail BEFORE guest boot; error names parent VM + scratch path; no partial state | 021 §rule 2 |
| Secret mount failure (JIT WriteFile RPC fails) | abort with exit 2, surfacing error from agent; do NOT fall back to env vars | 021 §rule 3 — **was missing in v1** |
| Job command exits non-zero | wrapper exits non-zero, forwarding runner exit code | 021 §rule 1 — **was missing in v1** |
| Timeout (per-job, default 6h) | SIGTERM the guest exec, wait min(10s, remaining_timeout), then `cove ctl shutdown -force`; always run teardown | 021 §rule 4 |
| Teardown failure (`cove vm delete <child>` fails post-job) | log + exit non-zero EVEN IF the job itself succeeded; the next run cannot rely on a clean parent | 021 §rule 5 — **was missing in v1** |
| Agent disconnect mid-job | terminate the fork (`cove ctl shutdown -force`), mark job failed in state file, continue serving (in serve mode) or exit non-zero (in job mode) | 026-only |
| Host process exits cleanly (SIGTERM) | drain currently-running fork to completion (`--drain-timeout`, default 10m), then exit | 026-only |
| Host process crashes | on next start, scan `~/.vz/runners/<name>/state/` for orphan VMs and clean them up via `cove vm delete <child>` before accepting new jobs | 026-only |
| Cancelled-job mechanics | If a fork fails before the runner registers, GitHub has no runner for that job — the job stays queued until GitHub's default 6h timeout expires. There is no in-band "job cancelled" signal we can send. Document this plainly in the Slice 1 cookbook. | 026-only — was glossed as "(best-effort)" in v1; now stated plainly |

**Tests** (in addition to per-function):

- `TestRunnerServe_HappyPath` — fake GitHub queue, stub `cove` PATH,
  verify one full cycle.
- `TestRunnerServe_OrphanCleanupOnRestart` — leave a fake orphan VM in
  state dir; restart; verify cleanup before next loop iteration.
- `TestRunnerServe_TimeoutForcesShutdown` — fork that doesn't exit;
  verify `cove ctl shutdown -force` invocation after the configured
  timeout.
- `TestRunnerServe_TeardownFailureExitsNonZero` — Slice 1's faithful
  inheritance from 021 §rule 5; stub `cove vm delete` to return error;
  verify daemon exits non-zero even though the job succeeded.
- `TestRunnerServe_DrainOnSIGTERM` — start with one in-flight job, send
  SIGTERM, verify daemon waits for fork exit before shutting down.

### Slice 2 (v0.4 candidate, ~150 LOC): parallel fan-out + observability

**Goal**: scale Slice 1 from 1 → N concurrent forks per host with
backpressure, Prometheus metrics, and a `cove runner status` query
command.

- `--max-parallel N` flag — semaphore-gated fork attempts.
- `--prom-port N` flag — exposes job-counter, fork-time-histogram,
  active-job gauge metrics on `0.0.0.0:N/metrics`.
- `cove runner status` subcommand — reads
  `~/.vz/runners/<name>/state/` + queries the live host process (vsock
  or unix socket TBD) for in-flight jobs.
- Reuses Slice 1's failure invariants per-fork.

### Slice 3 (deferred): GitLab parity

Mirror Slice 1 for GitLab self-hosted runners. GitLab uses
`gitlab-runner register --token <runner-token>` not JIT configs; needs
its own registration shim. Out of scope until GitHub Slice 1+2 are
proven; tracked in [021](021-v04-ci-executors-tracks.md) Slice 2 layer.

## Differentiator vs Cirrus

Cirrus's [`gitlab-tart-executor`](https://github.com/cirruslabs/gitlab-tart-executor)
runs as a GitLab Custom Executor: GitLab calls into it per job, it
spawns a `tart clone` + `tart run`, the runner inside the clone picks
up the job, GitLab tears down. Same shape as our Slice 1, but tied to
GitLab's executor protocol.

Our Slice 0 (`cove runner job`) is the smallest unit — a one-shot fork
that no Tart/Lume equivalent ships. Tart users self-hosting GitHub
runners must script their own `tart clone` + `actions-runner
--jitconfig` glue. Lume users have no fork primitive at all. Our Slice
1 (`cove runner serve`) sits on the GitHub side (where Cirrus has
Cirrus CI itself as the alternative, not a self-hosted-runner fork)
and ships the host-daemon shape that no Cirrus equivalent provides for
GitHub Actions. Combined with [015](015-soft-reset-empirical.md)'s
empirical basis for fork/restore being the only working isolation
primitive, this is a measurable safety + perf claim no competitor
matches.

## Top runtime risks (NEW in v2 — not in v1)

These show up when the user actually runs this in anger, not in design
review:

1. **JIT config TTL expiration before fork boots** (Slice 0 risk
   already noted; Slice 1 risk is worse with parallel forks competing
   for boot bandwidth). Mitigations: prefer warm pre-baked images;
   acquire JIT token AT fork-time, not at job-dispatch-time; document
   TTL probe in cookbook.
2. **Runner binary version in the baked image goes stale.** GitHub's
   JIT mode does NOT auto-update the runner binary (auto-update
   requires the service-mode lifecycle). A pre-baked image captures
   one runner version. After ~3 months, GitHub will reject the runner
   as too old and `./run.sh --jitconfig` will fail. The existing
   `github-runner.vzscript:69-75` avoids this by fetching the latest
   runner at registration time — but that's incompatible with
   pre-baked images. **Mitigation**: document a periodic `cove image
   build` schedule in the cookbook; flag in `cove image inspect` if
   the runner binary inside is older than 90 days (future v0.4 work).
3. **Residue in keychain and LaunchDaemon slots between ephemeral
   forks from the SAME parent.** Design 015 proved warm soft-reset
   fails 3 of 6 isolation probes. Fork-from IS the right isolation
   primitive — but only if the parent image is built from a clean,
   never-job-run snapshot. See §"Image authoring contract" below for
   the cleanup checklist this design REQUIRES of any pre-baked runner
   image.

## Image authoring contract (load-bearing)

Pre-baked runner images that will serve as fork-parents for `cove
runner job` MUST be built from a clean snapshot. Design 015 measured
0/3 pass on warm-guest isolation probes (System Keychain,
GlobalPreferences, orphan LaunchDaemons) without a fresh
fork+restore. Forks inherit every byte of the parent's delta state,
including:

- **Registered runners** (`~/.runner` state directory, GitHub-side
  registration entries that reuse the same machine fingerprint).
- **Keychain entries** captured during runner registration
  (`./config.sh` writes credentials to System Keychain).
- **Loaded LaunchDaemons** (`./svc.sh install` registers a
  per-user LaunchDaemon that persists across forks).
- **Last-shell history files** that may contain `GH_PAT` / `GH_TOKEN`
  exports.
- **Cached HTTP credentials** in `~/.config/gh`, `~/.git-credentials`,
  `~/Library/Caches/com.github.runner`.

The image-build cookbook MUST run, before `cove image build`:

```bash
# Inside the VM that will be sealed as the runner image:
./config.sh remove --token "$GH_REMOVE_TOKEN"   # Drops ~/.runner + GH-side registration
./svc.sh uninstall                              # Drops the LaunchDaemon plist
sudo security delete-certificate -c "GitHub Actions" /Library/Keychains/System.keychain || true
rm -rf ~/.config/gh ~/.git-credentials ~/Library/Caches/com.github.runner
rm -f ~/.zsh_history ~/.bash_history /private/var/log/system.log*
sudo rm -rf /tmp/* /private/var/folders/*/T/*
```

**Future automation** (v0.4 work, NOT in this design's slices): a
`cove image build --strict-clean` flag that automates these checks
and refuses to seal an image with detectable runner residue. Until
that ships, the cookbook is the contract.

Without this vacuum, fork residue carries between jobs and silently
breaks the per-job isolation guarantee that motivates the entire
design.

## Privacy and trademark gates

- All work in this design is LOCAL. No registry pushes. No
  public-facing surface beyond what GitHub already exposes for
  self-hosted runners.
- Slice 1+2 ship under the same private-repo regime as the rest of
  v0.3.
- No `cove` brand surface that hits a public registry until trademark
  counsel clears the name (per ROADMAP `Product Decisions`).

## Open questions

1. **GitHub App vs PAT for JIT acquisition**: PAT is simpler for
   Slice 0 cookbook; GitHub App is required for org-wide deployments
   (avoids per-user token expiry). Strawman: support both via
   `GH_TOKEN` (PAT path) OR `GH_APP_ID`+`GH_APP_PRIVATE_KEY` env vars
   (App path). Decide in Slice 1 implementation. **Lean (per agent
   review)**: route both through design 005's URI delegation
   (`1password://`, `env://`) rather than building token-refresh
   lifecycles into the runner daemon itself.
2. **State store format**: JSON files per job is the simplest shape
   but doesn't survive concurrent writes well. Strawman: one JSON
   per job_id, write atomically via temp+rename, never modify in
   place. **Confirmed**: matches design 001 LRO pattern at
   `~/.vz/operations/<op_id>.json`. Use that exact shape.
3. **Slice 1 vs `cove-gha-runner` from 021**: 021 Slice 1 is a GHA
   *Action* (job-step). This design's Slice 1 is a GHA *runner*
   (host daemon). They compose: 021 runs INSIDE jobs that arrive
   via the runner this design serves. No conflict, but they MUST
   be released in the right order: this design's Slice 0/1 (runner)
   before 021's Slice 1 (action), since the action needs a runner
   to run on.
4. **Where does `vzscripts/github-runner.vzscript` go after Slice 0
   ships**: keep it. The vzscript is the "I just want a long-lived
   runner inside a permanent VM" path; `cove runner job` is the "I
   want fork-per-job" path. Both are valid. Document the trade-off
   in the cookbook.
5. **Structured stdout from `cove run -fork-from -ephemeral`**: Slice
   0's subprocess management needs the child VM name (and ideally the
   control-socket path) emitted in machine-parseable form. Today the
   logs are unstructured. Options:
   - **5a**: Add `cove run --output-format json` flag (~30 LOC change
     to `runtime_lifecycle.go`). Emits `{"event":"vm_started","name":
     "...","control_socket":"..."}` lines on stderr. Cleanest.
   - **5b**: Slice 0 scrapes existing log lines. Fragile; will break
     on any log-format change.
   - **5c**: Slice 0 uses `cove vm list --json` polling after fork
     (find the newest `<parent>-fork-N` VM). Race-prone but no
     subprocess coupling.
   - **5d**: Pre-allocate the child name in Slice 0 and pass via `cove
     run -fork-from <img> -ephemeral -name <slice-allocated>`. The
     `EphemeralForkName` field already exists at
     `runtime_lifecycle.go:45` and is plumbed through to
     `image_fork.go:51` and `run_bundle.go:334`. So the wiring is
     ~5 LOC. Downside: doesn't establish reusable structured-output
     infra that future commands (cove image inspect, cove vm tree,
     etc.) would benefit from.
   **Lean**: 5a. Adds reusable structured-output infrastructure that
   future commands can adopt; 5d is a viable fallback if
   `--output-format json` slips out of Slice 0 scope.

## References

- [001](001-cove-serve.md) — LRO state file pattern at
  `~/.vz/operations/<op_id>.json`. Slice 1's state store mirrors this
  exactly.
- [005](005-uri-delegation.md) — URI-based secret delegation. Slice 1's
  `--token` flag should accept URIs (e.g. `1password://op/cove/gh-pat`).
- [013](013-vm-fork.md) — fork-from semantics, Phase 4 lineage.
- [015](015-soft-reset-empirical.md) — load-bearing for the
  "fork/restore is the only isolation primitive" claim.
- [021](021-v04-ci-executors-tracks.md) — v0.4 CI executors. This
  design ships the runner daemon; 021 Slice 1 ships the action that
  runs ON that daemon. Failure rules at lines 149-163 are the
  authoritative source for v2's Slice 1 failure-invariant table.
- [023](023-cove-shell-exec-ux.md) — `cove shell` exec UX. Slice 0
  reuses the `agent-exec-attach` plumbing (`AgentClient.WriteFile` +
  `AgentClient.Exec`) shipped in 023 Slice 1.
- [024](024-cove-runner-images.md) — image surface (`cove image
  build`, `fork-from <ref>`, `-ephemeral`). Slice 0 of this design
  composes on top of 024's already-shipped Slice 1.
- [025](025-cove-action-security.md) — security architecture.
  Token-handling rules apply unchanged: the runner NEVER mounts the
  JIT config or `GH_PAT` as a guest env var; injection happens via
  agent RPC, and the in-guest runner consumes the JIT config from a
  guest path that is `rm`'d in the same `bash -c` invocation that
  reads it.
- [`vzscripts/github-runner.vzscript`](../../vzscripts/github-runner.vzscript)
  — the existing manual-runner script. Stays in tree as the
  long-lived alternative to `cove runner job`.

[gh-jit]: https://docs.github.com/en/rest/actions/self-hosted-runners?apiVersion=2022-11-28#create-configuration-for-a-just-in-time-runner-for-a-repository
