---
title: Migrate from Cirrus to cove
status: Draft
date: 2026-05-05
---

# Migrate from Cirrus to cove

Cirrus hosted CI shuts down on **2026-06-01**. This guide is the short path for a team moving a typical `.cirrus.yml` to cove while keeping VM-backed isolation and local control.

Cove is not a hosted queue. It replaces the VM image, fork-per-job, logs, and artifact substrate. Keep scheduling in GitHub Actions, your existing CI, or a small host-side runner script.

## Mapping

| Cirrus | Cove |
|---|---|
| `task:` | one workflow step or shell script that invokes cove |
| `container: image: foo` | `cove image build -from <vm> -tag foo` then `cove run -fork-from foo` |
| `macos_instance:` / `linux_instance:` | explicit cove macOS or Linux runner image |
| `script:` | command passed to the forked VM |
| Cirrus cache | cove image cache plus Design 030 build cache |
| persistent worker | `cove fleet add` for an operator-owned Mac host |
| task logs | `~/.vz/runs/<run-id>/`, `cove runs list`, `cove runs show` |

Run these checks before cutting over:

```bash
cove action doctor --fork-from acme/runner:latest
cove action prepare-image acme/runner:latest
cove image verify acme/runner:latest --strict --newer-than 7d
```

For multi-host execution, register Macs with the fleet slice 1 commands in [Fleet Quickstart](quickstart/fleet.md). For strategy and competitive context, see [Competitive Matrix, May 2026](strategy/competitive-2026-05.md).

## Example 1: simple build

Cirrus:

```yaml
test_task:
  container:
    image: ubuntu:24.04
  install_script: apt-get update && apt-get install -y golang
  script: go test ./...
```

Cove:

```bash
cove up -linux -vm ubuntu-runner -user ubuntu -password ubuntu
cove ctl -vm ubuntu-runner agent-exec --daemon -- bash -lc 'apt-get update && apt-get install -y golang'
cove image build -from ubuntu-runner -tag acme/runner:go
cove run -fork-from acme/runner:go -ephemeral -- bash -lc 'go test ./...'
```

The package install moves into the image. Each job starts from a fresh fork of `acme/runner:go`.

## Example 2: matrix

Cirrus:

```yaml
test_task:
  matrix:
    - env:
        GO_VERSION: "1.24"
    - env:
        GO_VERSION: "1.25"
  container:
    image: golang:$GO_VERSION
  script: go test ./...
```

Cove:

```bash
for go in 1.24 1.25; do
  cove image build -from "go-${go}-runner" -tag "acme/runner:go-${go}"
  cove run -fork-from "acme/runner:go-${go}" -ephemeral -- bash -lc 'go test ./...'
done
```

If the matrix spans multiple Macs, add each host once:

```bash
cove fleet add studio tmc@mac-studio.local -vm ubuntu
cove --fleet=studio image list
```

Then let your scheduler choose which host runs each image.

## Example 3: cached dependencies

Cirrus:

```yaml
deps_cache:
  folder: ~/.cache/go-build
  fingerprint_script: cat go.sum

test_task:
  container:
    image: golang:1.25
  script: go test ./...
```

Cove:

```bash
cove build -from go-base -tag acme/runner:deps -script ci/deps.vzscript
cove image verify acme/runner:deps --strict --newer-than 24h
cove run -fork-from acme/runner:deps -ephemeral -- bash -lc 'go test ./...'
```

The dependency cache becomes part of the runner image or Design 030 build cache entry. Freshness is explicit through `cove image verify --newer-than` instead of hidden in a hosted cache service.

## Cutover checklist

1. Build one runner image from a known-good VM.
2. Verify it with `cove image verify --strict --newer-than 7d`.
3. Run one Cirrus task equivalent with `cove run -fork-from ... -ephemeral`.
4. Compare logs under `~/.vz/runs/<run-id>/` to the old Cirrus task output.
5. Add `cove action doctor` and `cove action prepare-image` to the workflow preflight.

Do not migrate hosted queue semantics, Cirrus-specific cache assumptions, or warm-reset isolation claims directly. Cove's replacement boundary is a fresh VM fork from a verified image.
