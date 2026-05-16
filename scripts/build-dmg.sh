#!/usr/bin/env bash
# Build a drag-install DMG for the signed + notarized cove binary.
#
# Usage: scripts/build-dmg.sh <signed-binary> <version> <output-dmg>
#   <signed-binary>   path to the post-notarize cove binary (e.g. dist/cove_darwin_arm64_v8.0/cove)
#   <version>         release version (e.g. 0.2.0); the DMG volume name uses this
#   <output-dmg>      path to write the .dmg to (e.g. dist/cove-0.2.0.dmg)
#
# The DMG contains:
#   /cove                          the signed binary
#   /Install to usr-local-bin      a symlink to /usr/local/bin so users see the install destination
#   /vz.entitlements               reference copy of the entitlements file
#   /README.txt                    a one-paragraph install hint
#
# The script uses only `hdiutil` and `ln`; it does not depend on `create-dmg`,
# AppleScript, or a GUI session, so it runs cleanly on a headless macOS runner.

set -euo pipefail

if [[ $# -ne 3 ]]; then
    echo "usage: $0 <signed-binary> <version> <output-dmg>" >&2
    exit 64
fi

BINARY=$1
VERSION=$2
OUTPUT=$3

if [[ ! -f "$BINARY" ]]; then
    echo "error: signed binary not found at $BINARY" >&2
    exit 66
fi

WORKDIR=$(mktemp -d -t cove-dmg)
trap 'rm -rf "$WORKDIR"' EXIT

STAGING="$WORKDIR/stage"
mkdir -p "$STAGING"

# Stage the binary preserving executable bit.
install -m 0755 "$BINARY" "$STAGING/cove"

# Drag-install hint: a symlink the user can drop the binary into via Finder.
ln -s /usr/local/bin "$STAGING/Install to usr-local-bin"

# Reference copy of the entitlements so users running the binary outside
# Homebrew can re-sign locally if needed.
ENTITLEMENTS_SRC=$(dirname "$(dirname "$(realpath "$0")")")/internal/autosign/vz.entitlements
if [[ -f "$ENTITLEMENTS_SRC" ]]; then
    cp "$ENTITLEMENTS_SRC" "$STAGING/vz.entitlements"
fi

cat > "$STAGING/README.txt" <<EOF
cove $VERSION

cove is signed with Developer ID and notarized by Apple, so it runs
without Gatekeeper prompts on macOS.

To install:
  1. Drag "cove" onto the "Install to usr-local-bin" link, or
  2. Open Terminal and run:
       sudo install -m 0755 /Volumes/cove\ $VERSION/cove /usr/local/bin/

To verify the signature locally:
  codesign --verify --strict --verbose /usr/local/bin/cove
  spctl --assess --type execute --verbose /usr/local/bin/cove

For Homebrew users:
  brew install tmc/tap/cove
EOF

# hdiutil creates a UDIF read-only zlib-compressed (UDZO) image, the standard
# format for distributed DMGs. Volume name shows in Finder when the DMG is
# mounted. -ov overwrites if the output exists from a prior run.
hdiutil create \
    -volname "cove $VERSION" \
    -srcfolder "$STAGING" \
    -fs HFS+ \
    -format UDZO \
    -imagekey zlib-level=9 \
    -ov \
    "$OUTPUT"

echo "wrote $OUTPUT"
