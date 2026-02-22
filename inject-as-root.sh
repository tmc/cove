#!/bin/bash
# inject-as-root.sh - Run vz-macos inject with admin privileges
# Uses osascript to prompt for admin password via GUI dialog

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
VZ_MACOS="$SCRIPT_DIR/vz-macos"

# Check if vz-macos binary exists
if [ ! -f "$VZ_MACOS" ]; then
    echo "Error: vz-macos binary not found at $VZ_MACOS"
    echo "Build it first with: go build"
    exit 1
fi

# Default values
VM_DIR=""
USER=""
PASSWORD=""
SKIP_SETUP="false"
NO_AUTO_LOGIN="false"
VERBOSE="false"

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        -vm)
            VM_DIR="$2"
            shift 2
            ;;
        -user)
            USER="$2"
            shift 2
            ;;
        -password)
            PASSWORD="$2"
            shift 2
            ;;
        -skip-setup-assistant)
            SKIP_SETUP="true"
            shift
            ;;
        -no-auto-login)
            NO_AUTO_LOGIN="true"
            shift
            ;;
        -v)
            VERBOSE="true"
            shift
            ;;
        -h|--help)
            echo "Usage: $0 [options]"
            echo ""
            echo "Options:"
            echo "  -vm <name>              VM name to inject into"
            echo "  -user <username>        Username for the provisioned user (required)"
            echo "  -password <password>    Password for the provisioned user (required)"
            echo "  -skip-setup-assistant   Skip Setup Assistant entirely"
            echo "  -no-auto-login          Disable automatic login"
            echo "  -v                      Verbose output"
            echo ""
            echo "Example:"
            echo "  $0 -user testuser -password secret123 -skip-setup-assistant"
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            echo "Use -h for help"
            exit 1
            ;;
    esac
done

# Validate required arguments
if [ -z "$USER" ]; then
    echo "Error: -user is required"
    echo "Use -h for help"
    exit 1
fi

if [ -z "$PASSWORD" ]; then
    echo "Error: -password is required"
    echo "Use -h for help"
    exit 1
fi

# Build the command
CMD="$VZ_MACOS"
if [ -n "$VM_DIR" ]; then
    CMD="$CMD -vm $VM_DIR"
fi
CMD="$CMD inject -user '$USER' -password '$PASSWORD'"
if [ "$SKIP_SETUP" = "true" ]; then
    CMD="$CMD -skip-setup-assistant"
fi
if [ "$NO_AUTO_LOGIN" = "true" ]; then
    CMD="$CMD -no-auto-login"
fi
if [ "$VERBOSE" = "true" ]; then
    CMD="$CMD -v"
fi

echo "Running inject with admin privileges..."
echo "Command: $CMD"
echo ""

# Use osascript to run with admin privileges (prompts for password via GUI)
osascript -e "do shell script \"$CMD\" with administrator privileges"

echo ""
echo "Inject complete. Run the VM with: ./vz-macos run"
