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
# The script prints the artifact paths and upload command after verification.

set -euo pipefail

REPO_ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$REPO_ROOT"

# --- Guards ----------------------------------------------------------------

STATUS=$(git status --porcelain)
if [[ -n "$STATUS" ]]; then
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

if ! xcrun notarytool history --keychain-profile cove-notarytool >/dev/null; then
    echo "error: notarytool preflight failed for keychain profile 'cove-notarytool'" >&2
    exit 1
fi

ENTITLEMENTS="cmd/cove/vz.entitlements"
if [[ ! -f "$ENTITLEMENTS" ]]; then
    echo "error: entitlements file not found at $ENTITLEMENTS" >&2
    exit 1
fi

# --- Build -----------------------------------------------------------------

echo ">>> building $TAG via goreleaser (skip publish)"
GOWORK=off GORELEASER_CURRENT_TAG="$TAG" goreleaser release --clean --skip=publish

binaries=(.release-dist/cove_darwin_arm64*/cove)
if [[ ${#binaries[@]} -ne 1 || ! -f "${binaries[0]}" ]]; then
    echo "error: expected one GoReleaser cove binary" >&2
    exit 1
fi
BINARY="${binaries[0]}"
ARCHIVE=".release-dist/cove_${VERSION}_darwin_arm64.tar.gz"
AGENT_DARWIN=".release-dist/vz-agent_${VERSION}_darwin_arm64.tar.gz"
AGENT_LINUX=".release-dist/vz-agent_${VERSION}_linux_arm64.tar.gz"
for artifact in "$ARCHIVE" "$AGENT_DARWIN" "$AGENT_LINUX"; do
    if [[ ! -f "$artifact" ]]; then
        echo "error: GoReleaser did not produce $artifact" >&2
        exit 1
    fi
done

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

ZIP=".release-dist/cove-${VERSION}-binary.zip"
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

DMG=".release-dist/cove-${VERSION}.dmg"
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
echo ">>> rebuilding $ARCHIVE against signed binary"
TMP_STAGE=$(mktemp -d -t cove-archive)
trap 'rm -rf "$TMP_STAGE"' EXIT
install -m 0755 "$BINARY" "$TMP_STAGE/cove"
cp "$ENTITLEMENTS" "$TMP_STAGE/"
[[ -f LICENSE ]]   && cp LICENSE   "$TMP_STAGE/"
[[ -f README.md ]] && cp README.md "$TMP_STAGE/"
( cd "$TMP_STAGE" && tar czf "$REPO_ROOT/$ARCHIVE" . )

# Refresh checksums against the rebuilt artifacts.
CHECKSUMS=".release-dist/checksums.txt"
echo ">>> refreshing $CHECKSUMS"
( cd .release-dist && shasum -a 256 \
    "cove_${VERSION}_darwin_arm64.tar.gz" \
    "cove-${VERSION}.dmg" \
    "${AGENT_DARWIN#.release-dist/}" \
    "${AGENT_LINUX#.release-dist/}" \
    > checksums.txt )

# --- Verification ----------------------------------------------------------

echo ">>> verifying $BINARY"
codesign -dvv "$BINARY"
spctl -a -vv -t install "$DMG"

cat <<EOF

Release artifacts ready at $REPO_ROOT/.release-dist:

  $ARCHIVE
  $DMG
  $CHECKSUMS
  $AGENT_DARWIN
  $AGENT_LINUX

Next steps:

  gh release create $TAG --title "$TAG" --notes-file RELEASE-NOTES-next.md \\
      "$ARCHIVE" \\
      "$DMG" \\
      "$CHECKSUMS" \\
      "$AGENT_DARWIN" \\
      "$AGENT_LINUX"

Then bump the Homebrew formula manually in tmc/homebrew-tap (no auto-PR
from CI any more — see docs/release-pipeline.md).
EOF
