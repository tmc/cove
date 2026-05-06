---
title: Tag cut runbook
status: Draft
date: 2026-05-05
---

# Tag cut runbook

This runbook leaves the release one command away from shipped. Do not run these
steps until the operator explicitly approves the tag.

## Common preflight

```bash
git fetch origin main --tags
git status --short --branch
git log --oneline origin/main..HEAD
go test ./...
go build -o cove .
codesign -s - -f --entitlements internal/autosign/vz.entitlements ./cove
codesign -d --entitlements - ./cove 2>&1 | grep -E 'com.apple.security.virtualization|com.apple.vm'
make release-check
```

The working tree must be clean before tagging. The local build must be
re-signed after every `go build`.

## v0.2.1

Do not cut `v0.2.1` at current `main` until the operator accepts the
version-order warning in `docs/release/v0.2.1-readiness-final.md`.

```bash
git tag -a v0.2.1 -m "v0.2.1 release"
git push origin v0.2.1
dist/build-v0.2.1.sh
dist/smoke-test.sh ./dist/v0.2.1/cove <fresh-vm-name>
gh release create v0.2.1 \
  --title "v0.2.1" \
  --notes-file RELEASE-NOTES-v0.2.1.md \
  dist/cove_0.2.1_darwin_arm64.tar.gz \
  dist/cove-0.2.1.dmg \
  dist/SHA256SUMS-v0.2.1.txt
```

Homebrew tap update is currently skipped because the repo remains private.
When public distribution is approved, copy `dist/Casks/cove-v0.2.1.rb` to the
tap after `dist/build-v0.2.1.sh` fills in the real SHA256.

## v0.3.0

Do not cut `v0.3.0` until the operator either runs the live local-base smoke or
accepts the blocked smoke gate in `docs/release/v0.3-readiness-final.md`.

```bash
git tag -a v0.3.0 -m "v0.3.0 release"
git push origin v0.3.0
dist/build-v0.3.0.sh
dist/smoke-test.sh ./dist/v0.3.0/cove <fresh-vm-name>
gh release create v0.3.0 \
  --title "v0.3.0" \
  --notes-file RELEASE-NOTES-v0.3.0.md \
  dist/cove_0.3.0_darwin_arm64.tar.gz \
  dist/cove-0.3.0.dmg \
  dist/SHA256SUMS-v0.3.0.txt
```

Homebrew tap update is currently skipped because the repo remains private.
When public distribution is approved, copy `dist/Casks/cove-v0.3.0.rb` to the
tap after `dist/build-v0.3.0.sh` fills in the real SHA256.

## One-command wrapper

After operator approval, the wrapper prompts for the exact version before doing
the tag/build sequence:

```bash
dist/cut-release.sh v0.3.0
```

The wrapper does not push to Homebrew and does not create a GitHub release; the
release upload remains a visible operator step.
