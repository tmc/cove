#!/bin/sh
set -eu

bench_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
. "$bench_dir/internal/harness.sh"

cove=${COVE:-./cove}
parent=
levels=1,2,4,8,16
out_dir=
keep=false
prefix=cove-bench-parallel

usage() {
	cat >&2 <<EOF
usage: bench/parallel-fork/run.sh --parent VM [--levels 1,2,4,8,16] [--out DIR] [--cove ./cove] [--keep]
EOF
}

while [ "$#" -gt 0 ]; do
	case "$1" in
	--parent) parent=$2; shift 2 ;;
	--levels) levels=$2; shift 2 ;;
	--out) out_dir=$2; shift 2 ;;
	--cove) cove=$2; shift 2 ;;
	--prefix) prefix=$2; shift 2 ;;
	--keep) keep=true; shift ;;
	-h|--help) usage; exit 0 ;;
	*) usage; exit 2 ;;
	esac
done

if [ -z "$out_dir" ]; then
	out_dir=$(bench_default_out_dir parallel-fork)
fi
mkdir -p "$out_dir"
host_json="$out_dir/host.json"
jsonl="$out_dir/runs.jsonl"
summary="$out_dir/summary.md"
: >"$jsonl"
bench_write_host_json "$host_json" "$cove"

bench_summary_header "$summary" "parallel-fork benchmark" "$jsonl" "$host_json"

if [ -z "$parent" ]; then
	bench_emit_skip "$jsonl" parallel-fork "missing --parent"
	cat >>"$summary" <<EOF
Status: skip

Reason: missing \`--parent\`.
EOF
	exit 0
fi

if [ ! -d "$HOME/.vz/vms/$parent" ]; then
	bench_emit_skip "$jsonl" parallel-fork "parent VM not found: $parent"
	cat >>"$summary" <<EOF
Status: skip

Reason: parent VM \`$parent\` not found under \`~/.vz/vms\`.
EOF
	exit 0
fi

cat >>"$summary" <<EOF
| Level | Status | Total wall | Notes |
|---:|---|---:|---|
EOF

old_ifs=$IFS
IFS=,
for level in $levels; do
	IFS=$old_ifs
	start=$(bench_ms_now)
	children=
	pids=
	status=ok
	for i in $(seq 1 "$level"); do
		child="$prefix-$parent-$level-$i-$$"
		children="$children $child"
		(
			child_start=$(bench_ms_now)
			stdout="$out_dir/$child.stdout"
			stderr="$out_dir/$child.stderr"
			exit_code=0
			if "$cove" fork "$parent" "$child" >"$stdout" 2>"$stderr"; then
				exit_code=0
			else
				exit_code=$?
			fi
			child_end=$(bench_ms_now)
			printf '{"timestamp":"%s","benchmark":"parallel-fork","parent":"%s","child":"%s","level":%s,"duration_ms":%s,"exit_code":%s,"stdout":"%s","stderr":"%s","status":"%s"}\n' \
				"$(bench_timestamp)" \
				"$(bench_json_escape "$parent")" \
				"$(bench_json_escape "$child")" \
				"$level" \
				"$((child_end - child_start))" \
				"$exit_code" \
				"$(bench_json_escape "$stdout")" \
				"$(bench_json_escape "$stderr")" \
				"$([ "$exit_code" -eq 0 ] && printf ok || printf fail)" >>"$jsonl"
			exit "$exit_code"
		) &
		pids="$pids $!"
	done
	for pid in $pids; do
		if ! wait "$pid"; then
			status=fail
		fi
	done
	end=$(bench_ms_now)
	if [ "$keep" != true ]; then
		for child in $children; do
			printf 'y\n' | "$cove" vm delete "$child" >/dev/null 2>&1 || true
		done
	fi
	printf '| %s | %s | %sms | parent `%s` |\n' "$level" "$status" "$((end - start))" "$parent" >>"$summary"
	IFS=,
done
IFS=$old_ifs
