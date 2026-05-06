#!/bin/sh
set -eu

bench_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
. "$bench_dir/internal/harness.sh"

cove=${COVE:-./cove}
image=
runs=3
out_dir=
prefix=cove-bench-agent
timeout_s=300
keep=false

usage() {
	cat >&2 <<EOF
usage: bench/boot-to-agent/run.sh --image REF [--runs N] [--timeout 300] [--out DIR] [--cove ./cove] [--keep]
EOF
}

while [ "$#" -gt 0 ]; do
	case "$1" in
	--image) image=$2; shift 2 ;;
	--runs) runs=$2; shift 2 ;;
	--timeout) timeout_s=$2; shift 2 ;;
	--out) out_dir=$2; shift 2 ;;
	--cove) cove=$2; shift 2 ;;
	--prefix) prefix=$2; shift 2 ;;
	--keep) keep=true; shift ;;
	-h|--help) usage; exit 0 ;;
	*) usage; exit 2 ;;
	esac
done

if [ -z "$out_dir" ]; then
	out_dir=$(bench_default_out_dir boot-to-agent)
fi
mkdir -p "$out_dir"
host_json="$out_dir/host.json"
jsonl="$out_dir/runs.jsonl"
summary="$out_dir/summary.md"
: >"$jsonl"
bench_write_host_json "$host_json" "$cove"
bench_summary_header "$summary" "boot-to-agent benchmark" "$jsonl" "$host_json"

if [ -z "$image" ]; then
	bench_emit_skip "$jsonl" boot-to-agent "missing --image"
	cat >>"$summary" <<EOF
Status: skip

Reason: missing \`--image\`.
EOF
	exit 0
fi

if ! "$cove" image verify -quiet "$image" >/dev/null 2>&1; then
	bench_emit_skip "$jsonl" boot-to-agent "image not found or invalid: $image"
	cat >>"$summary" <<EOF
Status: skip

Reason: image \`$image\` was not found or did not pass \`cove image verify -quiet\`.
EOF
	exit 0
fi

cat >>"$summary" <<EOF
| Iteration | Status | Agent ready | VM |
|---:|---|---:|---|
EOF

i=1
while [ "$i" -le "$runs" ]; do
	child="$prefix-$i-$$"
	stdout="$out_dir/$child.run.stdout"
	stderr="$out_dir/$child.run.stderr"
	ready_json="$out_dir/$child.ready.json"
	start=$(bench_ms_now)
	"$cove" run -fork-from "$image" -fork-name "$child" -ephemeral -headless -no-resume >"$stdout" 2>"$stderr" &
	run_pid=$!
	status=timeout
	ready_ms=0
	deadline=$((start + timeout_s * 1000))
	while [ "$(bench_ms_now)" -lt "$deadline" ]; do
		now=$(bench_ms_now)
		if "$cove" -vm "$child" ctl ready --require agent-ping --json --timeout 10s >"$ready_json" 2>>"$stderr"; then
			status=ok
			ready_ms=$((now - start))
			break
		fi
		if ! kill -0 "$run_pid" >/dev/null 2>&1; then
			status=run_exited
			break
		fi
		sleep 2
	done
	"$cove" -vm "$child" ctl stop >/dev/null 2>>"$stderr" || true
	wait "$run_pid" >/dev/null 2>&1 || true
	if [ "$keep" != true ]; then
		printf 'y\n' | "$cove" vm delete "$child" >/dev/null 2>&1 || true
	fi
	printf '{"timestamp":"%s","benchmark":"boot-to-agent","image_ref":"%s","vm":"%s","iteration":%s,"agent_ready_ms":%s,"status":"%s","ready_json":"%s","stdout":"%s","stderr":"%s"}\n' \
		"$(bench_timestamp)" \
		"$(bench_json_escape "$image")" \
		"$(bench_json_escape "$child")" \
		"$i" \
		"$ready_ms" \
		"$status" \
		"$(bench_json_escape "$ready_json")" \
		"$(bench_json_escape "$stdout")" \
		"$(bench_json_escape "$stderr")" >>"$jsonl"
	printf '| %s | %s | %sms | `%s` |\n' "$i" "$status" "$ready_ms" "$child" >>"$summary"
	i=$((i + 1))
done
bench_append_duration_stats "$summary" "$jsonl" agent_ready_ms "agent readiness"
