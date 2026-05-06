#!/bin/sh
set -eu

bench_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
. "$bench_dir/internal/harness.sh"

out_dir=
tool=cirrus
workload=${WORKLOAD:-guest-ready-and-command}

usage() {
	echo "usage: bench/cove-vs-cirrus/run.sh [--out DIR]" >&2
}

while [ "$#" -gt 0 ]; do
	case "$1" in
	--out) out_dir=$2; shift 2 ;;
	-h|--help) usage; exit 0 ;;
	*) usage; exit 2 ;;
	esac
done

if [ -z "$out_dir" ]; then
	out_dir=$(bench_default_out_dir cove-vs-cirrus)
fi
mkdir -p "$out_dir"
host_json="$out_dir/host.json"
jsonl="$out_dir/runs.jsonl"
summary="$out_dir/summary.md"
: >"$jsonl"
bench_write_host_json "$host_json" "${COVE:-./cove}"
bench_summary_header "$summary" "cove vs Cirrus benchmark" "$jsonl" "$host_json"

if ! command -v "$tool" >/dev/null 2>&1; then
	bench_emit_not_measured "$jsonl" cove-vs-cirrus "$tool" "cirrus CLI not found on PATH"
	cat >>"$summary" <<EOF
| Tool | Status | Reason |
|---|---|---|
| Cirrus | not measured | \`cirrus\` CLI not found on PATH; Cirrus CI shutdown is announced for 2026-06-01 |
EOF
	exit 0
fi

version=$("$tool" --version 2>&1 || "$tool" version 2>&1 || true)
if [ -z "$version" ]; then
	version=unknown
fi
bench_emit_not_measured "$jsonl" cove-vs-cirrus "$tool" "installed Cirrus CLI captured; hosted service or Tart-backed task credentials required"
cat >>"$summary" <<EOF
| Tool | Status | Version | Reason |
|---|---|---|---|
| Cirrus | not measured | \`$version\` | provide a runnable Cirrus/Tart task before publishing numbers |

Workload: \`$workload\`
EOF
