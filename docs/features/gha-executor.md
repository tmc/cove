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
| `secrets` | no |  | Multiline `KEY=value`, `KEY=env://VAR`, or `KEY=file:///path` entries resolved on the host and injected with log redaction. |
| `timeout` | no | `30m` | Maximum runtime for provisioning and commands. |
| `cove-bin` | no | `cove` | Host-side cove binary path. |
| `vm-name` | no | generated | Ephemeral fork name. |
| `keep` | no | `false` | Keep the ephemeral fork after the run for debugging. |
| `cache-key` | no |  | Whole-VM cache key. Empty disables cache restore and save. |
| `cache-paths` | no |  | Multiline guest paths expected to benefit from the whole-VM cache. Informational only; cove snapshots the whole disk. |
| `artifacts` | no |  | Multiline absolute guest paths copied into the run bundle under `guest/` after the command finishes. |

Outputs:

| Output | Description |
|---|---|
| `vm-name` | Ephemeral fork name used by the run. |
| `exit-code` | Guest command exit code. |
| `log-path` | Host path to the cove run log root. |
| `metrics-path` | Host path to the action run `metrics.jsonl`, when discovered. |
| `artifact-path` | Host path to the run artifact directory, when metrics are discovered. |
| `cache-hit` | `true` when `cache-key` restored a local cache image. |
| `cache-image` | Local cache image ref used for lookup or save. |
| `cache-saved` | `true` when this run saved a cache image. |

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

## Preflight Commands

Preflight the host and runner image before wiring a workflow:

```bash
cove action doctor
cove action prepare-image ubuntu-runner
```

`cove action doctor` checks the host-side action prerequisites: `cove` is on
`PATH`, the binary is signed with the virtualization entitlement, the `~/.vz`
volume has enough free space, the network helper can list host interfaces, and
the run artifact root is writable. It is read-only.

`cove action prepare-image <ref>` checks that the local image ref is suitable for
action jobs before a workflow uses it. It verifies the image exists, can be
forked, has a current guest agent, can run a shell command through the agent, has
runner dependencies present, has enough disk headroom for a job fork, and has no
stale forks that would make operator intent ambiguous.

Both commands accept `--json` for automation. Human output is for operators;
JSON output is for CI gates. Exit codes are:

| Code | Meaning |
|---:|---|
| `0` | All required checks passed. |
| `1` | One or more preflight checks failed. The output lists the failing checks and hints. |
| `2` | Warning-only result, such as low but still usable free disk space. |

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

## Cache

Slice 2 adds optional local cross-run cache reuse. Set `cache-key` to restore
and save a whole-VM cache image on the trusted runner host:

```yaml
      - uses: ./.github/actions/cove-action
        id: cove
        with:
          image: ubuntu-runner
          cache-key: linux-go-${{ hashFiles('go.sum') }}
          cache-paths: |
            /home/runner/.cache/go-build
            /root/.npm
          script: go test ./...
```

The cache image lives only in the local image store:

```text
~/.vz/images/cache/<cache-key>/latest/
```

On a hit, the action runs from `cache/<cache-key>:latest`. On a miss, it runs
from `image`; if the guest command exits 0, the wrapper stops the fork, snapshots
it with `cove image build -from <vm-name> -tag cache/<cache-key>:latest`, writes
a `CACHE-TTL` marker with the default `168h` lifetime, and then deletes the
temporary fork unless `keep: true` was set.

Failed guest commands are not cached. If two runs race to save the same key, the
first writer wins and the duplicate save is treated as nonfatal.

`cache-paths` does not drive partial restore or extraction. It is a visible
workflow hint for humans and metrics; the saved object is the entire stopped VM
disk. Include lockfile hashes, toolchain versions, image versions, or other
invalidation inputs in `cache-key`.

Cache images are private to the host. The action never pushes cache images to a
registry and never uploads them as workflow artifacts. Because a whole-VM cache
can contain credentials written by the guest, do not write secrets to disk unless
you are comfortable keeping them on the trusted runner host.

`cove image gc` honors `CACHE-TTL` for `cache/*` images and removes expired cache
images only when no fork still references them. Operators should schedule GC
outside active CI windows.

## Secrets

The `secrets` input forwards entries to `cove shell --secret-env`, so values are
resolved on the trusted host and redacted from cove run logs. Each non-comment
line is `KEY=value`, `KEY=env://VAR`, or `KEY=file:///path`.

Example:

```yaml
with:
  image: ubuntu-runner
  script: ./ci/run-private-tests.sh
  secrets: |
    GH_TOKEN=env://GH_TOKEN
```

Limitations:

- The host runner and operator remain trusted.
- Secrets are process environment variables inside the guest command. They are
  not an isolated tmpfs secret mount.
- Avoid writing secrets to disk before saving whole-VM caches.

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
2. copies any declared guest artifacts into the cove run bundle
3. writes the action outputs
4. shuts down the fork
5. removes the ephemeral fork state

Declare guest artifact paths with the `artifacts` input. Paths must be absolute
inside the guest. Cove copies them into the host-side run bundle under
`guest/<path-without-leading-slash>/` before teardown, then exposes the bundle
directory as `steps.<id>.outputs.artifact-path`.

Run logs and copied guest artifacts are written to cove's host-side run
directory. The workflow is responsible for uploading that directory with
`actions/upload-artifact` if it should be retained by GitHub.

Each `cove-action` invocation also writes cove run metrics to
`~/.vz/runs/<run-id>/metrics.jsonl`. The stream uses the schema documented in
[Run Metrics](metrics.md), with one JSON object per line. In addition to the
normal cove run events, the action wrapper records `action_start` and
`action_complete` events around the guest job; `action_complete.extra.exit_code`
records the guest exit code. The wrapper also records `command_complete` after
the guest command returns so operators can separate wrapper time from VM boot,
readiness, command execution, and teardown. Each copied guest artifact records an
`artifact_copy` event with `guest_path`, `host_path`, and copied byte count when
the host can stat the copied data.

Example:

```yaml
      - uses: ./.github/actions/cove-action
        id: cove
        with:
          image: ubuntu-runner
          script: ./ci/test.sh
          artifacts: |
            /tmp/junit.xml
            /tmp/build/report.html

      - uses: actions/upload-artifact@v4
        if: always()
        with:
          name: cove-run
          path: ${{ steps.cove.outputs.artifact-path }}
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
