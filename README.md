# vz-macos

macOS and Linux VM management using Apple's Virtualization.framework via [purego](https://github.com/ebitengine/purego) (cgo-free).

## Features

- Install macOS from IPSW restore images
- Install Linux with cloud-init automated setup
- Non-interactive provisioning via disk injection
- Control socket for programmatic VM management
- Screenshot capture and screen state detection
- Guest agent over vsock (gRPC)
- vzscript engine for automated VM configuration
- Clipboard sharing, drag-and-drop, GUI toolbars

## Quick Start

```bash
# Build and sign
go build -o vz-macos .
codesign --entitlements vz.entitlements -f -s - ./vz-macos

# Install macOS
./vz-macos install -ipsw ~/Downloads/UniversalMac_14.0_RestoreImage.ipsw

# Provision (requires sudo for LaunchDaemon ownership)
sudo ./vz-macos inject -user testuser -password secret123 -skip-setup-assistant

# Verify provisioning
./vz-macos verify

# Run with GUI
./vz-macos run -gui
```

## Linux VMs

```bash
# Install Ubuntu Server (auto-downloads ISO)
./vz-macos install -linux -provision-user ubuntu -provision-password secret

# Run
./vz-macos run -linux -gui
```

## Requirements

- Apple Silicon Mac (M1/M2/M3/M4)
- macOS 12.0+ (Monterey or later)
- Xcode Command Line Tools (for codesign)

## Project Structure

```
main.go              CLI entry point and VM configuration
macos.go             macOS VM creation and lifecycle
installer.go         macOS installation from IPSW
linux.go             Linux VM support with EFI boot
linux_installer.go   Cloud-init based Linux installation
provision*.go        Disk injection provisioning system
password.go          Password hashing and auto-login
control_socket.go    Unix socket server for VM control
screenshots.go       Screen capture via CGWindowListCreateImage
screen_detection.go  UI state detection heuristics
agent_*.go           Guest agent client and control
vzscript*.go         Scripted VM configuration engine
cmd/vz-agent/        Guest-side gRPC daemon
proto/               Protocol buffer definitions
```

## References

- [Apple Virtualization Framework](https://developer.apple.com/documentation/virtualization)
- [purego](https://github.com/ebitengine/purego)
- [Code-Hex/vz](https://github.com/Code-Hex/vz) (CGO reference implementation)
