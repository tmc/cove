// rosetta.go - Rosetta 2 support for Linux VMs (x86-64 binary translation on ARM64)
package main

import (
	"fmt"
	"path/filepath"
	"time"

	vz "github.com/tmc/apple/virtualization"
)

// RosettaAvailability represents the availability state of Rosetta
type RosettaAvailability int

const (
	RosettaNotSupported RosettaAvailability = 0 // Not available on this host
	RosettaNotInstalled RosettaAvailability = 1 // Available but needs installation
	RosettaInstalled    RosettaAvailability = 2 // Installed and ready to use
)

// String returns a human-readable description of the availability
func (a RosettaAvailability) String() string {
	switch a {
	case RosettaNotSupported:
		return "not supported"
	case RosettaNotInstalled:
		return "not installed"
	case RosettaInstalled:
		return "installed"
	default:
		return fmt.Sprintf("unknown (%d)", a)
	}
}

// RosettaStatus contains information about Rosetta availability
type RosettaStatus struct {
	Availability RosettaAvailability `json:"availability"`
	Description  string              `json:"description"`
	CanInstall   bool                `json:"canInstall"`
}

// CheckRosettaAvailability checks the current Rosetta availability status
func CheckRosettaAvailability() RosettaStatus {
	availability := vz.GetVZLinuxRosettaDirectoryShareClass().Availability()

	status := RosettaStatus{
		Availability: RosettaAvailability(availability),
	}

	switch status.Availability {
	case RosettaNotSupported:
		status.Description = "Rosetta is not supported on this Mac (requires Apple Silicon)"
		status.CanInstall = false
	case RosettaNotInstalled:
		status.Description = "Rosetta is not installed but can be installed"
		status.CanInstall = true
	case RosettaInstalled:
		status.Description = "Rosetta is installed and ready to use"
		status.CanInstall = false
	}

	return status
}

// InstallRosetta installs Rosetta if available
// This requires macOS 13.0+ and Apple Silicon
func InstallRosetta() error {
	status := CheckRosettaAvailability()

	if status.Availability == RosettaNotSupported {
		return fmt.Errorf("rosetta is not supported on this Mac")
	}

	if status.Availability == RosettaInstalled {
		fmt.Println("Rosetta is already installed")
		return nil
	}

	fmt.Println("Installing Rosetta...")

	errCh := make(chan error, 1)
	vz.GetVZLinuxRosettaDirectoryShareClass().InstallRosettaWithCompletionHandler(
		func(err error) {
			errCh <- err
		})

	// Wait for installation with timeout
	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("rosetta installation failed: %w", err)
		}
	case <-time.After(5 * time.Minute):
		return fmt.Errorf("rosetta installation timed out")
	}

	fmt.Println("Rosetta installed successfully")
	return nil
}

// CreateRosettaDirectoryShare creates a Rosetta directory share for a Linux VM
// Returns nil if Rosetta is not available
func CreateRosettaDirectoryShare(vmDir string) (vz.VZLinuxRosettaDirectoryShare, error) {
	status := CheckRosettaAvailability()

	if status.Availability == RosettaNotSupported {
		return vz.VZLinuxRosettaDirectoryShare{}, fmt.Errorf("rosetta is not supported on this Mac")
	}

	if status.Availability == RosettaNotInstalled {
		return vz.VZLinuxRosettaDirectoryShare{}, fmt.Errorf("rosetta is not installed; run 'vz-macos rosetta install' first")
	}

	// Create the directory share
	share, err := vz.NewLinuxRosettaDirectoryShareWithError()
	if err != nil {
		return share, fmt.Errorf("create rosetta share: %w", err)
	}

	if share.ID == 0 {
		return share, fmt.Errorf("failed to create rosetta directory share")
	}

	// Configure caching for better performance (optional)
	// The caching socket will be at vmDir/rosetta-cache.sock
	// Note: This requires additional setup in the guest
	cachePath := filepath.Join(vmDir, "rosetta-cache.sock")
	_ = cachePath // Reserved for future caching options implementation

	fmt.Println("  Rosetta directory share created")
	return share, nil
}

// CreateRosettaVirtioFSConfig creates a VirtioFS configuration for Rosetta
func CreateRosettaVirtioFSConfig(vmDir string) (vz.VZVirtioFileSystemDeviceConfiguration, error) {
	share, err := CreateRosettaDirectoryShare(vmDir)
	if err != nil {
		return vz.VZVirtioFileSystemDeviceConfiguration{}, err
	}

	// Create VirtioFS device configuration
	fsConfig := vz.NewVZVirtioFileSystemDeviceConfiguration()
	if fsConfig.ID == 0 {
		return fsConfig, fmt.Errorf("failed to create VirtioFS configuration")
	}

	// Set the share
	fsConfig.SetShare(&share.VZDirectoryShare)

	// Set the tag that the guest will use to mount
	// The guest mounts with: mount -t virtiofs rosetta /run/rosetta
	fsConfig.SetTag("rosetta")

	return fsConfig, nil
}

// AddRosettaToLinuxVM adds Rosetta support to a Linux VM configuration
// This adds a VirtioFS device configured for Rosetta
func AddRosettaToLinuxVM(config vz.VZVirtualMachineConfiguration, vmDir string) error {
	rosettaConfig, err := CreateRosettaVirtioFSConfig(vmDir)
	if err != nil {
		return err
	}

	// Get existing directory sharing devices
	existingDevices := config.DirectorySharingDevices()

	// Add the Rosetta device
	devices := make([]vz.VZDirectorySharingDeviceConfiguration, 0, len(existingDevices)+1)
	for _, dev := range existingDevices {
		devices = append(devices, vz.VZDirectorySharingDeviceConfigurationFromID(dev.GetID()))
	}
	devices = append(devices, vz.VZDirectorySharingDeviceConfigurationFromID(rosettaConfig.ID))

	config.SetDirectorySharingDevices(devices)

	fmt.Println("  Rosetta VirtioFS device added to VM configuration")
	return nil
}

// PrintRosettaGuestSetup prints instructions for setting up Rosetta in the guest
func PrintRosettaGuestSetup() {
	fmt.Print(`
Rosetta Guest Setup Instructions
=================================

After booting your Linux VM, run the following commands to enable Rosetta:

1. Mount the Rosetta directory:
   sudo mkdir -p /run/rosetta
   sudo mount -t virtiofs rosetta /run/rosetta

2. Register Rosetta as a binfmt handler:
   sudo /run/rosetta/rosetta --register

3. (Optional) Make the mount persistent by adding to /etc/fstab:
   echo 'rosetta /run/rosetta virtiofs ro,nofail 0 0' | sudo tee -a /etc/fstab

4. (Optional) Make registration persistent:
   sudo update-binfmts --enable rosetta

After setup, x86-64 binaries will transparently run through Rosetta.

Verify with:
   file /bin/ls                    # Should show ARM64
   dpkg --add-architecture amd64   # Enable x86-64 packages (Debian/Ubuntu)
   apt update
   apt install hello:amd64         # Install an x86-64 package
   hello                           # Should run via Rosetta
`)
}

// RosettaHelp returns help text for Rosetta commands
func RosettaHelp() string {
	return `Rosetta 2 for Linux VMs
=======================

Rosetta allows ARM64 Linux VMs to run x86-64 binaries through
transparent binary translation.

Requirements:
  - Apple Silicon Mac (M1/M2/M3/M4)
  - macOS 13.0 or later
  - Linux VM with VirtioFS support

Commands:
  vz-macos rosetta status   Check Rosetta availability
  vz-macos rosetta install  Install Rosetta (if needed)
  vz-macos rosetta setup    Show guest setup instructions

Usage:
  vz-macos run -linux -rosetta   Run Linux VM with Rosetta enabled

The guest must mount the Rosetta virtiofs share and register the
binfmt handler. See 'vz-macos rosetta setup' for instructions.`
}

// handleRosettaCommand handles the rosetta subcommand
func handleRosettaCommand(args []string) error {
	if len(args) == 0 {
		fmt.Println(RosettaHelp())
		return nil
	}

	switch args[0] {
	case "status":
		status := CheckRosettaAvailability()
		fmt.Printf("Rosetta availability: %s\n", status.Availability.String())
		fmt.Printf("Description: %s\n", status.Description)
		if status.CanInstall {
			fmt.Println("\nTo install: vz-macos rosetta install")
		}
		return nil

	case "install":
		return InstallRosetta()

	case "setup":
		PrintRosettaGuestSetup()
		return nil

	case "help":
		fmt.Println(RosettaHelp())
		return nil

	default:
		return fmt.Errorf("unknown rosetta command: %s (use status, install, setup, or help)", args[0])
	}
}
