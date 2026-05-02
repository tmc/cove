---
title: Release pipeline
---

# Release pipeline

The release pipeline is tag-driven. Pushing a tag matching `v*` to GitHub
triggers `.github/workflows/release.yml`, which runs goreleaser on a
`macos-14` runner to produce signed + notarized artifacts:

- `cove_<version>_darwin_arm64.tar.gz` — tar.gz archive (Homebrew + manual install).
- `cove-<version>.dmg` — drag-install DMG, signed and notarized.
- `vz-agent_<version>_<os>_<arch>.tar.gz` — guest agent for darwin/arm64 and linux/arm64.
- `checksums.txt` — SHA256 sums.
- A cask update PR opened against `tmc/homebrew-tap` (default branch: `master`).

The DMG is built by `scripts/build-dmg.sh` from the post-notarize binary;
the cask currently points at the tar.gz for fast `brew install`, with the
DMG offered as a drag-install alternative in the cask `caveats`.

## Required GitHub repository secrets

Set these in **Settings → Secrets and variables → Actions** on the
`tmc/vz-macos` repository before cutting the first release tag.

| Secret | What it is |
|---|---|
| `MACOS_DEVELOPER_ID_CERT_P12_BASE64` | Base64-encoded `.p12` export of the **Developer ID Application** certificate + private key. Generate with `security export -k login.keychain-db -t certs -f pkcs12 -P <password> -o cert.p12 && base64 < cert.p12 \| pbcopy`. |
| `MACOS_DEVELOPER_ID_CERT_PASSWORD` | The password used during the `.p12` export. |
| `MACOS_NOTARY_APPLE_ID` | The Apple ID of the account enrolled in the Apple Developer program (e.g. `release@example.com`). Used as the notarytool key ID. |
| `MACOS_NOTARY_TEAM_ID` | The 10-character Team ID from the Apple Developer membership page. Used as the notarytool issuer ID. |
| `MACOS_NOTARY_APP_PASSWORD` | An **app-specific password** generated at <https://appleid.apple.com/account/manage> → "App-Specific Passwords". Used as the notarytool key. |
| `HOMEBREW_TAP_TOKEN` | A scoped GitHub PAT with `contents: write` and `pull_requests: write` on `tmc/homebrew-tap`. Required so goreleaser can push the side branch and open the cask PR. |

`GITHUB_TOKEN` is provided automatically by Actions and does not need to be
configured.

## First-time setup

1. **Export the Developer ID certificate.**
   On a workstation with the cert in the login keychain:

   ```bash
   security find-identity -v -p codesigning login.keychain-db
   # Look for "Developer ID Application: Your Name (TEAMID)"
   security export -k login.keychain-db -t identities -f pkcs12 \
     -P "<choose-a-password>" -o ~/cove-developer-id.p12
   base64 < ~/cove-developer-id.p12 | pbcopy
   # Paste into MACOS_DEVELOPER_ID_CERT_P12_BASE64
   # Paste the password into MACOS_DEVELOPER_ID_CERT_PASSWORD
   rm ~/cove-developer-id.p12
   ```

2. **Generate an app-specific password for notarytool.**
   Sign in at <https://appleid.apple.com/account/manage>, generate a new
   app-specific password labelled "cove-notarytool", and paste it into
   `MACOS_NOTARY_APP_PASSWORD`.

3. **Create a tap PAT.**
   Generate a fine-grained personal access token scoped only to
   `tmc/homebrew-tap` with `contents: write`. Paste into
   `HOMEBREW_TAP_TOKEN`.

4. **Smoke-test on a pre-release tag.**
   The first run is the riskiest; cut a `vX.Y.Z-rc1` tag against a branch
   and inspect the produced release. The workflow automatically marks
   pre-release tags via goreleaser's `prerelease: auto`.

## Cutting a release

```bash
# 1. Land the changelog edits and version bump on main.
# 2. Tag with the standard prefix.
git tag -s v0.1.3 -m "cove v0.1.3"
git push origin v0.1.3
# 3. Watch the run.
gh run watch
# 4. After the run lands, find and merge the cask PR.
gh pr list -R tmc/homebrew-tap
gh pr merge -R tmc/homebrew-tap <PR-number> --squash
# 5. Smoke-test the published cask.
brew update
brew install tmc/tap/cove
cove version
```

The cask PR's branch (`cove-<version>`) is opened against the tap's default
branch (`master`). Until the PR is merged, `brew install tmc/tap/cove` will
still resolve to the previous version; merging is part of the cut, not an
optional follow-up.

The workflow runs in three logical phases:

1. **Setup**: checkout, set up Go (version pinned via `go.mod`), import
   the Developer ID cert into a temporary keychain, resolve
   `MACOS_SIGN_IDENTITY`.
2. **Build + notarize**: goreleaser builds, signs, and notarizes the
   `cove` binary using the entitlements at
   `internal/autosign/vz.entitlements`. The `vz-agent` builds for
   darwin/arm64 and linux/arm64 (no signing required).
3. **DMG package + publish**: `scripts/build-dmg.sh` packages the
   notarized binary into a DMG; the DMG is then signed, notarized, and
   stapled. A second goreleaser run uploads everything to GitHub
   Releases and pushes the cask update.

## Smoke-testing the DMG build offline

`scripts/build-dmg.sh` does not require any signing keys; it only uses
`hdiutil` and `ln`. You can dry-run the DMG step against a locally-built
binary before cutting a tag:

```bash
GOWORK=off go build -o /tmp/cove .
codesign -s - -f --entitlements internal/autosign/vz.entitlements /tmp/cove
./scripts/build-dmg.sh /tmp/cove 0.1.3-dryrun /tmp/cove-dryrun.dmg
hdiutil verify /tmp/cove-dryrun.dmg
```

The `hdiutil verify` step confirms the DMG is well-formed; the resulting
`.dmg` will be ad-hoc-signed only and **not notarized**, so do not publish
it. The release workflow re-builds the DMG against the post-notarize
binary and signs+notarizes the DMG itself before upload.

## Verifying a release locally

After a release ships, end users can verify it without re-running
notarization:

```bash
# Verify the signed binary
codesign --verify --strict --verbose=2 /usr/local/bin/cove

# Confirm Gatekeeper acceptance
spctl --assess --type execute --verbose /usr/local/bin/cove

# Confirm the DMG ships notarized + stapled
xcrun stapler validate cove-<version>.dmg
```

## Public release gate

The cove binary and project name remain under a USPTO conflict review
documented in `docs/research/trademark-cove.md`. Until counsel clears the
name or a rename lands, this pipeline is fine for `v0.x.y` and `v0.x.y-rcN`
pre-releases against the existing repository, but **a v1.0 public release
must not ship under the cove name** without the trademark gate cleared.

This document explicitly does not propose alternative names; that work is
tracked separately. The release pipeline itself is name-agnostic — only
the user-visible strings in `.goreleaser.yml`, `README.md`, and cask
metadata would need renaming if the project moves to a different name.

## Troubleshooting

### `no Developer ID Application identity found in imported keychain`

The `.p12` export did not include the **identity** (cert + private key
pair). Use `security export -t identities` rather than `-t certs`. If the
private key is missing from your local login keychain, you need to
re-download the cert from the Apple Developer portal — the private key is
generated at CSR time and cannot be re-exported.

### Notarization hangs past the 20-minute timeout

App Store Connect occasionally has multi-hour queues. Re-run the workflow;
the goreleaser `notarize.macos.notarize.timeout` is configured at 20m for
the binary and `xcrun notarytool submit --wait` for the DMG. If
queue-induced failure becomes routine, switch to async notarization plus a
follow-up `stapler staple` step.

### `homebrew-tap` PR not opened

The most common cause is `HOMEBREW_TAP_TOKEN` not having `contents: write`
on the tap repo, or the tap repo missing the target branch. Verify both
before re-running.
