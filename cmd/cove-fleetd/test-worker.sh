#!/usr/bin/env bash
set -euo pipefail

controller_dir=$(cd "$(dirname "$0")" && pwd)
cove_dir=$(cd "$controller_dir/../.." && pwd)
scratch=$(mktemp -d "${TMPDIR:-$cove_dir}/cove-fleet-worker.XXXXXX")
trap 'rm -r "$scratch"' EXIT

(
    cd "$scratch"
    GOWORK=off go work init "$cove_dir" "$controller_dir"
)
python3 - "$cove_dir/internal/coved/fleet_controller_test.go" "$controller_dir/testdata/fleet_controller_test.go" > "$scratch/overlay.json" <<'PY'
import json
import sys

print(json.dumps({"Replace": {sys.argv[1]: sys.argv[2]}}))
PY

cd "$cove_dir"
GOWORK="$scratch/go.work" go test -overlay "$scratch/overlay.json" ./internal/coved "$@"
