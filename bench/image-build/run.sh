#!/bin/sh
set -eu

bench_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
. "$bench_dir/internal/harness.sh"

cove=${COVE:-./cove}
from_vm=
base=
name=cove-bench-build
script=
tag=
out_dir=
runs=1
dry_run=false
compact=targeted

usage() {
	cat >&2 <<EOF
usage:
  bench/image-build/run.sh --from-vm VM --tag REF [--runs N] [--out DIR]
  bench/image-build/run.sh --base DIR --script STEP --tag REF [--name NAME] [--dry-run] [--out DIR]
EOF
}

while [ "$#" -gt 0 ]; do
	case "$1" in
	--from-vm) from_vm=$2; shift 2 ;;
	--base) base=$2; shift 2 ;;
	--name) name=$2; shift 2 ;;
	--script) script=$2; shift 2 ;;
	--tag) tag=$2; shift 2 ;;
	--out) out_dir=$2; shift 2 ;;
	--runs) runs=$2; shift 2 ;;
	--cove) cove=$2; shift 2 ;;
	--compact) compact=$2; shift 2 ;;
	--dry-run) dry_run=true; shift ;;
	-h|--help) usage; exit 0 ;;
	*) usage; exit 2 ;;
	esac
done

if [ -z "$out_dir" ]; then
	out_dir=$(bench_default_out_dir image-build)
fi
mkdir -p "$out_dir"
host_json="$out_dir/host.json"
jsonl="$out_dir/runs.jsonl"
summary="$out_dir/summary.md"
: >"$jsonl"
bench_write_host_json "$host_json" "$cove"
bench_summary_header "$summary" "image-build benchmark" "$jsonl" "$host_json"

if [ -n "$from_vm" ]; then
	if [ -z "$tag" ]; then
		bench_emit_skip "$jsonl" image-build "missing --tag for --from-vm"
		printf 'Status: skip\n\nReason: missing `--tag` for image snapshot mode.\n' >>"$summary"
		exit 0
	fi
	if [ ! -d "$HOME/.vz/vms/$from_vm" ]; then
		bench_emit_skip "$jsonl" image-build "source VM not found: $from_vm"
		printf 'Status: skip\n\nReason: source VM `%s` not found under `~/.vz/vms`.\n' "$from_vm" >>"$summary"
		exit 0
	fi
	cat >>"$summary" <<EOF
| Iteration | Mode | Status | Wall |
|---:|---|---|---:|
EOF
	i=1
	while [ "$i" -le "$runs" ]; do
		run_tag="$tag"
		if [ "$runs" -gt 1 ]; then
			run_tag="$tag-$i"
		fi
		stdout="$out_dir/image-build-$i.stdout"
		stderr="$out_dir/image-build-$i.stderr"
		start=$(bench_ms_now)
		status=ok
		exit_code=0
		if ! "$cove" image build -from "$from_vm" -tag "$run_tag" >"$stdout" 2>"$stderr"; then
			exit_code=$?
			status=fail
		fi
		end=$(bench_ms_now)
		inspect="$out_dir/image-inspect-$i.json"
		"$cove" image inspect -json "$run_tag" >"$inspect" 2>/dev/null || true
		printf '{"timestamp":"%s","benchmark":"image-build","mode":"image-build","source_vm":"%s","tag":"%s","iteration":%s,"duration_ms":%s,"exit_code":%s,"status":"%s","inspect":"%s","stdout":"%s","stderr":"%s"}\n' \
			"$(bench_timestamp)" "$(bench_json_escape "$from_vm")" "$(bench_json_escape "$run_tag")" "$i" "$((end - start))" "$exit_code" "$status" "$(bench_json_escape "$inspect")" "$(bench_json_escape "$stdout")" "$(bench_json_escape "$stderr")" >>"$jsonl"
		printf '| %s | image build | %s | %sms |\n' "$i" "$status" "$((end - start))" >>"$summary"
		i=$((i + 1))
	done
	exit 0
fi

if [ -z "$base" ] || [ -z "$script" ] || [ -z "$tag" ]; then
	bench_emit_skip "$jsonl" image-build "missing --base, --script, or --tag"
	printf 'Status: skip\n\nReason: build mode requires `--base`, `--script`, and `--tag`.\n' >>"$summary"
	exit 0
fi
if [ ! -d "$base" ]; then
	bench_emit_skip "$jsonl" image-build "local base directory not found: $base"
	printf 'Status: skip\n\nReason: local base directory `%s` not found.\n' "$base" >>"$summary"
	exit 0
fi

cat >>"$summary" <<EOF
| Iteration | Mode | Status | Wall |
|---:|---|---|---:|
EOF

i=1
while [ "$i" -le "$runs" ]; do
	stdout="$out_dir/cove-build-$i.stdout"
	stderr="$out_dir/cove-build-$i.stderr"
	start=$(bench_ms_now)
	args="build $name --base $base --script $script --tag $tag --compact $compact"
	if [ "$dry_run" = true ]; then
		args="$args --dry-run"
	fi
	status=ok
	exit_code=0
	# shellcheck disable=SC2086
	if ! "$cove" $args >"$stdout" 2>"$stderr"; then
		exit_code=$?
		status=fail
	fi
	end=$(bench_ms_now)
	printf '{"timestamp":"%s","benchmark":"image-build","mode":"cove-build","base":"%s","script":"%s","tag":"%s","compact":"%s","iteration":%s,"duration_ms":%s,"exit_code":%s,"status":"%s","stdout":"%s","stderr":"%s"}\n' \
		"$(bench_timestamp)" "$(bench_json_escape "$base")" "$(bench_json_escape "$script")" "$(bench_json_escape "$tag")" "$(bench_json_escape "$compact")" "$i" "$((end - start))" "$exit_code" "$status" "$(bench_json_escape "$stdout")" "$(bench_json_escape "$stderr")" >>"$jsonl"
	printf '| %s | cove build | %s | %sms |\n' "$i" "$status" "$((end - start))" >>"$summary"
	i=$((i + 1))
done
