#!/bin/sh
set -eu

bench_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
. "$bench_dir/internal/harness.sh"

out_dir=
tool=lume
workload=${WORKLOAD:-guest-ready-and-command}

usage() {
	echo "usage: bench/cove-vs-lume/run.sh [--out DIR]" >&2
}

while [ "$#" -gt 0 ]; do
	case "$1" in
	--out) out_dir=$2; shift 2 ;;
	-h|--help) usage; exit 0 ;;
	*) usage; exit 2 ;;
	esac
done

if [ -z "$out_dir" ]; then
	out_dir=$(bench_default_out_dir cove-vs-lume)
fi
mkdir -p "$out_dir"
host_json="$out_dir/host.json"
jsonl="$out_dir/runs.jsonl"
summary="$out_dir/summary.md"
: >"$jsonl"
bench_write_host_json "$host_json" "${COVE:-./cove}"
bench_summary_header "$summary" "cove vs Lume benchmark" "$jsonl" "$host_json"

if ! command -v "$tool" >/dev/null 2>&1; then
	bench_emit_not_measured "$jsonl" cove-vs-lume "$tool" "lume CLI not found on PATH"
	cat >>"$summary" <<EOF
| Tool | Status | Reason |
|---|---|---|
| Lume | not measured | \`lume\` CLI not found on PATH |
EOF
	exit 0
fi

version=$("$tool" --version 2>&1 || "$tool" version 2>&1 || true)
if [ -z "$version" ]; then
	version=unknown
fi
bench_emit_not_measured "$jsonl" cove-vs-lume "$tool" "installed Lume version captured; comparable VM/image arguments required"
cat >>"$summary" <<EOF
| Tool | Status | Version | Reason |
|---|---|---|---|
| Lume | not measured | \`$version\` | provide a prepared Lume VM/image and fill the protocol before publishing numbers |

Workload: \`$workload\`
EOF
