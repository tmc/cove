#!/bin/bash
# test_provision_e2e.sh - End-to-end provisioning test for vz-macos
#
# This script tests the full provisioning workflow:
# 1. Install macOS to a VM
# 2. Inject provisioning files
# 3. Run VM and verify user creation
#
# Prerequisites:
# - macOS IPSW file (download with: ./vz-macos download-ipsw)
# - Sufficient disk space (~70GB for VM)
# - Time (~15-30 minutes for installation)
#
# Usage:
#   ./test_provision_e2e.sh [OPTIONS]
#
# Options:
#   -ipsw PATH     Path to IPSW file (required)
#   -user NAME     Username to create (default: testuser)
#   -password PWD  Password for user (default: test123)
#   -ssh-key PATH  SSH public key to inject (optional)
#   -no-cleanup    Don't cleanup VM after test
#   -skip-install  Skip installation, use existing VM
#   -v             Verbose output

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

# Default values
IPSW_PATH=""
TEST_USER="testuser"
TEST_PASSWORD="test123"
SSH_KEY_PATH=""
CLEANUP=true
SKIP_INSTALL=false
VERBOSE=false

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
            head -30 "$0" | tail -25
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            exit 1
            ;;
    esac
done

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

# Cleanup function
cleanup() {
    if [ "$CLEANUP" = true ]; then
        log "Cleaning up..."
        rm -rf ./vm 2>/dev/null || true
    else
        warn "Skipping cleanup (VM preserved in ./vm)"
    fi
}

# Set trap for cleanup on exit
trap cleanup EXIT

# Build
log "Building vz-macos..."
go build || { error "Build failed"; exit 1; }

# Check IPSW
if [ "$SKIP_INSTALL" = false ]; then
    if [ -z "$IPSW_PATH" ]; then
        error "IPSW path required. Use -ipsw PATH or download with:"
        echo "  ./vz-macos download-ipsw"
        exit 1
    fi

    if [ ! -f "$IPSW_PATH" ]; then
        error "IPSW file not found: $IPSW_PATH"
        exit 1
    fi
fi

echo ""
echo "=============================================="
echo "  vz-macos End-to-End Provisioning Test"
echo "=============================================="
echo ""
echo "Configuration:"
echo "  User:     $TEST_USER"
echo "  Password: $TEST_PASSWORD"
if [ -n "$SSH_KEY_PATH" ]; then
    echo "  SSH Key:  $SSH_KEY_PATH"
fi
echo "  IPSW:     ${IPSW_PATH:-<skip install>}"
echo ""

# Step 1: Clean previous VM
if [ "$SKIP_INSTALL" = false ]; then
    log "Step 1: Cleaning previous VM..."
    rm -rf ./vm 2>/dev/null || true
    mkdir -p ./vm
fi

# Step 2: Install macOS
if [ "$SKIP_INSTALL" = false ]; then
    log "Step 2: Installing macOS (this takes ~15-30 minutes)..."
    if [ "$VERBOSE" = true ]; then
        VZ_DEBUG_INSTALL=1 ./vz-macos install -ipsw "$IPSW_PATH"
    else
        ./vz-macos install -ipsw "$IPSW_PATH"
    fi
    success "macOS installation complete"
else
    log "Step 2: Skipping installation (using existing VM)"
    if [ ! -f ./vm/disk.img ]; then
        error "No existing VM found at ./vm/disk.img"
        exit 1
    fi
fi

# Step 3: Inject provisioning
log "Step 3: Injecting provisioning files..."

INJECT_ARGS="-user $TEST_USER -password $TEST_PASSWORD -skip-setup-assistant"
if [ -n "$SSH_KEY_PATH" ]; then
    INJECT_ARGS="$INJECT_ARGS -ssh-key $SSH_KEY_PATH"
fi
if [ "$VERBOSE" = true ]; then
    INJECT_ARGS="$INJECT_ARGS -v"
fi

./vz-macos inject $INJECT_ARGS
success "Provisioning files injected"

# Step 4: Instructions for manual verification
echo ""
echo "=============================================="
echo "  Manual Verification Required"
echo "=============================================="
echo ""
echo "Run the VM with:"
echo "  ./vz-macos run -gui"
echo ""
echo "Expected behavior:"
echo "  1. VM boots directly to login screen (Setup Assistant skipped)"
echo "  2. User '$TEST_USER' should auto-login"
echo "  3. After login, verify in Terminal:"
echo "     - whoami              # Should show: $TEST_USER"
echo "     - id                  # Should show uid=501"
echo "     - groups              # Should include: admin staff"
if [ -n "$SSH_KEY_PATH" ]; then
    echo "     - cat ~/.ssh/authorized_keys  # Should contain your key"
fi
echo ""
echo "To test SSH access (if SSH key was injected):"
echo "  1. Enable Remote Login in System Preferences > Sharing"
echo "  2. Get VM IP: ifconfig | grep 'inet '"
echo "  3. SSH: ssh $TEST_USER@<VM_IP>"
echo ""

# Step 5: Optionally run the VM
read -p "Start VM now for verification? (y/N) " -n 1 -r
echo
if [[ $REPLY =~ ^[Yy]$ ]]; then
    log "Starting VM..."
    CLEANUP=false  # Don't cleanup if user is testing
    ./vz-macos run -gui
fi

success "End-to-end test complete"
