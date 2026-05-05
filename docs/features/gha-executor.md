---
title: GitHub Actions Executor
---
# GitHub Actions Executor

The GitHub Actions executor is an internal, private action wrapper for running
one GitHub Actions job inside a fresh cove fork. It is Slice 1 of the v0.4 CI
executor track.

This is not a Marketplace action yet. Do not publish it to GitHub Marketplace or
document it as public installation surface until the repository, image channel,
and action packaging decisions are made.

## Model

Each action invocation starts one disposable VM fork from a local cove image:

```text
GitHub runner -> cove action wrapper -> cove run -fork-from <image> -ephemeral
                                      -> guest agent exec
                                      -> artifacts and teardown
```

The parent image is never used as the runner. The job runs in a copy-on-write
fork, and the fork is destroyed after the job. This is the isolation boundary;
soft reset is not used for CI jobs.

## Inputs

The private action accepts these inputs:

| Input | Required | Default | Description |
|---|---:|---|---|
| `image` | yes |  | Local cove image to fork for the job. |
| `command` | no |  | Single shell command to execute in the guest. |
| `args` | no |  | Alias for `command`. |
| `script` | no |  | Multiline shell script to execute in the guest. Overrides `command` and `args`. |
| `env` | no |  | Multiline `KEY=VALUE` entries injected into the guest command environment. |
| `secrets` | no |  | Reserved for cove secret URI mounting in a later slice. Non-empty input fails. |
| `timeout` | no | `30m` | Maximum runtime for provisioning and commands. |
| `cove-bin` | no | `cove` | Host-side cove binary path. |
| `vm-name` | no | generated | Ephemeral fork name. |
| `keep` | no | `false` | Keep the ephemeral fork after the run for debugging. |

Outputs:

| Output | Description |
|---|---|
| `vm-name` | Ephemeral fork name used by the run. |
| `exit-code` | Guest command exit code. |
| `log-path` | Host path to the cove run log root. |
| `metrics-path` | Host path to the action run `metrics.jsonl`, when discovered. |
| `artifact-path` | Host path to the run artifact directory, when metrics are discovered. |

## Runner Setup

Use a trusted self-hosted macOS runner on Apple Silicon. The runner host must
already have:

- `cove` installed on `PATH`
- Virtualization.framework available
- the cove binary signed with the virtualization entitlement
- a local cove image suitable for `image`
- enough disk space for per-job APFS copy-on-write forks and artifacts
- the GitHub Actions runner installed outside the cove guest

The action runs on the host runner and starts cove VMs from there. It is not a
replacement for GitHub-hosted runners, and it does not install or register a
self-hosted runner by itself.

Example runner labels:

```yaml
runs-on: [self-hosted, macOS, ARM64, cove]
```

## Usage

Internal workflow example:

```yaml
name: cove smoke

on:
  workflow_dispatch:

jobs:
  smoke:
    runs-on: [self-hosted, macOS, ARM64, cove]
    steps:
      - uses: actions/checkout@v4
      - uses: ./.github/actions/cove-action
        id: cove
        with:
          image: ubuntu-runner
          script: |
            go version
            go test ./...
          timeout: 30m
```

The action path above is intentionally local. Until the public packaging
decision is made, use this only from private repositories that vendor or
reference the internal action source.

## Secrets

Slice 1 does not mount secret URI values into the guest. Use GitHub Actions
environment injection for non-sensitive smoke inputs, or wait for the later
secret-mount slice before passing credentials to guest jobs.

Example:

```yaml
with:
  image: ubuntu-runner
  script: ./ci/run-private-tests.sh
  env: |
    CI=1
```

Limitations:

- GitHub-injected secrets should not be passed through this Slice 1 wrapper.
- The host runner and operator remain trusted.
- Non-empty `secrets` input aborts the action instead of pretending to mount
  credentials.

## Security Limits

The action is for a trusted operator running untrusted job code in a local VM
fork. It is not a multi-tenant hosted runner service.

Hard constraints:

- Every job uses `cove run -fork-from <image> -ephemeral`.
- The parent image must be immutable during the job.
- The control socket and control token stay on the host and are not mounted
  into the guest.
- Host directories are not shared by default.
- Any configured shared folder must be opt-in, pinned by the operator, and
  read-only unless a later design explicitly relaxes that.
- Soft reset is not an accepted isolation primitive.

Known limits:

- A compromised guest can read anything GitHub or cove intentionally put inside
  that fork for the job.
- The macOS host, cove process, and self-hosted runner are part of the trusted
  computing base.
- Host kernel, hypervisor, and Virtualization.framework escapes are out of
  scope for the wrapper.
- Concurrent forks on the same host rely on the host hypervisor boundary.
- Public pull request workflows need the same approval and secret policies used
  for any self-hosted runner.

## Cleanup and Artifacts

At the end of each run the wrapper:

1. records the guest command exit code
2. writes the action outputs
3. shuts down the fork
4. removes the ephemeral fork state

Artifact collection is not part of Slice 1. The wrapper reports the host-side
`~/.vz/runs` root so operators can inspect cove's per-run logs. If teardown
fails, the action reports the orphaned VM name so the operator can inspect or
remove it with cove tooling.

Run logs are written to cove's host-side run directory. The workflow is
responsible for uploading them with `actions/upload-artifact` if they should be
retained by GitHub.

Each `cove-action` invocation also writes cove run metrics to
`~/.vz/runs/<run-id>/metrics.jsonl`. The stream uses the schema documented in
[Run Metrics](metrics.md), with one JSON object per line. In addition to the
normal cove run events, the action wrapper records `action_start` and
`action_complete` events around the guest job; `action_complete.extra.exit_code`
records the guest exit code. The wrapper also records `command_complete` after
the guest command returns so operators can separate wrapper time from VM boot,
readiness, command execution, and teardown.

Example:

```yaml
      - uses: ./.github/actions/cove-action
        id: cove
        with:
          image: ubuntu-runner
          script: ./ci/test.sh

      - uses: actions/upload-artifact@v4
        if: always()
        with:
          name: cove-run-logs
          path: ${{ steps.cove.outputs.log-path }}

      - uses: actions/upload-artifact@v4
        if: always()
        with:
          name: cove-run-metrics
          path: ~/.vz/runs/**/metrics.jsonl
```

## Publication Status

This action is internal/private only.

Do not:

- publish it to GitHub Marketplace
- advertise `uses: owner/repo/...@vX` as a public contract
- promise public registry-backed images
- treat the action inputs as stable public API

Marketplace publication is a later release decision, after the public action
repository, image distribution, signing, and support boundaries are settled.
