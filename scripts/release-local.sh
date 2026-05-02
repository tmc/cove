#!/usr/bin/env bash
# Build, sign, notarize, staple, and verify a cove release locally.
#
# Run from the repo root on a clean checkout that is sitting on a v* tag.
# The Developer ID identity is selected via $DEVELOPER_ID, which must match
# an entry in `security find-identity -v -p codesigning`. Notarization uses
# the keychain profile `cove-notarytool` set up once via:
#
#     xcrun notarytool store-credentials cove-notarytool \
#       --apple-id <apple-id> --team-id <TEAMID> --password <app-specific-pw>
#
# After this script succeeds, upload the printed artifacts with:
#
#     gh release create v<version> dist/cove_<version>_darwin_arm64.tar.gz \
#       dist/cove-<version>.dmg dist/checksums.txt
#
# and bump the Homebrew cask manually in tmc/homebrew-tap.

set -euo pipefail

# --- Guards ----------------------------------------------------------------

if ! git diff-index --quiet HEAD --; then
    echo "error: uncommitted changes in working tree; commit or stash first" >&2
    exit 1
fi

if ! TAG=$(git describe --exact-match --tags HEAD 2>/dev/null); then
    echo "error: HEAD is not on a release tag (use git tag -s vX.Y.Z first)" >&2
    exit 1
fi

case "$TAG" in
    v*) ;;
    *)
        echo "error: tag '$TAG' does not start with 'v'" >&2
        exit 1
        ;;
esac

VERSION="${TAG#v}"

if [[ -z "${DEVELOPER_ID:-}" ]]; then
    echo "error: DEVELOPER_ID is unset; export DEVELOPER_ID='Developer ID Application: Your Name (TEAMID)'" >&2
    echo "       (run 'security find-identity -v -p codesigning' to list installed identities)" >&2
    exit 1
fi

if ! security find-identity -v -p codesigning | grep -F "$DEVELOPER_ID" >/dev/null; then
    echo "error: identity '$DEVELOPER_ID' not found in any keychain" >&2
    exit 1
fi

if ! xcrun notarytool history --keychain-profile cove-notarytool >/dev/null 2>&1; then
    echo "error: notarytool keychain profile 'cove-notarytool' is missing or invalid" >&2
    echo "       set it up with: xcrun notarytool store-credentials cove-notarytool \\" >&2
    echo "                         --apple-id <apple-id> --team-id <TEAMID> --password <app-specific-pw>" >&2
    exit 1
fi

REPO_ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$REPO_ROOT"

ENTITLEMENTS="internal/autosign/vz.entitlements"
if [[ ! -f "$ENTITLEMENTS" ]]; then
    echo "error: entitlements file not found at $ENTITLEMENTS" >&2
    exit 1
fi

# --- Build -----------------------------------------------------------------

echo ">>> building $TAG via goreleaser snapshot (skip publish)"
GOWORK=off goreleaser release --snapshot --clean --skip=publish

BINARY=$(ls dist/cove_darwin_arm64*/cove 2>/dev/null | head -1 || true)
if [[ -z "$BINARY" ]]; then
    echo "error: goreleaser did not produce dist/cove_darwin_arm64*/cove" >&2
    find dist -maxdepth 2 -type f | head -20 >&2
    exit 1
fi

# --- Codesign --------------------------------------------------------------

echo ">>> codesigning $BINARY"
codesign --sign "$DEVELOPER_ID" \
    --options runtime \
    --timestamp \
    --force \
    --entitlements "$ENTITLEMENTS" \
    "$BINARY"

codesign --verify --strict --verbose=2 "$BINARY"

# --- Notarize the binary (zip submission) ----------------------------------

ZIP="dist/cove-${VERSION}-binary.zip"
echo ">>> packaging binary for notarization at $ZIP"
ditto -c -k --keepParent "$BINARY" "$ZIP"

echo ">>> submitting binary to notarytool (this can take several minutes)"
xcrun notarytool submit "$ZIP" \
    --keychain-profile cove-notarytool \
    --wait

# Note: stapler does not support raw binaries — only bundles, dmgs, pkgs.
# The notarization ticket is recorded with Apple by hash; Gatekeeper checks
# it online. We staple the DMG below, which is what users download.

# --- Build the DMG against the signed+notarized binary ---------------------

DMG="dist/cove-${VERSION}.dmg"
echo ">>> building $DMG"
./scripts/build-dmg.sh "$BINARY" "$VERSION" "$DMG"

echo ">>> codesigning $DMG"
codesign --sign "$DEVELOPER_ID" --timestamp --force "$DMG"

echo ">>> notarizing $DMG"
xcrun notarytool submit "$DMG" \
    --keychain-profile cove-notarytool \
    --wait

echo ">>> stapling $DMG"
xcrun stapler staple "$DMG"
xcrun stapler validate "$DMG"

# --- Re-pack the tar.gz against the signed binary --------------------------

# goreleaser produced an archive against the *unsigned* binary; rebuild it
# now that the binary on disk is signed+notarized so users get a signed
# binary out of the tar.gz too.
ARCHIVE="dist/cove_${VERSION}_darwin_arm64.tar.gz"
echo ">>> rebuilding $ARCHIVE against signed binary"
TMP_STAGE=$(mktemp -d -t cove-archive)
trap 'rm -rf "$TMP_STAGE"' EXIT
install -m 0755 "$BINARY" "$TMP_STAGE/cove"
cp "$ENTITLEMENTS" "$TMP_STAGE/"
[[ -f LICENSE ]]   && cp LICENSE   "$TMP_STAGE/"
[[ -f README.md ]] && cp README.md "$TMP_STAGE/"
( cd "$TMP_STAGE" && tar czf "$REPO_ROOT/$ARCHIVE" . )

# Refresh checksums against the rebuilt artifacts.
CHECKSUMS="dist/checksums.txt"
echo ">>> refreshing $CHECKSUMS"
( cd dist && shasum -a 256 \
    "cove_${VERSION}_darwin_arm64.tar.gz" \
    "cove-${VERSION}.dmg" \
    vz-agent_*_darwin_arm64.tar.gz \
    vz-agent_*_linux_arm64.tar.gz \
    > checksums.txt )

# --- Verification ----------------------------------------------------------

echo ">>> verifying $BINARY"
codesign -dvv "$BINARY"
spctl -a -vv -t install "$DMG"

cat <<EOF

Release artifacts ready at $REPO_ROOT/dist:

  $ARCHIVE
  $DMG
  $CHECKSUMS
  dist/vz-agent_${VERSION}_darwin_arm64.tar.gz
  dist/vz-agent_${VERSION}_linux_arm64.tar.gz

Next steps:

  gh release create $TAG --title "$TAG" --notes-file <changelog> \\
      "$ARCHIVE" \\
      "$DMG" \\
      "$CHECKSUMS" \\
      dist/vz-agent_${VERSION}_darwin_arm64.tar.gz \\
      dist/vz-agent_${VERSION}_linux_arm64.tar.gz

Then bump the Homebrew cask manually in tmc/homebrew-tap (no auto-PR
from CI any more — see docs/release-pipeline.md).
EOF
