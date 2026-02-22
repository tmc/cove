#!/bin/bash
# test_inject.sh - Integration tests for vz-macos inject command
# Run: ./test_inject.sh
#
# These tests verify the inject command's validation and error handling.
# For full end-to-end testing, use test_provision_e2e.sh which requires a VM.

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

passed=0
failed=0

pass() {
    echo -e "${GREEN}PASS${NC}: $1"
    passed=$((passed + 1))
}

fail() {
    echo -e "${RED}FAIL${NC}: $1"
    failed=$((failed + 1))
}

skip() {
    echo -e "${YELLOW}SKIP${NC}: $1"
}

# Build first
echo "Building vz-macos..."
go build || { echo "Build failed"; exit 1; }

echo ""
echo "=== Inject Command Validation Tests ==="
echo ""

# Test: Missing required flags
echo "Test: Missing -user flag"
if ./vz-macos inject -password test123 2>&1 | grep -q "missing required flag: -user"; then
    pass "Rejects missing -user flag"
else
    fail "Should reject missing -user flag"
fi

echo "Test: Missing -password flag"
if ./vz-macos inject -user testuser 2>&1 | grep -q "missing required flag: -password"; then
    pass "Rejects missing -password flag"
else
    fail "Should reject missing -password flag"
fi

# Test: Invalid usernames
echo "Test: Username with slash"
if ./vz-macos inject -user "test/user" -password test123 2>&1 | grep -q "invalid username"; then
    pass "Rejects username with slash"
else
    fail "Should reject username with slash"
fi

echo "Test: Username with colon"
if ./vz-macos inject -user "test:user" -password test123 2>&1 | grep -q "invalid username"; then
    pass "Rejects username with colon"
else
    fail "Should reject username with colon"
fi

echo "Test: Reserved username 'root'"
if ./vz-macos inject -user "root" -password test123 2>&1 | grep -q "reserved by the system"; then
    pass "Rejects reserved username 'root'"
else
    fail "Should reject reserved username 'root'"
fi

echo "Test: Reserved username 'daemon'"
if ./vz-macos inject -user "daemon" -password test123 2>&1 | grep -q "reserved by the system"; then
    pass "Rejects reserved username 'daemon'"
else
    fail "Should reject reserved username 'daemon'"
fi

echo "Test: Empty username"
if ./vz-macos inject -user "" -password test123 2>&1 | grep -q "missing required flag: -user"; then
    pass "Rejects empty username"
else
    fail "Should reject empty username"
fi

# Test: Missing disk image
echo "Test: Missing disk image"
# This test requires no ./vm/disk.img to exist
if [ -f ./vm/disk.img ]; then
    skip "Disk image exists, cannot test missing disk error"
else
    if ./vz-macos inject -user testuser -password test123 2>&1 | grep -q "disk image not found\|hdiutil attach failed"; then
        pass "Reports missing/invalid disk image"
    else
        fail "Should report missing disk image"
    fi
fi

# Test: Help output
echo "Test: Help includes all options"
help_output=$(./vz-macos inject --help 2>&1)
missing_opts=""
for opt in "-user" "-password" "-admin" "-skip-setup-assistant" "-auto-login" "-ssh-key" "-plist" "-v"; do
    if ! echo "$help_output" | grep -q -- "$opt"; then
        missing_opts="$missing_opts $opt"
    fi
done
if [ -z "$missing_opts" ]; then
    pass "Help includes all expected options"
else
    fail "Help missing options:$missing_opts"
fi

echo ""
echo "=== SSH Key Validation Tests ==="
echo ""

# Create test SSH key files
TEST_DIR="/tmp/vz-test-$$"
mkdir -p "$TEST_DIR"

# Valid SSH keys
echo "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQ... test@example.com" > "$TEST_DIR/valid_rsa.pub"
echo "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA... test@example.com" > "$TEST_DIR/valid_ed25519.pub"
echo "ecdsa-sha2-nistp256 AAAAE2VjZHNh... test@example.com" > "$TEST_DIR/valid_ecdsa.pub"
echo "sk-ssh-ed25519@openssh.com AAAAGnN... test@example.com" > "$TEST_DIR/valid_sk.pub"

# Invalid SSH keys
echo "not-a-key" > "$TEST_DIR/invalid.pub"
echo "-----BEGIN RSA PRIVATE KEY-----" > "$TEST_DIR/private_key.pem"

# Note: These tests would need a mounted disk to fully run, so we skip them
skip "SSH key validation tests require a mounted VM disk (run test_provision_e2e.sh)"

# Cleanup
rm -rf "$TEST_DIR"

echo ""
echo "=== Summary ==="
echo -e "Passed: ${GREEN}$passed${NC}"
echo -e "Failed: ${RED}$failed${NC}"
echo ""

if [ $failed -gt 0 ]; then
    echo -e "${RED}Some tests failed${NC}"
    exit 1
else
    echo -e "${GREEN}All tests passed${NC}"
    exit 0
fi
