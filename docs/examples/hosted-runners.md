# Hosted Runner Examples

`cove` is the local VM runner engine. A Cirrus-style hosted runner product
should live outside this repository and call into `cove` for image validation,
ephemeral VM forks, logs, artifacts, and cleanup.

## Self-hosted macOS runner

Use this when GitHub Actions can schedule onto a trusted Apple Silicon Mac that
has `cove` installed and signed.

1. Install and sign `cove` on the Mac:

```bash
go build -o cove .
codesign -s - -f --entitlements internal/autosign/vz.entitlements ./cove
```

2. Bake a runner image:

```bash
cove up -vm macos-runner -user ci
cove image build -from macos-runner -tag macos-runner:latest
cove action doctor
cove action prepare-image macos-runner:latest --ttl 24h
```

Omit `-password` for interactive setup so cove prompts instead of saving the
guest password in shell history. Do not bake a shared default password into
runner images.

3. Generate the workflow:

```bash
cove runner workflow --image macos-runner:latest --script './ci/test.sh'
```

The generated job runs on labels like `[self-hosted, macOS, ARM64, cove]`,
preflights the host, validates the image, then invokes
`./.github/actions/cove-action`. The action forks a disposable VM from the
image, runs the script through the guest agent, records metrics under
`~/.vz/runs/`, and removes the fork unless `keep: true` is set.

## GitHub-hosted workflow with a remote cove Mac

Use this when the repository must run the GitHub job on `ubuntu-latest`, but
the VM work should happen on a trusted Mac reached over SSH.

1. Put the Mac's SSH target in a GitHub secret:

```text
COVE_HOST=ci@mac-mini.example.com
```

2. Ensure the Mac has `cove`, Go, SSH access, and enough disk space under
`~/.vz`.

3. Generate the workflow:

```bash
cove runner workflow \
  --mode github-hosted \
  --image macos-runner:latest \
  --remote '${{ secrets.COVE_HOST }}' \
  --script './ci/test.sh'
```

The generated job checks out source on GitHub-hosted Linux, preflights the
remote Mac with `cove action doctor`, prepares the image, rsyncs the checkout
to the Mac, and runs `cmd/cove-action` there. This keeps Apple Virtualization
on Apple hardware while allowing a normal GitHub-hosted job to drive it.

## Product boundary

Do not put hosted scheduling into `cove`. A separate hosted runner product
should own:

- GitHub App installation and webhook handling.
- Just-in-time runner registration.
- Queueing, concurrency groups, host selection, quotas, and billing.
- Tenant isolation, secret policy, audit logs, and observability.
- Retry and teardown supervision across host failures.

The stable `cove` contract for that service is the command surface:

```bash
cove action doctor --json
cove action prepare-image <ref> --json --ttl 24h
cove run -fork-from <ref> -ephemeral -headless
go run ./cmd/cove-action -image <ref> -script './ci/test.sh'
```
