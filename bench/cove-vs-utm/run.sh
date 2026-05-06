#!/bin/sh
set -eu

bench_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
. "$bench_dir/internal/harness.sh"

out_dir=
tool=utmctl
workload=${WORKLOAD:-guest-ready-and-command}

usage() {
	echo "usage: bench/cove-vs-utm/run.sh [--out DIR]" >&2
}

while [ "$#" -gt 0 ]; do
	case "$1" in
	--out) out_dir=$2; shift 2 ;;
	-h|--help) usage; exit 0 ;;
	*) usage; exit 2 ;;
	esac
done

if [ -z "$out_dir" ]; then
	out_dir=$(bench_default_out_dir cove-vs-utm)
fi
mkdir -p "$out_dir"
host_json="$out_dir/host.json"
jsonl="$out_dir/runs.jsonl"
summary="$out_dir/summary.md"
: >"$jsonl"
bench_write_host_json "$host_json" "${COVE:-./cove}"
bench_summary_header "$summary" "cove vs UTM benchmark" "$jsonl" "$host_json"

if ! command -v "$tool" >/dev/null 2>&1; then
	bench_emit_not_measured "$jsonl" cove-vs-utm "$tool" "utmctl CLI not found on PATH"
	cat >>"$summary" <<EOF
| Tool | Status | Reason |
|---|---|---|
| UTM | not measured | \`utmctl\` CLI not found on PATH |
EOF
	exit 0
fi

version=$("$tool" --version 2>&1 || "$tool" version 2>&1 || true)
if [ -z "$version" ]; then
	version=unknown
fi
bench_emit_not_measured "$jsonl" cove-vs-utm "$tool" "installed UTM CLI captured; comparable guest shell readiness path required"
cat >>"$summary" <<EOF
| Tool | Status | Version | Reason |
|---|---|---|---|
| UTM | not measured | \`$version\` | provide a prepared UTM VM and guest shell readiness path before publishing numbers |

Workload: \`$workload\`
EOF
