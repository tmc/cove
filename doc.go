// vz-macos - macOS and Linux VM management using Apple's Virtualization framework
//
// # Overview
//
// vz-macos is a command-line tool for creating, provisioning, and running
// virtual machines using Apple's Virtualization.framework. It supports both
// macOS and Linux guests on Apple Silicon Macs.
//
// The tool uses purego for cgo-free Objective-C interop, making it a pure Go
// implementation that interfaces directly with Apple's native frameworks.
//
// # Quick Start
//
// Install and provision a macOS VM:
//
//	# Download and install macOS
//	./vz-macos install -ipsw ~/Downloads/UniversalMac_14.0_RestoreImage.ipsw
//
//	# Inject provisioning for automatic user creation (REQUIRES SUDO)
//	sudo ./vz-macos inject -user testuser -password secret123 -skip-setup-assistant
//
//	# Verify provisioning files are correct
//	./vz-macos verify
//
//	# Run the VM with GUI
//	./vz-macos run -gui
//
// # Architecture
//
// The tool is organized into several components:
//
//	main.go           - CLI entry point and VM configuration
//	installer.go      - macOS installation from IPSW restore images
//	provision.go      - Disk injection and LaunchDaemon provisioning
//	password.go       - Password hashing and auto-login encoding
//	control_socket.go - Unix socket server for VM control
//	screenshots.go    - Screen capture using CGWindowListCreateImage
//	linux.go          - Linux VM support with EFI boot
//	linux_installer.go - Cloud-init based Linux installation
//
// # Provisioning System
//
// The provisioning system enables fully non-interactive VM setup by injecting
// files directly into the VM's disk image before first boot.
//
// Key concepts:
//
//  1. APFS Volume Mounting - VM disks contain multiple APFS volumes; the "Data"
//     volume holds user data and configuration files.
//
//  2. LaunchDaemon Injection - A plist and script are written to
//     /Library/LaunchDaemons/ to run at boot and create the user account.
//
//  3. Setup Assistant Bypass - The .AppleSetupDone marker tells macOS to skip
//     the first-boot setup wizard.
//
//  4. Auto-Login - kcpassword and loginwindow.plist enable automatic login
//     to the provisioned user account.
//
// See provision.go and password.go for detailed documentation of each component.
//
// # Control Socket
//
// Running VMs expose a Unix domain socket for control and monitoring:
//
//	~/.vz/vms/<name>/control.sock
//
// Commands are sent as JSON and support:
//
//	{"type": "ping"}              - Health check
//	{"type": "status"}            - VM state, pause/resume capability
//	{"type": "screenshot"}        - Capture current display
//	{"type": "screenshot", "path": "/tmp/screen.png"}  - Save to file
//	{"type": "key", "keycode": 36}                     - Send keypress
//	{"type": "text", "text": "hello"}                  - Type text
//
// Example usage:
//
//	echo '{"type":"status"}' | nc -U ~/.vz/vms/default/control.sock
//
// # Linux VM Support
//
// Linux VMs use a different boot mechanism than macOS:
//
//  1. EFI Boot - VZEFIBootLoader with NVRAM variable store
//  2. Virtio GPU - VZVirtioGraphicsDeviceConfiguration for display
//  3. Cloud-init - Automatic installation via NoCloud datasource
//
// Install and run a Linux VM:
//
//	./vz-macos install -linux -provision-user ubuntu -provision-password secret
//	./vz-macos run -linux -gui
//
// # VM Directory Structure
//
// Each VM is stored in ~/.vz/vms/<name>/ with these files:
//
//	disk.img      - Main storage (sparse APFS/ext4 image)
//	aux.img       - macOS auxiliary storage (NVRAM, etc.)
//	hw.model      - Hardware model identifier (macOS only)
//	machine.id    - Machine identifier (macOS only)
//	efi-vars.img  - EFI variable store (Linux only)
//	control.sock  - Control socket (when running)
//
// # Entitlements
//
// The binary must be signed with virtualization entitlements:
//
//	codesign -s - -f --entitlements vz.entitlements ./vz-macos
//
// Required entitlements (vz.entitlements):
//
//	com.apple.security.virtualization          - Basic VM capability
//	com.apple.security.hypervisor             - Hypervisor access
//
// # Requirements
//
//   - Apple Silicon Mac (M1/M2/M3/M4)
//   - macOS 12.0+ (Monterey or later)
//   - Xcode Command Line Tools (for codesign)
//   - IPSW restore image for macOS installation
//
// # References
//
//   - Apple Virtualization Framework: https://developer.apple.com/documentation/virtualization
//   - purego: https://github.com/ebitengine/purego
//   - Code-Hex/vz (CGO reference): https://github.com/Code-Hex/vz
//
package main

//go:generate go build -o vz-macos .
//go:generate codesign --entitlements entitlements.plist -f -s - ./vz-macos
