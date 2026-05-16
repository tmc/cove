# v0.4 CI executors (GitHub Actions, GitLab)

**Status**: GitHub Actions executor surface shipped on 2026-05-05. Docs
landed at `19804c7`; implementation shipped at `0985377`, `8bd473e`,
`82a0ac5`, `7fafe40`, `9e6253a`, `f06d554`, and `c0a1433`. GitLab
shell-runner shim remains unshipped.
**Source**: `/tmp/cove-v04-audit-a4k2.md` (audit at `4511a60`), plus local
review of the v0.3 build executor (`build.go`, `build_execute.go`,
`build_scratch.go`, `fork.go`) and the OpenAI Agents SDK adapter at
`adapters/openai-agents-python/` (layout under `src/cove_sandbox/`).
**Roadmap**: v0.4. Roadmap section is currently absent from
[ROADMAP](../ROADMAP.md); this doc plus [022](022-v04-anthropic-adapter.md)
are intended to populate it.
**Branch**: planning (no branch yet).

## Goal

cove provides a CI executor abstraction that runs jobs in cove-managed
VMs across GitHub Actions and GitLab CI. Each executor is a
thin wrapper around the v0.3 primitives — `cove run -fork-from`, the
per-VM control socket, the guest agent gRPC — and reuses the OpenAI
adapter's "client lives outside the cove runtime" shape. New code in
v0.4 is wrapper code only; no Virtualization.framework changes, no
build-executor changes, no new agent RPCs.

## Mental model

```text
CI runner ──► executor wrapper ──► cove run -fork-from
(GHA /         (action / shim                  │
 GitLab)        entrypoint)                    ▼
                        │                scratch VM fork
                        │ control.sock          │
                        └──────────────►  guest agent
                                                │
                                                ▼
                                          job + diff + teardown
```

The shared isolation primitive is `cove run -fork-from` from
design 013 (shipped 2026-05-01 at `99b3732`). Per-job isolation is the
fork; per-job teardown is the existing scratch cleanup. No new
isolation mechanism is introduced.

## Why it's feasible

- `cove run -fork-from` already gives one-shot, COW-cloned scratch VMs
  with deterministic teardown (`fork.go`, `vm_tree.go`).
- The control socket (`control_socket.go`) accepts JSON commands over
  `~/.vz/vms/<name>/control.sock` for agent-exec, screenshot, snapshot,
  and shutdown. Wrappers reuse this surface as-is.
- The vsock guest agent (`cmd/vz-agent`, `proto/agent.proto`) exposes
  exec/cp/write/shell RPCs that cover "run a job script and collect
  artifacts".
- The OpenAI adapter at `adapters/openai-agents-python/src/cove_sandbox/`
  proves the pattern: an out-of-tree client package wraps `cove run` and
  the control socket, owns its own packaging metadata
  (`adapters/openai-agents-python/pyproject.toml`), and depends only on
  the cove binary plus its public socket protocol. CI executors follow
  the same shape.
- Secrets resolution will reuse [005](../005-v04-secrets-architecture.md)
  URI delegation once that lands; v0.4 CI executors do not invent a
  parallel secret mechanism.

## Architecture

### Common pieces

A small Go package under `adapters/ci-executors/executor/` defines:

- `JobConfig` — base image, vzscript, commands, secret refs, env,
  artifact globs, timeout. Loaded from `cove-job.yaml` at the repo root
  so the same descriptor drives both platforms.
- `DispatchResult` — layer digest (or empty for ephemeral jobs), log
  path, exit code, duration, artifact paths.
- `Executor` interface with one method, `Dispatch(ctx, JobConfig)
  (DispatchResult, error)`.

Schema mirrors existing build-plan inputs (base, vzscript, secrets)
rather than introducing a parallel grammar.

### Per-platform shims

| Platform        | Wrapper shape                                        | Distribution           |
|-----------------|------------------------------------------------------|------------------------|
| GitHub Actions  | `action.yml` composite action wrapping a Go binary   | GitHub Marketplace     |
|                 | from `cmd/cove-gha-runner/`.                         |                        |
| GitLab CI       | Shell-runner shim invoked from `.gitlab-ci.yml`;     | OCI image + raw binary |
|                 | no Custom Executor protocol in v0.4.                 |                        |

Both shims invoke the same `executor.Dispatch` and translate
platform-specific I/O at the edges.

## Slice 1: GitHub Actions executor (~600 LOC)

Layout under `adapters/ci-executors/github-actions/`: `action.yml`,
`cmd/cove-gha-runner/`, `examples/workflow.yml`, `README.md`.

Inputs (`action.yml`): `base-image` (required), `vzscript`, `commands`,
`secrets` (multiline URIs resolved per
[005](../005-v04-secrets-architecture.md), mounted as tmpfs under
`/run/cove/secrets/<name>` matching the existing `# secret:` rule in
[003](003-cove-build-oci-caching.md)), `artifacts` (multiline globs),
`timeout` (default `30m`).

Outputs: `digest`, `log-path`, `exit-code`.

Tests:

- `TestGHARunnerHappyPath`: feeds a fake GHA env (`GITHUB_*`) and a
  stub `cove` binary that records its argv; verifies the wrapper builds
  the right fork command and writes the right outputs file.
- `TestGHARunnerSecretMountFailure`: stub `cove` exits non-zero before
  guest boot; wrapper must fail with a clear error and not write
  `digest`.
- `TestGHARunnerArtifactsCollected`: stub `cove` writes files via
  shared folder; wrapper archives them and reports `artifacts`.

No live GHA calls. The fake-runner pattern follows the `act`-style
contract: set `GITHUB_OUTPUT`, `GITHUB_WORKSPACE`, etc., to temp paths
and assert on the resulting files.

## Slice 2: GitLab shell-runner shim (~300 LOC)

Layout under `adapters/ci-executors/gitlab/`:
`cmd/cove-gitlab-runner/`, `examples/.gitlab-ci.yml`, `Dockerfile`,
`README.md`.

Invocation in user `.gitlab-ci.yml`:

```yaml
cove-build:
  image: ghcr.io/tmc/cove-gitlab-runner:v0.4
  script:
    - cove-gitlab-runner --config cove-job.yaml
```

The shim reuses the shared `executor` package. GitLab-specific
concerns: read `CI_JOB_TOKEN` and `CI_PROJECT_DIR`; line-buffer
stdout/stderr so the web UI updates in real time; surface artifacts
under `$CI_PROJECT_DIR/cove-artifacts/` so users wire them up via
GitLab's `artifacts:` directive.

Custom Executor protocol (with `prepare`, `run`, `cleanup` stages) is
out of scope for v0.4.

Tests: `TestGitLabRunnerParsesEnv`, `TestGitLabRunnerStreamsStdout`,
`TestGitLabRunnerArtifactsLayout`.

## Failure rules

- Job command exits non-zero → wrapper exits non-zero, forwarding the
  exit code where the platform allows.
- Fork failure (parent VM missing, APFS clonefile fails, scratch root
  unwritable) → bail before any guest boot, with an error that names
  the parent VM and scratch path. No partial state.
- Secret mount failure → abort with exit code `2`, surfacing the URI
  scheme and resolution error from the
  [005](../005-v04-secrets-architecture.md) resolver. Do not fall back to
  unmounted env vars.
- Timeout → SIGTERM the guest exec, wait `min(10s,
  remaining_timeout)`, then `cove ctl shutdown -force`. Always run
  teardown.
- Teardown failure → log and exit non-zero even if the job itself
  succeeded; the next run cannot rely on a clean parent.

## Tests

Per-slice tests are itemized above. Cross-cutting:

- `TestExecutorDispatchCommonContract`: every platform shim, fed the
  same `JobConfig`, produces the same sequence of `cove` subprocess
  invocations (asserted via a stub `cove` on `PATH`).
- `TestSecretResolutionViaSlice005`: stub the URI resolver and verify
  wrappers route through it instead of reading env vars directly.
- `TestExecutorSchemaRoundtrip`: `cove-job.yaml` round-trips through
  `JobConfig` with no field loss.

No real CI calls. No real registry pushes. No real secret stores. All
external surfaces are stubbed.

## Non-goals

- No cross-arch builds. v0.4 ships Apple Silicon (arm64) macOS and
  Linux guest support only.
- No multi-VM jobs. Slice 1 = single fork per job.
- No GitLab Custom Executor protocol. Shell-runner shim only in v0.4.
- No registry-base execution. Each executor consumes a local cove
  VM/image by name; pulling from an OCI registry is gated on
  [002](002-cove-disks-oci.md) follow-up work.
- No new secret mechanism. Reuse
  [005](../005-v04-secrets-architecture.md).
- No log streaming protocol beyond line-buffered stdout. Real-time UI
  rendering is the platform's job.

## Acceptance gates

Each slice has its own gate; ship one at a time.

- **Slice 1 (GHA)**: `examples/workflow.yml` runs locally via `act`
  (or the equivalent fake-runner harness), builds a tiny image with a
  one-step vzscript, exits 0, and emits `digest` and `log-path`
  outputs that match the wrapper contract. Integration test in CI.
- **Slice 2 (GitLab)**: a sample `.gitlab-ci.yml` plus a stubbed `cove`
  on PATH runs the shim end-to-end inside the published Docker image;
  artifacts land under the documented path; integration test asserts
  on artifact contents.

Cross-slice gate (one-time, before declaring v0.4 done): both shims
dispatch the same trivial job (a vzscript that installs `cowsay` and
runs it) and produce identical exit codes, log output, and artifact
digests modulo timestamps.

## Open questions

1. **Log streaming parity**. GitHub Actions buffers per-step output;
   GitLab streams in real time. Accept the UX gap, or invest in a
   chunked-progress shim for GHA in v0.4? Recommendation: accept the
   gap; revisit if users complain.
2. **Secret precedence**. If a job declares `secrets: op://foo/bar`
   *and* the CI platform also injects an env var named `FOO_BAR`,
   which wins? Recommendation: cove-resolved URIs always win;
   document loudly. Needs sanity check against
   [005](../005-v04-secrets-architecture.md) once that doc has Council
   sign-off.
3. **Distribution channel for the GitLab Docker image**. GHCR vs
   Docker Hub vs GitLab's own registry. Recommendation: GHCR, single
   source of truth alongside cove's release artifacts.

## Handoff

Once Slice 1 is in flight, [022](022-v04-anthropic-adapter.md)
(parallel track) and [005](../005-v04-secrets-architecture.md) are
unblocked. ROADMAP.md should grow a v0.4 section that links here and
to 022 once both designs are accepted.
