#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 2 ]]; then
	echo "usage: $0 <cove-binary> <fresh-vm-name>" >&2
	exit 64
fi

COVE=$1
VM=$2

if [[ ! -x "$COVE" ]]; then
	echo "error: cove binary is not executable: $COVE" >&2
	exit 66
fi

run() {
	echo "+ $*" >&2
	"$@"
}

expect() {
	local pattern=$1
	shift
	local out
	echo "+ $*" >&2
	out=$("$@" 2>&1)
	printf '%s\n' "$out"
	if ! grep -E "$pattern" <<<"$out" >/dev/null; then
		echo "error: output did not match /$pattern/" >&2
		exit 1
	fi
}

cleanup_note() {
	cat >&2 <<EOF

Smoke test stopped before cleanup completed.
Inspect or remove the VM manually:
  $COVE vm delete $VM
EOF
}
trap cleanup_note ERR

expect 'cove ' "$COVE" version

run "$COVE" -vm "$VM" install -force
run "$COVE" -vm "$VM" up -force
expect 'desktop|login|setup_assistant|unknown' "$COVE" -vm "$VM" ctl detect
expect 'version|connected|agent|status' "$COVE" -vm "$VM" ctl agent-status

# Image snapshots require a stopped VM.
run "$COVE" -vm "$VM" ctl stop
sleep 5
expect 'image|manifest|built|created|saved' "$COVE" image build -from "$VM" -tag "smoke/$VM:latest"
expect "smoke/$VM|REF|IMAGE|latest" "$COVE" image list

# Local fleet aggregation is allowed to be empty on a fresh workstation, but the
# command must succeed.
run "$COVE" fleet vm ls

# Bring the VM back up for logs and a final control-plane check.
run "$COVE" -vm "$VM" run -headless
expect 'log|journal|Timestamp|--' "$COVE" logs "$VM"
expect 'state|running|paused|stopped' "$COVE" -vm "$VM" ctl status
run "$COVE" -vm "$VM" ctl stop

trap - ERR
echo "smoke test passed for $VM"
