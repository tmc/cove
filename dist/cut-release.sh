#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 ]]; then
	echo "usage: $0 v0.2.1|v0.3.0" >&2
	exit 64
fi

VERSION=$1
case "$VERSION" in
	v0.2.1) BUILD=dist/build-v0.2.1.sh ;;
	v0.3.0) BUILD=dist/build-v0.3.0.sh ;;
	*)
		echo "error: unsupported release version: $VERSION" >&2
		exit 64
		;;
esac

if [[ -n $(git status --porcelain) ]]; then
	echo "error: working tree is dirty" >&2
	exit 1
fi

git fetch origin main --tags
if [[ $(git rev-parse HEAD) != $(git rev-parse origin/main) ]]; then
	echo "error: HEAD is not origin/main" >&2
	exit 1
fi

if git rev-parse -q --verify "refs/tags/$VERSION" >/dev/null; then
	echo "error: tag already exists locally: $VERSION" >&2
	exit 1
fi

printf 'Type %s to create and push the release tag: ' "$VERSION"
read -r confirm
if [[ "$confirm" != "$VERSION" ]]; then
	echo "aborted" >&2
	exit 1
fi

git tag -a "$VERSION" -m "$VERSION release"
git push origin "$VERSION"
"$BUILD"

cat <<EOF

Tag and local artifacts complete for $VERSION.

Next:
  dist/smoke-test.sh ./dist/$VERSION/cove <fresh-vm-name>
  gh release create $VERSION --title "$VERSION" --notes-file RELEASE-NOTES-$VERSION.md ...

Homebrew tap push is intentionally not included while cove is private.
EOF
