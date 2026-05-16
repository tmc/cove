# Cove after Cirrus CI

Status: private landing-page draft. Do not publish this page until the release,
privacy, and trademark review gates are complete.

Cirrus Labs announced that Cirrus CI shuts down on Monday, June 1, 2026:
<https://cirruslabs.org/>. Teams with `.cirrus.yml`, hosted macOS jobs,
Tart-backed images, or persistent workers need a replacement execution surface
before that date.

Cove is not a hosted CI service. It is the Apple Silicon VM substrate for teams
that want to keep their scheduler and own the machine running the VM job. Keep
GitHub Actions, Buildkite, or another scheduler. Move the VM boundary to cove:
verified runner images, disposable forks, guest-agent command execution, local
logs, and machine-readable run metrics.

## Why Cirrus worked

Cirrus had a strong operator story:

- hosted Linux and macOS task VMs;
- Tart image workflows for Apple Silicon macOS jobs;
- persistent workers for teams with their own Macs;
- `.cirrus.yml` as a compact task format;
- task logs, caches, secrets, artifacts, schedules, and queueing in one system.

The cove migration does not pretend those are one feature. It separates the
scheduler from the VM substrate so the parts are inspectable.

## Cove mapping

| Cirrus strength | Cove analog |
|---|---|
| Hosted task VM | `cove run -fork-from <image-ref> -ephemeral` on a trusted Mac host. |
| Tart-backed macOS image | `cove image build`, `cove image verify`, then fork from the local image. |
| Persistent worker | GitHub self-hosted runner labels or fleet host selection; each job still runs in a disposable cove fork. |
| `.cirrus.yml` task script | `.github/actions/cove-action` `command` or `script` input. |
| Cache service | Whole-VM cove action cache from a local image key. |
| Task logs and artifacts | `~/.vz/runs/<run-id>/`, `cove runs list/show/export`, and `metrics.jsonl`. |
| Warm reset between jobs | Soft-reset probes exist, but CI isolation uses fresh forks instead of trusting warm cleanup. |
| GUI or agent evaluation jobs | `cove agent-sandbox run` starts provider loops in fresh image forks. |

## Proof points

Phase 2 benchmark evidence is checked in under
[`docs/benchmarks/results-2026-05-cove.json`](../benchmarks/results-2026-05-cove.json)
and rendered in
[`docs/benchmarks/competitive-2026-05.md`](../benchmarks/competitive-2026-05.md).
Use those files for citable values.

| Workload | Cove result | Why it matters |
|---|---:|---|
| Cold boot to guest agent | 52.7s | A fresh local image reaches the command channel in under a minute on the measured host. |
| Image build from stopped VM | 37.6s | Runner image refresh is fast enough to make image hygiene part of the daily workflow. |
| Parallel 16-fork fan-out | 1.16s | The differentiator: many task VMs can materialize from one stopped parent faster than per-task cold spin-up. |

Competitor cells in the May 2026 report are intentionally marked
`not measured` unless the repository contains same-host evidence. Do not
fabricate hosted Cirrus numbers.

## Quickstart

Until the public tap is available, build cove from a private checkout on the
trusted Apple Silicon runner host:

```bash
git clone git@github.com:tmc/cove.git
cd cove
go build -o cove .
codesign -s - -f --entitlements internal/autosign/vz.entitlements ./cove
```

Then migrate one Cirrus task from your existing repository:

1. Build or refresh a runner image.

   ```bash
   cove up -vm macos-runner -user ci -password '<admin-password>'
   cove image build -from macos-runner -tag macos-runner:latest
   cove image verify --strict --newer-than 168h macos-runner:latest
   ```

2. Run the task through the same private action wrapper used in GitHub Actions.

   ```bash
   cove action prepare-image macos-runner:latest --ttl 24h
   go run ./cmd/cove-action \
     -image macos-runner:latest \
     -command './ci/test.sh'
   ```

3. Add the private action to the workflow and keep Cirrus running on the same
   commit until results match.

   ```yaml
   jobs:
     test:
       runs-on: [self-hosted, macOS, ARM64, cove]
       steps:
         - uses: actions/checkout@v4
         - uses: ./.github/actions/cove-action
           with:
             image: macos-runner:latest
             script: ./ci/test.sh
   ```

Use the full migration walkthrough for real `.cirrus.yml` mappings:
[`docs/migrations/from-cirrus.md`](../migrations/from-cirrus.md). Run the
checklist before deleting old tasks:
[`docs/migrations/from-cirrus-checklist.md`](../migrations/from-cirrus-checklist.md).

## What cove does not replace yet

Cove is still missing parts of the hosted CI surface:

- **Public registry path.** Private image push/pull exists, but public registry
  promotion remains behind release, privacy, and trademark review gates.
- **Scheduled cron jobs.** Use the scheduler you already trust, such as GitHub
  Actions `schedule`, launchd, Buildkite, or another orchestrator.
- **Long-term artifact storage.** Cove records local run artifacts and can
  export them, but retention and long-storage belong in your artifact system
  for now.

Those are roadmap items, not hidden features. The migration pitch is narrower:
use cove for the VM execution boundary while your scheduler continues to own
triggers, approvals, retention, and notifications.

## Cutover rule

Run the old Cirrus task and the new cove-backed job on the same commit. Compare
exit code, test summary, artifacts, and `metrics.jsonl`. Turn off the Cirrus
task only after the new job has passed the agreed soak period.
