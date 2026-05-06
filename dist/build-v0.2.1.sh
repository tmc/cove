#!/usr/bin/env bash
set -euo pipefail

VERSION=v0.2.1
VERSION_NO_V=${VERSION#v}
REPO_ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$REPO_ROOT"

OUT="dist/$VERSION"
BIN="$OUT/cove"
TAR="dist/cove_${VERSION_NO_V}_darwin_arm64.tar.gz"
DMG="dist/cove-${VERSION_NO_V}.dmg"
SUMS="dist/SHA256SUMS-${VERSION}.txt"
CASK="dist/Casks/cove-${VERSION}.rb"
ENTITLEMENTS="internal/autosign/vz.entitlements"
COMMIT=$(git rev-parse --short HEAD)
DATE=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

mkdir -p "$OUT" dist/Casks

GOWORK=off go build -trimpath -ldflags "-s -w -X main.version=$VERSION -X main.commit=$COMMIT -X main.date=$DATE" -o "$BIN" .
codesign -s - -f --entitlements "$ENTITLEMENTS" "$BIN"
codesign --verify --strict --verbose=2 "$BIN"

TMP=$(mktemp -d -t cove-release)
trap 'rm -rf "$TMP"' EXIT
install -m 0755 "$BIN" "$TMP/cove"
cp "$ENTITLEMENTS" "$TMP/"
test -f LICENSE && cp LICENSE "$TMP/"
test -f README.md && cp README.md "$TMP/"
(cd "$TMP" && tar czf "$REPO_ROOT/$TAR" .)

./scripts/build-dmg.sh "$BIN" "$VERSION_NO_V" "$DMG"

(cd dist && shasum -a 256 \
	"cove_${VERSION_NO_V}_darwin_arm64.tar.gz" \
	"cove-${VERSION_NO_V}.dmg" \
	> "$(basename "$SUMS")")

SHA=$(shasum -a 256 "$TAR" | awk '{print $1}')
cat > "$CASK" <<EOF
cask "cove" do
  version "$VERSION_NO_V"
  sha256 "$SHA"

  url "https://github.com/tmc/cove/releases/download/$VERSION/cove_${VERSION_NO_V}_darwin_arm64.tar.gz"
  name "cove"
  desc "macOS and Linux VM management using Apple's Virtualization framework"
  homepage "https://github.com/tmc/cove"

  binary "cove"
end
EOF

printf 'built %s\n  %s\n  %s\n  %s\n  %s\n' "$VERSION" "$BIN" "$TAR" "$DMG" "$SUMS"
