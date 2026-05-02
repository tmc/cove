---
title: Release pipeline
---

# Release pipeline

Releases are cut from a maintainer workstation. CI only runs tests on tag
pushes; signing and notarization happen locally so the Developer ID
certificate and the App Store Connect app-specific password never leave
the machine that holds them.

A successful run produces:

- `cove_<version>_darwin_arm64.tar.gz` — tar.gz archive (Homebrew + manual install).
- `cove-<version>.dmg` — drag-install DMG, signed and notarized.
- `vz-agent_<version>_<os>_<arch>.tar.gz` — guest agent for darwin/arm64 and linux/arm64.
- `checksums.txt` — SHA256 sums.

The DMG is built by `scripts/build-dmg.sh` from the post-notarize binary
and stapled before upload.

## Why local-only signing

Storing a `.p12` of the Developer ID identity in GitHub Actions secrets
is a real supply-chain risk: anyone who can land a workflow change or
escalate via a compromised dependency can exfiltrate the certificate.
The trade-off is the maintainer becomes the bottleneck per release and
the Homebrew cask must be bumped manually. For a single-maintainer tool
that releases on a human cadence, the trade is worth it.

CI is only used to verify that a tag still builds and tests pass. No
secrets are configured on the repository.

## One-time keychain setup

These steps run once per workstation, before the first release.

1. **Confirm the Developer ID identity is in the login keychain.**

   ```bash
   security find-identity -v -p codesigning login.keychain-db
   # Expect a line like:
   #   1) ABCD1234... "Developer ID Application: Your Name (TEAMID)"
   ```

   If the identity is missing, request it from the Apple Developer
   portal and import the resulting `.cer` plus the private key from
   the original CSR.

2. **Store notarytool credentials in the keychain.**

   Generate an app-specific password at
   <https://appleid.apple.com/account/manage> labelled
   `cove-notarytool`, then:

   ```bash
   xcrun notarytool store-credentials cove-notarytool \
       --apple-id <apple-id> \
       --team-id <TEAMID> \
       --password <app-specific-password>
   ```

   The credentials live in the login keychain under the profile name
   `cove-notarytool` and are referenced by name from then on.

3. **Export `DEVELOPER_ID` to your shell environment.**

   ```bash
   export DEVELOPER_ID="Developer ID Application: Your Name (TEAMID)"
   ```

   Add to `.zshrc` / `.bashrc` for persistence. The release script
   refuses to run without it.

## Cutting a release

The release script enforces a clean working tree and refuses to run on
a non-tag commit, so the order matters: commit first, tag second, run
the script third.

```bash
# 1. Land the changelog and version bump on main.
# 2. Sign and push the tag.
git tag -s v0.1.4 -m "cove v0.1.4"
git push origin v0.1.4

# 3. Wait for CI to confirm the tag builds and tests pass.
gh run watch

# 4. Build, sign, notarize, and staple locally.
make release-local
# (or: scripts/release-local.sh)

# 5. Upload the artifacts to the GitHub release.
gh release create v0.1.4 \
    --title "cove v0.1.4" \
    --notes-file CHANGELOG.md \
    dist/cove_0.1.4_darwin_arm64.tar.gz \
    dist/cove-0.1.4.dmg \
    dist/checksums.txt \
    dist/vz-agent_0.1.4_darwin_arm64.tar.gz \
    dist/vz-agent_0.1.4_linux_arm64.tar.gz

# 6. Bump the Homebrew cask manually (see "Manual cask bump" below).

# 7. Smoke-test the published cask.
brew update
brew install tmc/tap/cove
cove version
```

## What `release-local` does

`scripts/release-local.sh` runs seven steps in order. Each step refuses
to start if its precondition is not met.

1. **Guards.** Bail if the working tree has uncommitted changes, if
   `HEAD` is not on a tag matching `v*`, if `$DEVELOPER_ID` is unset or
   missing from the keychain, or if the `cove-notarytool` keychain
   profile is invalid.
2. **Build.** `goreleaser release --snapshot --clean --skip=publish`
   produces unsigned archives plus the unsigned `cove` binary at
   `dist/cove_darwin_arm64*/cove`.
3. **Codesign the binary.** `codesign --sign "$DEVELOPER_ID"
   --options runtime --timestamp --force --entitlements
   internal/autosign/vz.entitlements <binary>`. Hardened runtime + a
   secure timestamp + the virtualization entitlements are all required
   for notarization to accept the binary.
4. **Notarize the binary.** Wrap the binary with `ditto -c -k
   --keepParent` (notarytool only takes `.zip` / `.pkg` / `.dmg`),
   submit with `xcrun notarytool submit --keychain-profile
   cove-notarytool --wait`, and confirm the response is `Accepted`.
   Stapling a raw binary is not supported by Apple — the ticket is
   recorded by hash and Gatekeeper looks it up online when the binary
   first runs.
5. **Build, sign, notarize, and staple the DMG.** `scripts/build-dmg.sh`
   produces `dist/cove-<version>.dmg`; `codesign` signs it with the
   same identity; `xcrun notarytool submit --wait` notarizes it; and
   `xcrun stapler staple` attaches the ticket so an offline downloader
   can verify the DMG without contacting Apple.
6. **Re-pack `cove_<version>_darwin_arm64.tar.gz`.** goreleaser archived
   the unsigned binary; we rebuild the tar.gz over the signed binary
   and refresh `checksums.txt` against all final artifacts.
7. **Verify.** `codesign -dvv` on the binary plus
   `spctl -a -vv -t install` on the DMG. Either failing aborts the
   release before it leaves the machine.

After step 7 the script prints the artifact paths and the suggested
`gh release create` invocation.

## Manual cask bump

The cask used to be auto-PRed by goreleaser. With local-only signing,
the maintainer opens the PR against `tmc/homebrew-tap` by hand:

```bash
# 1. Compute the SHA256 of the published tar.gz.
shasum -a 256 dist/cove_0.1.4_darwin_arm64.tar.gz
# Or pull from the published checksums.txt:
gh release view v0.1.4 -R tmc/cove --json assets \
    --jq '.assets[] | select(.name=="checksums.txt").url' \
    | xargs curl -sL

# 2. Edit Casks/cove.rb in tmc/homebrew-tap, updating:
#      version "0.1.4"
#      sha256  "<shasum-of-tar.gz>"
#      url     "https://github.com/tmc/cove/releases/download/v0.1.4/cove_0.1.4_darwin_arm64.tar.gz"

# 3. Open the PR.
git checkout -b cove-0.1.4
git commit -am "cove 0.1.4"
git push -u origin cove-0.1.4
gh pr create --base master --title "cove 0.1.4" --body ""
```

Until the PR is merged, `brew install tmc/tap/cove` still resolves to
the previous version; merging is part of the cut, not an optional
follow-up.

## Smoke-testing the DMG build offline

`scripts/build-dmg.sh` does not require any signing keys; it only uses
`hdiutil` and `ln`. Dry-run before the real cut:

```bash
GOWORK=off go build -o /tmp/cove .
codesign -s - -f --entitlements internal/autosign/vz.entitlements /tmp/cove
./scripts/build-dmg.sh /tmp/cove 0.1.4-dryrun /tmp/cove-dryrun.dmg
hdiutil verify /tmp/cove-dryrun.dmg
```

The `hdiutil verify` step confirms the DMG is well-formed; the resulting
`.dmg` will be ad-hoc-signed only and **not notarized**, so do not
publish it. `release-local.sh` re-builds the DMG against the
post-notarize binary and signs+notarizes the DMG itself.

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
documented in `docs/research/trademark-cove.md`. Until counsel clears
the name or a rename lands, this pipeline is fine for `v0.x.y` and
`v0.x.y-rcN` pre-releases against the existing repository, but **a v1.0
public release must not ship under the cove name** without the
trademark gate cleared.

This document explicitly does not propose alternative names; that work
is tracked separately. The release pipeline itself is name-agnostic —
only the user-visible strings in `.goreleaser.yml`, `README.md`, and
cask metadata would need renaming if the project moves to a different
name.

## Troubleshooting

### `error: DEVELOPER_ID is unset`

Export the full identity string in your shell, e.g.
`export DEVELOPER_ID="Developer ID Application: Your Name (TEAMID)"`.
Match it character-for-character against the second column of
`security find-identity -v -p codesigning`.

### `error: notarytool keychain profile 'cove-notarytool' is missing`

Run the `xcrun notarytool store-credentials` command from the one-time
setup above. If the password rotates, re-run the same command — it
overwrites the existing entry.

### Notarization hangs

App Store Connect occasionally has multi-hour queues; `--wait` blocks
indefinitely. If it stalls, kill the script with Ctrl-C and resume by
re-running it — the build is incremental and notarytool will surface
the status of any in-flight submission with `xcrun notarytool history
--keychain-profile cove-notarytool`.

### `codesign` complains about an expired certificate

Developer ID Application certificates expire after five years. Generate
a new CSR from Keychain Access, request a fresh cert in the Apple
Developer portal, install it, and update the value of `$DEVELOPER_ID`.
Existing notarized releases stay valid; only future signs need the new
identity.
