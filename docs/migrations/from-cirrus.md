# Migrating from Cirrus CI to cove

Cirrus CI shuts down on Monday, June 1, 2026. Use this guide to translate
`.cirrus.yml` tasks into private GitHub Actions jobs that run on a trusted
Apple Silicon host with cove.

The scheduler changes. The VM boundary stays explicit: each CI job starts from
a verified local cove image and runs in a disposable fork with
`cove run -fork-from <image> -ephemeral`.

## Baseline workflow

Prepare the parent image once, then run jobs from forks:

```bash
cove image verify --strict --newer-than 168h macos-runner:latest
cove action prepare-image macos-runner:latest --ttl 24h
```

```yaml
jobs:
  test:
    runs-on: [self-hosted, macOS, ARM64, cove]
    steps:
      - uses: actions/checkout@v4
      - name: Run in cove fork
        uses: ./.github/actions/cove-action
        with:
          image: macos-runner:latest
          script: ./ci/test.sh
```

The private action wraps:

```bash
cove run -fork-from macos-runner:latest -fork-name <job-vm> -ephemeral -headless
```

and then runs the script through the guest agent. Run logs and metrics land
under `~/.vz/runs/<run-id>/`.

## Container task

Original Cirrus task:

```yaml
linux_test_task:
  container:
    image: golang:1.23
  env:
    GOFLAGS: -mod=readonly
  script:
    - go test ./...
```

cove equivalent:

```bash
cove up -linux -vm ubuntu-go -user ci -password '<admin-password>'
cove ctl -vm ubuntu-go agent-exec -- /bin/sh -lc 'sudo apt-get update && sudo apt-get install -y golang-go'
cove image build -from ubuntu-go -tag ubuntu-go:latest
cove image verify --strict --newer-than 168h ubuntu-go:latest
```

```yaml
jobs:
  linux-test:
    runs-on: [self-hosted, macOS, ARM64, cove]
    steps:
      - uses: actions/checkout@v4
      - run: cove action prepare-image ubuntu-go:latest --ttl 24h
      - uses: ./.github/actions/cove-action
        with:
          image: ubuntu-go:latest
          env: |
            GOFLAGS=-mod=readonly
          script: go test ./...
```

Semantic changes:

| Cirrus concern | cove mapping |
|---|---|
| Container image | Build a cove Linux runner image once, then fork it per job. |
| Checkout | GitHub Actions checkout runs on the host; copy or mount source explicitly in later slices. For Slice 1, keep the guest command self-contained or fetch source inside the guest. |
| Environment | Non-secret `KEY=VALUE` lines use the action `env` input. |
| Secrets | Slice 1 does not mount secret URI values into the guest. Use non-sensitive smoke inputs or wait for the secret-mount slice. |
| Artifacts | Upload `~/.vz/runs/<run-id>/` from the host. Guest artifact copy-out is not part of Slice 1. |
| Cache | Use whole-VM `cache-key`; it snapshots a cove image, not individual directories. |

## macOS task

Original Cirrus task:

```yaml
macos_test_task:
  macos_instance:
    image: ghcr.io/cirruslabs/macos-runner:sonoma
  env:
    DEVELOPER_DIR: /Applications/Xcode.app/Contents/Developer
  install_script:
    - brew bundle --file=ci/Brewfile
  script:
    - xcodebuild test -scheme App -destination 'platform=macOS'
```

cove equivalent:

```bash
cove up -vm macos-runner -user ci -password '<admin-password>'
cove ctl -vm macos-runner agent-exec -- /bin/bash -lc 'brew bundle --file=ci/Brewfile'
cove ctl -vm macos-runner agent-exec -- /bin/bash -lc 'xcodebuild -version'
cove image build -from macos-runner -tag macos-runner:latest
cove image verify --strict --newer-than 168h macos-runner:latest
```

```yaml
jobs:
  macos-test:
    runs-on: [self-hosted, macOS, ARM64, cove]
    steps:
      - uses: actions/checkout@v4
      - run: cove action prepare-image macos-runner:latest --ttl 24h
      - uses: ./.github/actions/cove-action
        with:
          image: macos-runner:latest
          env: |
            DEVELOPER_DIR=/Applications/Xcode.app/Contents/Developer
          script: |
            xcodebuild test -scheme App -destination 'platform=macOS'
```

Semantic changes:

| Cirrus concern | cove mapping |
|---|---|
| `macos_instance.image` | Local cove image ref, usually built from a maintained parent VM. |
| `install_script` | Bake slow tool setup into the parent image before `cove image build`. |
| `script` | Action `script` input, executed by the guest agent in the disposable fork. |
| Artifacts | Preserve result bundles by copying them to the run artifact area in a later artifact slice, or inspect the kept fork with `keep: true` while migrating. |
| Cache | Whole-VM cache images can retain DerivedData, package caches, and tool installs. Avoid writing credentials to disk before saving caches. |

## persistent_worker

Original Cirrus task:

```yaml
persistent_worker:
  labels:
    env: mac-mini

build_task:
  persistent_worker:
    labels:
      env: mac-mini
  script:
    - ./ci/build.sh
```

cove equivalent:

```yaml
jobs:
  build:
    runs-on: [self-hosted, macOS, ARM64, cove]
    concurrency:
      group: mac-mini-cove
      cancel-in-progress: false
    steps:
      - uses: actions/checkout@v4
      - run: cove action prepare-image mac-mini-runner:latest --ttl 24h
      - uses: ./.github/actions/cove-action
        with:
          image: mac-mini-runner:latest
          cache-key: mac-mini-${{ hashFiles('Package.resolved') }}
          cache-paths: |
            ~/Library/Developer/Xcode/DerivedData
          script: ./ci/build.sh
```

Semantic changes:

| Cirrus concern | cove mapping |
|---|---|
| Persistent host label | GitHub self-hosted runner labels select the Mac. |
| Mutable worker state | Do not use the persistent host as the isolation boundary. Each job gets a fresh cove fork. |
| Warm caches | Use whole-VM `cache-key` when the cache contents are safe to retain on the trusted host. |
| Cleanup | cove-action tears down the fork by default; set `keep: true` only for debugging. |
| Scheduling | Use GitHub `concurrency` or runner groups for host-level serialization. |

## Matrix task

Original Cirrus task:

```yaml
test_task:
  matrix:
    - name: linux
      container:
        image: golang:1.23
    - name: macos
      macos_instance:
        image: ghcr.io/cirruslabs/macos-runner:sonoma
  script:
    - go test ./...
```

cove equivalent:

```yaml
jobs:
  test:
    runs-on: [self-hosted, macOS, ARM64, cove]
    strategy:
      fail-fast: false
      matrix:
        include:
          - name: linux
            image: ubuntu-go:latest
            shell: /bin/sh
          - name: macos
            image: macos-go:latest
            shell: /bin/bash
    steps:
      - uses: actions/checkout@v4
      - run: cove action prepare-image ${{ matrix.image }} --ttl 24h
      - uses: ./.github/actions/cove-action
        with:
          image: ${{ matrix.image }}
          script: go test ./...
```

Semantic changes:

| Cirrus concern | cove mapping |
|---|---|
| Matrix expansion | GitHub Actions `strategy.matrix`. |
| Per-row image | `matrix.image` selects the cove parent image. |
| Per-row environment | Use `matrix` values plus action `env`. |
| Cross-platform host | cove currently runs on Apple Silicon hosts with Apple's Virtualization.framework. |
| Result comparison | Compare exit code, test summary, and `metrics.jsonl` for each matrix row before deleting `.cirrus.yml`. |

## Cutover guardrails

Keep the first migration private and boring:

- preflight with `cove action doctor`, `cove image verify --strict`, and
  `cove action prepare-image`;
- keep the Cirrus task and cove job running on the same commit until outputs
  match;
- upload `~/.vz/runs/<run-id>/` as a workflow artifact for every failed run;
- do not pass GitHub secrets through the Slice 1 action `secrets` input;
- do not publish a Marketplace action while cove is still private;
- do not treat soft reset as the CI isolation boundary.

Use [`from-cirrus-checklist.md`](from-cirrus-checklist.md) before the first
cutover. Use [`vzscripts/cirrus-migrate-doctor.vzscript`](../../vzscripts/cirrus-migrate-doctor.vzscript)
from the repository root to inventory local migration inputs.
