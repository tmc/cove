# Quickstart from Cirrus

Status: private migration quickstart. Keep public install instructions gated
until the release, privacy, trademark, and Homebrew tap availability checks are
complete.

Use this when a repository already has `.cirrus.yml` and needs one cove-backed
replacement job. For the detailed mapping, see
[`docs/migrations/from-cirrus.md`](migrations/from-cirrus.md).

## 1. Build cove from a private checkout

```bash
git clone git@github.com:tmc/cove.git
cd cove
go build -o cove .
codesign -s - -f --entitlements internal/autosign/vz.entitlements ./cove
```

Put that signed `cove` binary on the trusted Apple Silicon runner host.

## 2. Pick one Cirrus task

Start with one task that has a clear script and no release secrets. Keep the
old Cirrus task enabled while the cove job soaks.

```bash
find . \( -name .cirrus.yml -o -name .cirrus.yaml \) -print
cove vzscript run cirrus-migrate-doctor
```

## 3. Prepare a runner image

Bake slow setup into a parent VM, then verify it before jobs use it:

```bash
cove up -vm macos-runner -user ci
cove image build -from macos-runner -tag macos-runner:latest
cove image verify --strict --newer-than 168h macos-runner:latest
cove action prepare-image macos-runner:latest --ttl 24h
```

For interactive image setup, let cove prompt for the guest password. Do not use
a fixed password such as `cove/cove` in reusable runner images.

## 4. Run the job through the private action wrapper

From the cove checkout:

```bash
go run ./cmd/cove-action \
  -image macos-runner:latest \
  -command './ci/test.sh'
```

The wrapper starts a disposable fork with `cove run -fork-from`, waits for the
guest agent, runs the command, records `~/.vz/runs/<run-id>/metrics.jsonl`, and
tears the fork down.

## 5. Add the GitHub Actions job

You can generate a starting workflow with:

```bash
cove runner workflow --image macos-runner:latest --script './ci/test.sh'
```

```yaml
jobs:
  migrated-task:
    runs-on: [self-hosted, macOS, ARM64, cove]
    steps:
      - uses: actions/checkout@v4
      - run: cove action doctor
      - run: |
          cove image verify --strict --newer-than 168h macos-runner:latest
          cove action prepare-image macos-runner:latest --ttl 24h
      - uses: ./.github/actions/cove-action
        with:
          image: macos-runner:latest
          script: ./ci/test.sh
```

Compare the Cirrus task and cove job on the same commit. Check exit code, test
summary, artifacts, and `metrics.jsonl`. Delete the old task only after the new
job has passed the agreed soak period.
