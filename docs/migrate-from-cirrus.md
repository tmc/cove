---
title: Migrate from Cirrus to cove
status: Draft
date: 2026-05-05
---

# Migrate from Cirrus to cove

Cirrus is shutting down its hosted CI service on 2026-06-01. This guide is the
practical migration path for teams that already rely on Cirrus runners,
`.cirrus.yml`, and VM-backed job isolation.

The short version is:

- Cirrus tasks become `cove run -fork-from` jobs.
- Cirrus worker images become `cove image build` artifacts.
- Cirrus task wiring becomes `cove action` for GitHub Actions, or a direct
  `cove run` invocation if you want to own the host runner yourself.
- cove keeps the fork-per-job isolation model local and explicit.

This draft assumes you want the same operational shape Cirrus gave you, but
with cove as the local execution substrate.

## What changes

Cirrus bundled three things together:

1. task declaration
2. runner image distribution
3. hosted execution

cove separates those concerns:

1. task declaration stays in your repo workflow or script
2. runner images live in `~/.vz/images/<name>/<tag>/` and can be pushed to an
   OCI registry
3. execution happens on your operator-owned macOS host through cove forks

That split is intentional. It keeps the VM boundary local, makes image
freshness visible, and avoids depending on a hosted service for the critical
job boundary.

## Compatibility model

This is not a drop-in replacement for Cirrus CI.

It is a migration guide for the parts that matter operationally:

- a reproducible VM image
- a task runner
- a clean per-job fork
- logs, metrics, and artifacts

If your Cirrus config depends on a hosted queue, Cirrus task templates, or
Cirrus-specific cache behavior, you should treat that as a port, not a toggle.

## Cirrus to cove mapping

| Cirrus task | cove equivalent | Notes |
| --- | --- | --- |
| `.cirrus.yml` task | `cove action` or a shell script wrapped by `cove run -fork-from` | Keep task logic in shell or workflow YAML; cove owns the VM boundary. |
| `linux_instance` / `instance_type` | `cove run -linux ...` / `cove up -linux ...` | Choose the guest shape directly instead of relying on Cirrus instance metadata. |
| `macos_instance` | `cove run` / `cove up` on the macOS host | Use the host VM knobs already exposed by cove. |
| `task_name:` | `cove action` step name, workflow job name, or run label | Name the run where you schedule it, not in the VM itself. |
| Cirrus worker image | `cove image build -from <vm> -tag <name[:tag]>` | Build once, then reuse via image refs. |
| Cirrus image pull | `cove image pull <registry/repo:tag> -tag <name[:tag]>` | Pull into the local image store before running jobs. |
| Cirrus image push | `cove image push <name[:tag]> <registry/repo:tag>` | Use OCI registry transport. Docker credentials are reused. |
| Cirrus startup script | `cove vzscript run ...` or `cove action` preflight | Put repeatable setup into a vzscript or a small wrapper. |
| Cirrus script step | `cove run -fork-from <image> -- <cmd>` or `cove shell <vm>` | Use shell execution inside the forked VM. |
| Cirrus cache | cove image layers, local VM forks, and host-side run artifacts | There is no Cirrus cache server equivalent here. |
| Cirrus logs | `~/.vz/runs/<run-id>/` and `cove runs show` | Keep logs and metrics on the operator host. |
| Cirrus rerun | `cove run -fork-from <image> -ephemeral` | Re-run from the same frozen image, not a warm reset. |
| Cirrus persistent worker | operator-owned cove host + pinned image | Use this only when you actually want a long-lived machine. |

## Recommended migration shape

### 1. Freeze the runner image

Build a runner image from a stopped VM:

```bash
cove image build -from ubuntu-runner -tag acme/runner:2026-05-05
```

If the image is meant to travel between hosts, push it to an OCI registry:

```bash
cove image push acme/runner:2026-05-05 ghcr.io/acme/runner:2026-05-05
```

Keep image verification in the release path:

```bash
cove image verify acme/runner:2026-05-05 --strict
```

### 2. Run each job in a fork

Use the image as the parent of each job:

```bash
cove run -fork-from acme/runner:2026-05-05 -ephemeral -- bash -lc 'go test ./...'
```

That is the cove equivalent of a Cirrus task running in an isolated worker.

### 3. Keep task logic outside the VM image

Put job logic in one of two places:

- a workflow step or script on the host
- a vzscript recipe for repeatable guest-side setup

Do not bury important task semantics inside the image build step unless the
image is intentionally part of the contract.

### 4. Treat image freshness as a gate

The image manifest records provenance and freshness fields. Use them.

Before you promote a runner image, verify:

- the image parses
- the manifest is current
- `execattach.v3` is present
- the image was built by the expected cove binary

If verification fails, fix the image and rebuild it. Do not keep using a stale
runner image just because it still boots.

## Private distribution model

cove remains a private repo and does not ship a public OCI image channel yet.

That means the migration model is:

- direct outreach to teams that need a replacement for Cirrus
- private binary distribution of cove itself
- private OCI registry use for runner images if you need cross-host transport
- operator-owned hosts for execution

Do not plan on a public Marketplace action or a public image catalog as part of
this migration.

## Cirrus task patterns and cove replacements

### Simple build-and-test task

Cirrus:

```yaml
test_task:
  container:
    image: ubuntu:24.04
  install_script: apt-get update && apt-get install -y golang
  script: go test ./...
```

cove:

```bash
cove image build -from ubuntu-runner -tag acme/runner:go
cove run -fork-from acme/runner:go -ephemeral -- bash -lc 'go test ./...'
```

### Task with prep and artifact capture

Cirrus often mixes setup, test, and artifact upload into one task. In cove,
split those concerns:

1. prepare the image once
2. run the job in a fresh fork
3. collect logs and artifacts from `~/.vz/runs/<run-id>/`

That makes the execution boundary visible and keeps reruns cheap.

### Task that expected hosted queueing

If you depended on Cirrus as a hosted queue, you need a local scheduling
decision now:

- GitHub Actions on a self-hosted macOS runner
- a plain host-side loop that invokes `cove run`
- a wrapper script in your own CI system

cove provides the VM execution and isolation layer, not the queue.

## Operational notes

- Use `cove action doctor` and `cove action prepare-image` before wiring a
  workflow.
- Keep `cove image verify` in the promotion path for runner images.
- Keep `--net` and `-sandbox-level` explicit when a job needs restricted
  networking.
- Prefer `-ephemeral` for disposable CI jobs.
- Use `cove runs list` and `cove runs show` to debug job history.

## What not to migrate blindly

- hosted Cirrus queue semantics
- Cirrus-specific cache assumptions
- warm-reset as an isolation boundary
- any public distribution story that assumes cove has a public OCI channel

## Suggested first port

If you want the least risky migration path, start with one workflow:

1. build a runner image from a known-good stopped VM
2. run one job via `cove run -fork-from`
3. compare logs, metrics, and artifact paths with the old Cirrus job
4. only then port the rest of the workflow

That keeps the migration anchored to a concrete job boundary rather than a
platform rewrite.

