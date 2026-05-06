# Cirrus to cove migration walkthrough

This walkthrough converts a common Cirrus macOS task into a GitHub Actions job
that runs on a local Apple Silicon host with cove. The scheduler changes from
Cirrus CI to GitHub Actions, but the important VM property remains: each job
gets a disposable VM.

Cirrus Labs announced that Cirrus CI shuts down on Monday, June 1, 2026:
<https://cirruslabs.org/>. Treat this as a cutover plan, not a long-term dual
run.

## Starting point: Cirrus macOS task

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

The task asks Cirrus for a macOS VM, prepares dependencies, and runs one
`xcodebuild` command. The cove replacement separates those phases:

1. build or refresh a local runner image;
2. verify the image before jobs use it;
3. run each job in an ephemeral fork;
4. collect run logs and metrics from `~/.vz/runs/`.

## Step 1: create the runner VM

```bash
cove up -vm macos-runner -user ci -password '<admin-password>'
cove ctl -vm macos-runner agent-exec -- /bin/bash -lc 'xcodebuild -version'
```

Expected output from the second command:

```text
Xcode 16.x
Build version ...
```

If the image still needs tools, install them in the long-lived parent VM:

```bash
cove ctl -vm macos-runner agent-exec -- /bin/bash -lc 'brew bundle --file=ci/Brewfile'
```

## Step 2: save and verify the image

```bash
cove image build -from macos-runner -tag macos-runner:latest
cove image verify macos-runner:latest --strict --newer-than 7d
cove action prepare-image macos-runner:latest --ttl 24h
```

Expected output is intentionally boring: `verify` and `prepare-image` should
exit 0. With `--json`, both commands emit machine-readable status for workflow
preflight.

## Step 3: run the job locally

Use the same wrapper the composite GitHub Action uses:

```bash
go run ./cmd/cove-action \
  -image macos-runner:latest \
  -command "DEVELOPER_DIR=/Applications/Xcode.app/Contents/Developer xcodebuild test -scheme App -destination 'platform=macOS'"
```

Expected output includes the guest command's stdout and stderr. On success the
wrapper exits 0 and writes GitHub-style output keys when `GITHUB_OUTPUT` is set:

```text
exit-code=0
vm-name=cove-action-...
log-path=/Users/<you>/.vz/runs/<run-id>
metrics-path=/Users/<you>/.vz/runs/<run-id>/metrics.jsonl
```

Under the hood, the wrapper starts:

```bash
cove run -fork-from macos-runner:latest -fork-name <job-vm> -ephemeral -headless
```

then waits for the guest agent, runs the command through the control socket,
stops the fork, and removes it unless `keep: true` is set.

## Step 4: convert to GitHub Actions

```yaml
name: macos tests

on:
  pull_request:
  push:

jobs:
  test:
    runs-on: [self-hosted, macOS, ARM64, cove]
    steps:
      - uses: actions/checkout@v4
      - name: Preflight cove host
        run: cove action doctor
      - name: Verify runner image
        run: |
          cove image verify macos-runner:latest --strict --newer-than 7d
          cove action prepare-image macos-runner:latest --ttl 24h
      - name: Run in cove fork
        uses: ./.github/actions/cove-action
        with:
          image: macos-runner:latest
          command: |
            DEVELOPER_DIR=/Applications/Xcode.app/Contents/Developer \
              xcodebuild test -scheme App -destination 'platform=macOS'
```

This is private-repo syntax. Do not use a public Marketplace URL until cove has
a public release channel.

## Step 5: add cache reuse when ready

Design 030 adds whole-VM local cache reuse without changing the isolation
boundary. A cache hit chooses another local image as the parent; the job still
runs in its own ephemeral fork.

```yaml
      - name: Run in cove fork
        uses: ./.github/actions/cove-action
        with:
          image: macos-runner:latest
          cache-key: macos-xcode-${{ hashFiles('Package.resolved') }}
          cache-paths: |
            ~/Library/Developer/Xcode/DerivedData
          command: |
            xcodebuild test -scheme App -destination 'platform=macOS'
```

Use `cache-key` for inputs that make a whole VM cache valid: lockfiles, tool
versions, SDK versions, or base image tags. Do not treat it as a partial
directory cache; the saved object is a cove image.

## Step 6: compare the mapping

| Cirrus line | Cove line |
|---|---|
| `macos_instance.image` | `cove image build -tag macos-runner:latest` |
| `env.DEVELOPER_DIR` | inline shell env or action `env` input |
| `install_script` | parent image preparation, then `cove image build` |
| `script` | cove-action `command` or `script` |
| hosted task logs | `~/.vz/runs/<run-id>/`, `cove runs show`, `cove runs export` |
| Cirrus cache | cove-action `cache-key` whole-VM cache |

## Cutover check

Before deleting `.cirrus.yml`, run the old Cirrus task and the cove action job
against the same commit. Compare:

- guest command exit code;
- `xcodebuild` result bundle or test summary;
- cove run metrics: fork time, agent-ready time, command duration;
- artifacts exported with `cove runs export <run-id> --format tar`.

If a command depends on an unshipped cove surface, mark it post-v0.4 in the
workflow and keep that line out of the cutover path. The commands above use the
shipped `cove action`, `cove image`, `cove run -fork-from`, and `cmd/cove-action`
surfaces on origin/main.
