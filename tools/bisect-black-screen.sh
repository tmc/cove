#!/usr/bin/env bash

set -euo pipefail

usage() {
	cat <<'EOF'
Usage: bisect-black-screen.sh (-vm NAME | -vm-dir DIR) [-mode run|selector] [-launch-order window-first|start-first] [-runtime-profile full|minimal] [-- extra go run args]

Launch cove for manual black-screen classification during git bisect.

The script:
- forces a cold start by deleting saved suspend state files
- refuses to run if the VM control socket is active
- launches through `go run .`, which keeps macgo in the path
- prompts on /dev/tty for good/bad/skip after the app exits

Exit codes are suitable for `git bisect run`:
- 0: good
- 1: bad
- 125: skip
- 130: abort

Examples:
  bisect-black-screen.sh -vm codex-e2e
  bisect-black-screen.sh -vm codex-e2e -launch-order start-first
  bisect-black-screen.sh -vm codex-e2e -runtime-profile minimal
  bisect-black-screen.sh -vm codex-e2e -launch-order start-first -runtime-profile minimal
  bisect-black-screen.sh -vm codex-e2e -- -display 1920x1200
  bisect-black-screen.sh -vm-dir "$HOME/.vz/vms/codex-e2e"
EOF
}

die() {
	echo "error: $*" >&2
	exit 125
}

prompt_classification() {
	local tty=/dev/tty
	if [[ ! -e "$tty" ]]; then
		die "no /dev/tty available for manual classification"
	fi

	cat >"$tty" <<'EOF'

Classify this revision:
- good: the VM window renders guest pixels after launch
- bad: the VM window stays black or never renders guest pixels
- skip: build/launch worked poorly enough that the result is inconclusive
- quit: abort the bisect
EOF

	while true; do
		printf "Result [good/bad/skip/quit]: " >"$tty"
		local answer
		if ! IFS= read -r answer <"$tty"; then
			exit 130
		fi
		case "${answer}" in
		g|G|good|GOOD|Good)
			exit 0
			;;
		b|B|bad|BAD|Bad)
			exit 1
			;;
		s|S|skip|SKIP|Skip)
			exit 125
			;;
		q|Q|quit|QUIT|Quit)
			exit 130
			;;
		esac
	done
}

vm_name=""
vm_dir=""
mode="run"
launch_order="window-first"
runtime_profile="full"
extra_args=()

while [[ $# -gt 0 ]]; do
	case "$1" in
	-vm)
		[[ $# -ge 2 ]] || die "missing value for -vm"
		vm_name="$2"
		shift 2
		;;
	-vm-dir)
		[[ $# -ge 2 ]] || die "missing value for -vm-dir"
		vm_dir="$2"
		shift 2
		;;
	-mode)
		[[ $# -ge 2 ]] || die "missing value for -mode"
		mode="$2"
		shift 2
		;;
	-launch-order)
		[[ $# -ge 2 ]] || die "missing value for -launch-order"
		launch_order="$2"
		shift 2
		;;
	-runtime-profile)
		[[ $# -ge 2 ]] || die "missing value for -runtime-profile"
		runtime_profile="$2"
		shift 2
		;;
	-h|--help)
		usage
		exit 0
		;;
	--)
		shift
		extra_args=("$@")
		break
		;;
	*)
		die "unknown argument: $1"
		;;
	esac
done

if [[ -n "$vm_name" && -n "$vm_dir" ]]; then
	die "use either -vm or -vm-dir, not both"
fi
if [[ -z "$vm_name" && -z "$vm_dir" ]]; then
	die "one of -vm or -vm-dir is required"
fi
if [[ "$mode" != "run" && "$mode" != "selector" ]]; then
	die "mode must be run or selector"
fi
if [[ "$launch_order" != "window-first" && "$launch_order" != "start-first" ]]; then
	die "launch order must be window-first or start-first"
fi
if [[ "$runtime_profile" != "full" && "$runtime_profile" != "minimal" ]]; then
	die "runtime profile must be full or minimal"
fi
if [[ "$(uname -s)" != "Darwin" ]]; then
	die "this helper only works on macOS"
fi

if [[ -z "$vm_dir" ]]; then
	vm_dir="$HOME/.vz/vms/$vm_name"
fi
if [[ ! -d "$vm_dir" ]]; then
	die "vm directory not found: $vm_dir"
fi

sock="$vm_dir/control.sock"
if [[ -S "$sock" ]] && lsof "$sock" >/dev/null 2>&1; then
	die "control socket is active at $sock; stop the running VM first"
fi

rm -f \
	"$sock" \
	"$vm_dir/suspend.vmstate" \
	"$vm_dir/suspend.config.json"

cmd=(go run .)
case "$mode" in
run)
	# Use the direct run path so the bisect isolates display startup instead
	# of selector behavior. Older revisions all support -vm-dir and the run
	# subcommand.
	cmd+=(-vm-dir "$vm_dir" -launch-order "$launch_order" -runtime-profile "$runtime_profile" run -gui -serial none)
		;;
	selector)
	# No subcommand here: allow the default GUI path to show the selector.
	cmd+=(-vm-dir "$vm_dir" -launch-order "$launch_order" -runtime-profile "$runtime_profile")
		;;
esac
cmd+=("${extra_args[@]}")

cat <<EOF
Launching:
  ${cmd[*]}

Approach:
- mode: $mode
- launch-order: $launch_order
- runtime-profile: $runtime_profile

Expected classification:
- good: VM display renders guest output
- bad: VM display stays black

Close the app after you have inspected the window.
EOF

status=0
if "${cmd[@]}"; then
	status=0
else
	status=$?
fi
if [[ "$status" -ne 0 ]]; then
	echo "skip: launch exited with status $status" >&2
	exit 125
fi

prompt_classification
