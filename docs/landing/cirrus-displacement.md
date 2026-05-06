# Cove for Cirrus CI migration

Cirrus Labs announced on April 7, 2026 that Cirrus CI will shut down on
Monday, June 1, 2026:
<https://cirruslabs.org/>. Teams using `.cirrus.yml`, Tart-backed macOS tasks,
or Cirrus-hosted queues need another scheduler and another VM isolation story
before that date.

Cove is the local Apple Silicon VM substrate for that migration. Keep GitHub
Actions, Buildkite, or your existing scheduler. Replace hosted Cirrus VM tasks
with disposable cove forks from verified runner images. Current citable
benchmark evidence lives in
[`docs/strategy/proof.md`](../strategy/proof.md); quote numbers from that table,
not from memory.

## Five-line migration

```bash
cove action doctor
cove image verify macos-runner:latest --strict --newer-than 7d
cove action prepare-image macos-runner:latest --ttl 24h
go run ./cmd/cove-action -image macos-runner:latest -command 'xcodebuild test'
cove run -fork-from macos-runner:latest -ephemeral -headless
```

The first four lines are the job path: validate the host, validate the image,
preflight the image for action use, then execute the guest command through the
private cove action wrapper. The final line is the underlying VM primitive:
spawn a fresh ephemeral fork from the same image and delete it at shutdown.

## Feature parity

The broader comparison lives in
[`docs/strategy/competitive-2026-05.md`](../strategy/competitive-2026-05.md).
Measured or explicitly unmeasured benchmark rows live in
[`docs/strategy/proof.md`](../strategy/proof.md).

| Need | Cirrus / Tart | Cove replacement |
|---|---|---|
| Task VM | `macos_instance`, `linux_instance`, Tart VM | `cove run -fork-from <image-ref> -ephemeral` |
| Runner image | Tart image, Packer template, hosted image | `cove image build`, `cove image verify`, private OCI push/pull |
| Task script | `.cirrus.yml` `script:` | `.github/actions/cove-action` `command` or `script` input |
| Isolation between jobs | Hosted task VM or persistent worker policy | Fresh image fork per job; no soft-reset trust boundary |
| Logs and metrics | Cirrus task logs | `~/.vz/runs/<run-id>/`, `cove runs list/show/export`, `metrics.jsonl` |
| Cache | Cirrus cache service | Whole-VM cove action cache from design 030 |
| Local debugging | Re-run Tart VM manually | `cove run`, `cove shell`, `cove ctl agent-exec` |

## What cove does that Cirrus did not

Cove's default CI boundary is fork-first. A job starts from a known runner
image, materializes a fresh child VM, runs through the guest agent, emits run
artifacts, and tears the child down. The same primitive is available locally
with `cove run -fork-from <image-ref> -ephemeral`.

That is deliberate. Design 015
([`docs/designs/015-soft-reset-empirical.md`](../designs/015-soft-reset-empirical.md))
measured warm-guest soft reset and found it was not a reliable isolation
primitive for privacy-critical work. Cove therefore does not ask displaced
Cirrus users to trust UID recycling, cache cleanup, or a persistent worker
reset hook as the security boundary.

Design 030
([`docs/designs/030-gha-executor-slice-2.md`](../designs/030-gha-executor-slice-2.md))
adds cache reuse without changing that boundary: cache hits restore a local
whole-VM image, then each job still runs in its own disposable fork.

## Get started

```bash
cove up -vm macos-runner -user ci -password '<admin-password>'
GH_REPO=owner/repo GH_TOKEN='<registration-token>' cove vzscript run github-runner
cove image build -from macos-runner -tag macos-runner:latest
```

After the image exists, wire the private action into GitHub Actions with
`uses: ./.github/actions/cove-action` and set `image: macos-runner:latest`.
Run `cove action doctor`, `cove action prepare-image`, and
`cove image verify --strict --newer-than 7d` in the preflight path before
turning off the Cirrus job.
