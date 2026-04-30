---
title: Release Checklist
---
# Release Checklist

Use this checklist for production tags. The historical
`docs/v0.1.0-publish-checklist.md` records the first release; this page is the
current runbook.

## Preflight

```bash
git fetch origin main --tags
git status --short --branch
git log --oneline origin/main..HEAD
```

Expect a clean worktree and no unintended commits ahead of `origin/main`.

Required local tools:

```bash
go version
codesign -h >/dev/null
goreleaser --version
```

Install missing release tooling with:

```bash
brew install goreleaser
```

## Quality Gates

```bash
go test ./...
go build -o cove .
codesign -s - -f --entitlements internal/autosign/vz.entitlements ./cove
codesign -dv --entitlements - ./cove
make release-check
```

The signed local binary must include `com.apple.security.virtualization`.
`make release-check` runs short tests, `go vet`, and a GoReleaser snapshot.

For `cove build`, verify the production boundary before tagging:

```bash
./cove help build
./cove build --base ghcr.io/acme/base@sha256:base --script missing.vzscript vm
./cove build --base ghcr.io/acme/base@sha256:base --script missing.vzscript --push vm
```

The second command must fail before script loading with:

```text
cove build: non-dry-run requires local VM base directory
```

The third command must fail with:

```text
cove build: --push is not implemented
```

## Docs Gates

```bash
go test . -run 'TestBuildCLIDocs|TestBuildDumpDocs'
./cove dump-docs -type cli -pretty >/tmp/cove-cli-docs.json
```

Check that:

- `docs/reference/changelog.md` describes the release surface.
- `docs/reference/cli.md` matches the command help.
- `docs/designs/ROADMAP.md` does not mark unfinished work as shipped.
- Public docs say non-dry-run `cove build` requires a local VM base directory
  and that `--push` remains gated.

## Tag And Publish

```bash
git push origin main
git tag -s vX.Y.Z -m "cove vX.Y.Z"
git push origin vX.Y.Z
goreleaser release --clean
```

After publish:

```bash
gh release view vX.Y.Z
brew update
brew install tmc/tap/cove
cove version
```

## Rollback

If the release is published with a bad artifact, remove the GitHub release
before deleting or moving the tag:

```bash
gh release delete vX.Y.Z --yes
git push origin :refs/tags/vX.Y.Z
git tag -d vX.Y.Z
```

Do not reuse a tag after public artifacts or Homebrew checksums have propagated;
cut a patch release instead.
