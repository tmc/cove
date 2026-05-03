# Custom ephemeral self-hosted runners

**Status**: planning input. Owns the bridge between the existing
long-lived `vzscripts/github-runner.vzscript` (registration-mode runner
inside a permanent VM) and the v0.4 `cove-gha-runner` GHA wrapper from
design [021](021-v04-ci-executors-tracks.md). This doc covers the
slices that ship BEFORE 021's full GHA wrapper lands, so users with a
working manual runner today can switch to fork-per-job ephemeral runners
without waiting on v0.4.

**Scope-source**: user prompt 2026-05-03 — "I have a VM that has manual
runners running in it right now but would like to get it to be faster /
more ephemeral." This is the on-ramp design 021's Slice 1 + design 024's
Slice 1 collectively imply but do not document end-to-end.

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

1. The user runs ONE command per CI host: `cove runner serve --image
   cove-runner-macos:14.5 --repo tmc/cove --token <reg-token>`.
2. The host process polls the GitHub Actions queue API or registers a
   long-poll listener. When a job is dispatched, the host:
   a. forks a fresh ephemeral child from `cove-runner-macos:14.5` via
      the existing `cove run -fork-from <ref> -ephemeral` codepath;
   b. injects a JIT runner config into the child;
   c. waits for the runner inside the child to claim and execute the
      job;
   d. destroys the child when the job completes (or hits timeout /
      crashes).
3. Every job sees a fresh guest. No residue. Throughput limited only by
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

## Slices

### Slice 0 (v0.2.2 candidate, ~80 LOC vzscript-only): ephemeral runner mode

**Goal**: extend `vzscripts/github-runner.vzscript` with an `--ephemeral`
mode that uses GitHub's [JIT runner config][gh-jit] instead of
persistent registration. Lets users with TODAY'S long-lived VM script
switch to single-use registration WITHOUT requiring `cove runner serve`.

**Files**:

- `vzscripts/github-runner.vzscript` (modify) — add `JIT_CONFIG` env
  var path; if set, `./run.sh --jitconfig "$JIT_CONFIG"` instead of
  `./svc.sh install`. ~30 LOC change to `install-runner.sh`.
- `docs/examples/ephemeral-github-runner.md` (new) — cookbook walking
  through the fork-once flow:
  ```
  TOKEN=$(curl -X POST -H "Authorization: bearer $GH_PAT" \
    https://api.github.com/repos/tmc/cove/actions/runners/generate-jitconfig \
    -d '{"name":"cove-fork-1","runner_group_id":1,"labels":["cove-vm"],"work_folder":"_work"}' \
    | jq -r '.encoded_jit_config')

  cove run -fork-from cove-runner-macos:14.5 -ephemeral \
    -vzscripts github-runner -env JIT_CONFIG="$TOKEN"
  ```

**Why ship this first**: zero Go code, no new subcommand, no proto bump.
Unlocks the core ephemeral pattern for users (you) who already have a
manual runner working. Pure additive change to an existing vzscript.

**Risks**: GitHub's JIT config API is in public beta as of 2025-09;
need to verify endpoint shape against current docs before shipping.

### Slice 1 (v0.3 candidate, ~350 LOC + tests): `cove runner serve`

**Goal**: a long-running host-side daemon that polls GitHub's queue,
forks-per-job, and tears down ephemeral children automatically.

**Files**:

- `runner.go` (new) — subcommand dispatch, config parsing, signal
  handling. ~80 LOC.
- `runner_serve.go` (new) — main loop:
  1. Acquire a JIT config from GitHub (using `GH_PAT` or GitHub App
     credentials).
  2. Spawn an ephemeral fork via the existing `cove run -fork-from
     <image> -ephemeral` codepath (re-use, do not re-implement the fork
     logic).
  3. Inject the JIT config via the in-VM agent's `AgentExecCommand`
     RPC (existing — see `proto/agent.proto`), with the job's working
     directory mounted via VirtioFS share.
  4. Wait for the fork to terminate (job done, agent disconnect, or
     timeout).
  5. Loop. ~200 LOC.
- `runner_serve_test.go` — table-driven tests for: JIT acquisition
  failure, fork failure (parent missing / clonefile fails), agent
  disconnect mid-job, timeout-then-shutdown. ~70 LOC.
- `cli_help.go` (modify) — add `cove runner` to the subcommand index.
- `docs/reference/cli.md` (modify) — `cove runner serve` reference.

**Concurrency model**: Slice 1 = ONE fork at a time. The host process
serializes fork attempts. Slice 2 adds a `--max-parallel N` flag.

**State on disk**:

- `~/.vz/runners/<runner-name>/config.json` — image ref, repo, token,
  labels, max-parallel.
- `~/.vz/runners/<runner-name>/state/<job-id>/` — per-job log + child
  VM name (for cleanup if the host crashes mid-job).

**Failure invariants** (mirror design 021 §"Failure rules"):

- JIT acquisition fails → log + retry with backoff; do NOT spawn a
  fork.
- Fork fails → fail-fast, log the parent VM name + error, surface to
  the GitHub side as job-cancelled (best-effort).
- Agent disconnect mid-job → terminate the fork (`cove ctl shutdown -force`),
  mark the job as failed in local state, continue serving.
- Host process exits cleanly (SIGTERM) → drain currently-running fork
  to completion (configurable timeout, default 10m), then exit.
- Host process crashes → on next start, scan `~/.vz/runners/<name>/state/`
  for orphan VMs and clean them up via `cove vm delete <child>` before
  accepting new jobs.

**Tests** (in addition to per-function):

- `TestRunnerServe_HappyPath` — fake GitHub queue, stub `cove run -fork-from`,
  verify one full cycle.
- `TestRunnerServe_OrphanCleanupOnRestart` — leave a fake orphan VM in
  state dir; restart; verify cleanup before next loop iteration.
- `TestRunnerServe_TimeoutForcesShutdown` — fork that doesn't exit;
  verify `cove ctl shutdown -force` invocation after the configured
  timeout.

### Slice 2 (v0.4 candidate, ~150 LOC): parallel fan-out + observability

**Goal**: scale Slice 1 from 1 → N concurrent forks per host with
backpressure, Prometheus metrics, and a `cove runner status` query
command.

- `--max-parallel N` flag — semaphore-gated fork attempts.
- `--prom-port N` flag — exposes job-counter, fork-time-histogram,
  active-job gauge metrics on `0.0.0.0:N/metrics`.
- `cove runner status` subcommand — reads `~/.vz/runners/<name>/state/`
  + queries the live host process (vsock or unix socket TBD) for
  in-flight jobs.
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

Our Slice 1 sits on the GitHub side (where Cirrus has Cirrus CI itself
as the alternative, not a self-hosted-runner fork) and ships the
host-daemon shape that no Cirrus equivalent provides for GitHub
Actions. Combined with [015](015-soft-reset-empirical.md)'s empirical
basis for fork/restore being the only working isolation primitive, this
is a measurable safety + perf claim no competitor matches:

- Tart users self-hosting GitHub runners must script their own
  `tart clone` + `actions-runner --jitconfig` glue. No `tart runner serve`.
- Lume users have no fork primitive at all.

## Privacy and trademark gates

- All work in this design is LOCAL. No registry pushes. No public-facing
  surface beyond what GitHub already exposes for self-hosted runners.
- Slice 1+2 ship under the same private-repo regime as the rest of v0.3.
- No `cove` brand surface that hits a public registry until trademark
  counsel clears the name (per ROADMAP `Product Decisions`).

## Open questions

1. **GitHub App vs PAT for JIT acquisition**: PAT is simpler for v0.2.2
   Slice 0 cookbook; GitHub App is required for org-wide deployments
   (avoids per-user token expiry). Strawman: support both via
   `GH_TOKEN` (PAT path) OR `GH_APP_ID`+`GH_APP_PRIVATE_KEY` env vars
   (App path). Decide in Slice 1 implementation.
2. **State store format**: JSON files per job is the simplest shape but
   doesn't survive concurrent writes well. Strawman: one JSON per
   job_id, write atomically via temp+rename, never modify in place.
3. **Slice 1 vs `cove-gha-runner` from 021**: 021 Slice 1 is a GHA
   *Action* (job-step). This design's Slice 1 is a GHA *runner* (host
   daemon). They compose: 021 runs INSIDE jobs that arrive via the
   runner this design serves. No conflict, but they MUST be released
   in the right order: this design's Slice 1 (runner) before 021's
   Slice 1 (action), since the action needs a runner to run on.
4. **Where does `vzscripts/github-runner.vzscript` go after Slice 1
   ships**: keep it. The vzscript is the "I just want a long-lived
   runner" path; `cove runner serve` is the "I want fork-per-job"
   path. Both are valid. Document the trade-off in the cookbook.

## References

- [013](013-vm-fork.md) — fork-from semantics, Phase 4 lineage.
- [015](015-soft-reset-empirical.md) — load-bearing for the
  "fork/restore is the only isolation primitive" claim.
- [021](021-v04-ci-executors-tracks.md) — v0.4 CI executors. This
  design ships the runner daemon; 021 Slice 1 ships the action that
  runs ON that daemon.
- [023](023-cove-shell-exec-ux.md) — `cove shell` exec UX. The runner
  daemon reuses the same `agent-exec-attach` plumbing for in-fork
  command execution.
- [024](024-cove-runner-images.md) — image surface (`cove image build`,
  `fork-from <ref>`, `-ephemeral`). Slice 1 of this design composes
  on top of 024's already-shipped Slice 1.
- [025](025-cove-action-security.md) — security architecture.
  Token-handling rules apply unchanged: the runner daemon NEVER mounts
  the JIT config or `GH_PAT` as a guest env var; injection happens via
  agent RPC after the fork is up, and the in-guest runner consumes the
  JIT config from a tmpfs path that the host unmounts before declaring
  the job done.
- [`vzscripts/github-runner.vzscript`](../../vzscripts/github-runner.vzscript)
  — the existing manual-runner script that Slice 0 augments with
  `--ephemeral` mode.

[gh-jit]: https://docs.github.com/en/rest/actions/self-hosted-runners?apiVersion=2022-11-28#create-configuration-for-a-just-in-time-runner-for-a-repository
