#!/bin/bash
# test_provision_e2e.sh - Automated end-to-end provisioning test for cove
#
# This script exercises the real install/provision/boot path:
# 1. Install macOS to a VM (optionally auto-download the latest IPSW)
# 2. Inject provisioning files
# 3. Boot the VM headless
# 4. Verify inside the guest that provisioning completed and the console user
#    is the provisioned account
#
# Prerequisites:
# - Apple Silicon host with virtualization support
# - Sufficient free space under HOME (~80 GB)
# - Time (~30-60 minutes for download + install + first boot)
#
# Usage:
#   ./test_provision_e2e.sh [OPTIONS]
#
# Options:
#   -ipsw PATH     Path to IPSW file (optional; downloads latest if omitted)
#   -user NAME     Username to create (default: testuser)
#   -password PWD  Password for user (default: test123)
#   -ssh-key PATH  SSH public key to inject (optional)
#   -home PATH     HOME to use for ~/.vz state (default: current HOME)
#   -vm NAME       VM name to use (default: e2e-provision)
#   -no-cleanup    Preserve the VM after the test
#   -skip-install  Skip installation, use existing VM
#   -v             Verbose output

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

# Default values
IPSW_PATH=""
TEST_USER="testuser"
TEST_PASSWORD="test123"
SSH_KEY_PATH=""
VZ_HOME="${HOME}"
VM_NAME="e2e-provision"
CLEANUP=true
SKIP_INSTALL=false
VERBOSE=false
RUN_PID=""
RUN_LOG=""
VERIFY_SCRIPT=""

# Parse arguments
while [[ $# -gt 0 ]]; do
	case $1 in
		-ipsw)
			IPSW_PATH="$2"
			shift 2
			;;
		-user)
			TEST_USER="$2"
			shift 2
			;;
		-password)
			TEST_PASSWORD="$2"
			shift 2
			;;
		-ssh-key)
			SSH_KEY_PATH="$2"
			shift 2
			;;
		-home)
			VZ_HOME="$2"
			shift 2
			;;
		-vm)
			VM_NAME="$2"
			shift 2
			;;
		-no-cleanup)
			CLEANUP=false
			shift
			;;
		-skip-install)
			SKIP_INSTALL=true
			shift
			;;
		-v)
			VERBOSE=true
			shift
			;;
		-h|--help)
			head -36 "$0" | tail -31
			exit 0
			;;
		*)
			echo "Unknown option: $1"
			exit 1
			;;
	esac
done

VM_PATH="$VZ_HOME/.vz/vms/$VM_NAME"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log() {
	echo -e "${BLUE}[$(date '+%H:%M:%S')]${NC} $1"
}

error() {
	echo -e "${RED}[ERROR]${NC} $1" >&2
}

success() {
	echo -e "${GREEN}[SUCCESS]${NC} $1"
}

warn() {
	echo -e "${YELLOW}[WARN]${NC} $1"
}

run_vz() {
	env HOME="$VZ_HOME" VZMAC_NO_MACGO=1 ./cove -vm "$VM_NAME" "$@"
}

run_vz_root() {
	sudo env HOME="$VZ_HOME" VZMAC_NO_MACGO=1 ./cove -vm "$VM_NAME" "$@"
}

require_free_space() {
	local avail_kb
	avail_kb=$(df -Pk "$VZ_HOME" | awk 'NR==2 { print $4 }')
	if [[ -z "$avail_kb" ]]; then
		error "could not determine free space for HOME=$VZ_HOME"
		exit 1
	fi
	if (( avail_kb < 80 * 1024 * 1024 )); then
		error "HOME=$VZ_HOME has less than 80 GB free"
		echo "Use -home PATH on a roomier volume before running an end-to-end install."
		exit 1
	fi
}

stop_vm() {
	if [[ -n "$RUN_PID" ]] && kill -0 "$RUN_PID" 2>/dev/null; then
		log "Stopping VM..."
		run_vz ctl -wait 2m agent-shutdown >/dev/null 2>&1 || \
			run_vz ctl request-stop >/dev/null 2>&1 || true
		for _ in $(seq 1 60); do
			if ! kill -0 "$RUN_PID" 2>/dev/null; then
				break
			fi
			sleep 2
		done
		if kill -0 "$RUN_PID" 2>/dev/null; then
			kill "$RUN_PID" >/dev/null 2>&1 || true
		fi
		wait "$RUN_PID" 2>/dev/null || true
		RUN_PID=""
	fi
}

# Cleanup function
cleanup() {
	stop_vm
	if [[ -n "$VERIFY_SCRIPT" && -f "$VERIFY_SCRIPT" ]]; then
		rm -f "$VERIFY_SCRIPT"
	fi
	if [[ "$CLEANUP" == true ]]; then
		log "Cleaning up $VM_PATH..."
		rm -rf "$VM_PATH" 2>/dev/null || true
	else
		warn "Skipping cleanup (VM preserved at $VM_PATH)"
		if [[ -n "$RUN_LOG" && -f "$RUN_LOG" ]]; then
			warn "Last run log: $RUN_LOG"
		fi
	fi
}

# Set trap for cleanup on exit
trap cleanup EXIT

# Build
log "Building cove..."
go build -o cove . || { error "build failed"; exit 1; }

mkdir -p "$VZ_HOME"
require_free_space

# Check explicit IPSW path if provided.
if [[ -n "$IPSW_PATH" && ! -f "$IPSW_PATH" ]]; then
	error "IPSW file not found: $IPSW_PATH"
	exit 1
fi

if [[ "$SKIP_INSTALL" == false && -z "$IPSW_PATH" ]]; then
	log "No IPSW path provided; the installer will download the latest supported image into $VZ_HOME/.vz/cache"
fi

echo ""
echo "=============================================="
echo "  cove Automated Provisioning E2E Test"
echo "=============================================="
echo ""
echo "Configuration:"
echo "  HOME:     $VZ_HOME"
echo "  VM:       $VM_NAME"
echo "  VM Path:  $VM_PATH"
echo "  User:     $TEST_USER"
echo "  Password: $TEST_PASSWORD"
if [[ -n "$SSH_KEY_PATH" ]]; then
	echo "  SSH Key:  $SSH_KEY_PATH"
fi
echo "  IPSW:     ${IPSW_PATH:-<auto-download latest>}"
echo ""

# Step 1: Clean previous VM
if [[ "$SKIP_INSTALL" == false ]]; then
	log "Step 1: Cleaning previous VM..."
	rm -rf "$VM_PATH" 2>/dev/null || true
fi

# Step 2: Install macOS
if [[ "$SKIP_INSTALL" == false ]]; then
	log "Step 2: Installing macOS (this may take a while)..."
	INSTALL_ARGS=(install -headless)
	if [[ -n "$IPSW_PATH" ]]; then
		INSTALL_ARGS+=(-ipsw "$IPSW_PATH")
	fi
	if [[ "$VERBOSE" == true ]]; then
		VZ_DEBUG_INSTALL=1 run_vz "${INSTALL_ARGS[@]}"
	else
		run_vz "${INSTALL_ARGS[@]}"
	fi
	success "macOS installation complete"
else
	log "Step 2: Skipping installation (using existing VM)"
	if [[ ! -f "$VM_PATH/disk.img" ]]; then
		error "no existing VM found at $VM_PATH/disk.img"
		exit 1
	fi
fi

# Step 3: Stage provisioning
log "Step 3: Staging provisioning files..."
INJECT_ARGS=(inject -user "$TEST_USER" -password "$TEST_PASSWORD" -skip-setup-assistant -stage-only)
if [[ -n "$SSH_KEY_PATH" ]]; then
	INJECT_ARGS+=(-ssh-key "$SSH_KEY_PATH")
fi
if [[ "$VERBOSE" == true ]]; then
	INJECT_ARGS+=(-v)
fi
run_vz "${INJECT_ARGS[@]}"
success "Provisioning files staged"

# Step 4: Apply provisioning with host privileges
log "Step 4: Applying provisioning files to VM disk..."
run_vz_root inject -apply
success "Provisioning files applied"

# Step 5: Create verification script
log "Step 5: Preparing guest verification..."
VERIFY_SCRIPT="$(mktemp -t vz-e2e-verify.XXXXXX)"
cat >"$VERIFY_SCRIPT" <<EOF
# e2e-verify -- validate provisioning and auto-login
guest-wait 20m
guest-ping
guest-shell verify.sh

-- verify.sh --
#!/bin/bash
set -eu
test -x /usr/local/bin/vz-agent
test -f /private/var/db/.vz-provisioned
test -f /private/var/db/.AppleSetupDone
test -d /Users/$TEST_USER
/usr/bin/id $TEST_USER
/usr/bin/dscl . -read /Groups/admin GroupMembership | /usr/bin/grep -wq $TEST_USER
test "\$(/usr/bin/stat -f %Su /dev/console)" = "$TEST_USER"
EOF
if [[ -n "$SSH_KEY_PATH" ]]; then
	printf '%s\n' 'test -s /Users/'"$TEST_USER"'/.ssh/authorized_keys' >>"$VERIFY_SCRIPT"
fi

# Step 6: Boot the VM headless
log "Step 6: Booting VM headless..."
RUN_LOG="$(mktemp -t vz-e2e-run.XXXXXX.log)"
if [[ "$VERBOSE" == true ]]; then
	env HOME="$VZ_HOME" VZMAC_NO_MACGO=1 ./cove -vm "$VM_NAME" run -headless -verbose >"$RUN_LOG" 2>&1 &
else
	env HOME="$VZ_HOME" VZMAC_NO_MACGO=1 ./cove -vm "$VM_NAME" run -headless >"$RUN_LOG" 2>&1 &
fi
RUN_PID=$!

# Step 7: Verify provisioning completed inside the guest
log "Step 7: Verifying guest state..."
if run_vz vzscript run -timeout 2m "$VERIFY_SCRIPT"; then
	success "Guest verification passed"
else
	error "guest verification failed"
	if [[ -f "$RUN_LOG" ]]; then
		echo ""
		echo "Last VM log lines:"
		tail -50 "$RUN_LOG" || true
	fi
	exit 1
fi

# Step 8: Shut down the VM cleanly
log "Step 8: Shutting down VM..."
stop_vm

success "End-to-end test complete"
