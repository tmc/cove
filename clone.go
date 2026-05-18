// clone.go - VM cloning functionality
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	vz "github.com/tmc/apple/virtualization"
	identityx "github.com/tmc/apple/x/vzkit/identity"
	networkx "github.com/tmc/apple/x/vzkit/network"
	platformx "github.com/tmc/apple/x/vzkit/platform"
	"github.com/tmc/vz-macos/internal/vmconfig"
	"golang.org/x/sys/unix"
)

// ErrCloneSourceNotFound is returned by CloneVM when the source VM
// directory does not exist or fails the vmconfig.Validate check.
var ErrCloneSourceNotFound = errors.New("clone source VM not found")

// ErrCloneTargetExists is returned by CloneVM when the target VM
// directory already exists. Callers can branch on this with errors.Is
// to offer overwrite/rename guidance.
var ErrCloneTargetExists = errors.New("clone target VM already exists")

// CloneOptions configures VM cloning behavior.
type CloneOptions struct {
	Source         string // Source VM name
	Target         string // Target VM name
	Linked         bool   // Use APFS clonefile for the primary disk image (copy-on-write)
	CopyMachineID  bool   // Keep same machine identity (default: false)
	SourceDiskPath string // Override the source system disk path; empty uses the source VM disk
}

// CloneVM creates a copy of a VM.
func CloneVM(opts CloneOptions) error {
	// Validate source exists
	srcPath := vmconfig.Path(opts.Source)
	if !vmconfig.Validate(srcPath) {
		return fmt.Errorf("%w: %s", ErrCloneSourceNotFound, opts.Source)
	}

	// Validate target doesn't exist
	dstPath := vmconfig.Path(opts.Target)
	if _, err := os.Stat(dstPath); !os.IsNotExist(err) {
		return fmt.Errorf("%w: %s", ErrCloneTargetExists, opts.Target)
	}

	fmt.Printf("Cloning %s -> %s\n", opts.Source, opts.Target)

	// Create target directory
	if err := os.MkdirAll(dstPath, 0755); err != nil {
		return fmt.Errorf("create target dir: %w", err)
	}
	if err := vmconfig.EnsureCompatibilityAlias(opts.Target, dstPath); err != nil {
		os.RemoveAll(dstPath)
		return fmt.Errorf("create target compatibility alias: %w", err)
	}

	sourceOS := vmconfig.DetectOSType(srcPath)
	filesToCopy := cloneRequiredFiles(sourceOS)

	for _, f := range filesToCopy {
		srcFile := filepath.Join(srcPath, f.name)
		if f.name == "disk.img" && opts.SourceDiskPath != "" {
			srcFile = opts.SourceDiskPath
		}
		dstFile := filepath.Join(dstPath, f.name)

		// Skip machine.id if we're generating a new one
		if f.name == "machine.id" && !opts.CopyMachineID {
			continue
		}

		if _, err := os.Stat(srcFile); os.IsNotExist(err) {
			if f.required {
				// Clean up and fail
				os.RemoveAll(dstPath)
				return fmt.Errorf("required file missing: %s", f.name)
			}
			continue
		}

		var err error
		if opts.Linked && f.useClone {
			fmt.Printf("  Cloning (CoW): %s\n", f.name)
			err = cloneFile(srcFile, dstFile)
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: CoW clone failed for %s: %v (falling back to full copy)\n", f.name, err)
				err = copyFile(srcFile, dstFile)
			}
		} else {
			fmt.Printf("  Copying: %s\n", f.name)
			err = copyFile(srcFile, dstFile)
		}

		if err != nil {
			os.RemoveAll(dstPath)
			return fmt.Errorf("copy %s: %w", f.name, err)
		}
	}

	// Generate new machine ID if not copying.
	if !opts.CopyMachineID {
		fmt.Println("  Generating new machine ID")
		var err error
		switch sourceOS {
		case "Linux":
			err = generateLinuxMachineID(dstPath)
		default:
			err = generateMachineID(dstPath)
		}
		if err != nil {
			os.RemoveAll(dstPath)
			return fmt.Errorf("generate machine ID: %w", err)
		}
	}

	// Copy optional files (boot-args.txt, control.token, etc.)
	optionalFiles := cloneOptionalFiles(sourceOS)
	for _, f := range optionalFiles {
		srcFile := filepath.Join(srcPath, f)
		dstFile := filepath.Join(dstPath, f)
		if _, err := os.Stat(srcFile); err == nil {
			copyFile(srcFile, dstFile)
		}
	}
	if sourceOS != "Linux" && !opts.CopyMachineID {
		if err := generateMACAddress(dstPath); err != nil {
			os.RemoveAll(dstPath)
			return fmt.Errorf("generate mac address: %w", err)
		}
	}

	// Remove suspend state from clone for deterministic cold boot.
	os.Remove(filepath.Join(dstPath, "suspend.vmstate"))

	fmt.Println("Clone complete.")
	return nil
}

func cloneRequiredFiles(sourceOS string) []struct {
	name     string
	required bool
	useClone bool
} {
	switch sourceOS {
	case "Linux":
		return []struct {
			name     string
			required bool
			useClone bool
		}{
			{"linux-disk.img", true, true},
			{"linux-machine.id", false, false},
		}
	default:
		return []struct {
			name     string
			required bool
			useClone bool
		}{
			{"disk.img", true, true},
			{"aux.img", true, false},
			{"hw.model", true, false},
			{"machine.id", false, false},
		}
	}
}

func cloneOptionalFiles(sourceOS string) []string {
	files := []string{"boot-args.txt", "control.token", "config.json", "shared_folders.json", loginScreenCredentialsFile}
	if sourceOS == "Linux" {
		files = append(files, "efi.nvram", "linux-installed", "cloud-init.iso", "vmlinuz", "initrd", linuxRootUUIDFileName, linuxRootDeviceFileName)
	} else {
		files = append(files, "mac.address")
	}
	return files
}

// cloneFile uses APFS clonefile for copy-on-write cloning.
func cloneFile(src, dst string) error {
	return unix.Clonefile(src, dst, 0)
}

// SupportsClonefile checks whether the directory at path supports APFS
// clonefile (copy-on-write). Returns false if the filesystem is not APFS
// or clonefile is otherwise unavailable.
func SupportsClonefile(dir string) bool {
	probe := filepath.Join(dir, ".clonefile-probe")
	f, err := os.CreateTemp(dir, ".clonefile-probe-*")
	if err != nil {
		return false
	}
	src := f.Name()
	f.Close()
	defer os.Remove(src)

	err = unix.Clonefile(src, probe, 0)
	os.Remove(probe)
	return err == nil
}

// copyFile is defined in ipsw.go

// generateMachineID creates a new machine identifier for a VM.
func generateMachineID(vmPath string) error {
	return identityx.CreateMacMachineIdentifier(filepath.Join(vmPath, "machine.id"))
}

func generateMACAddress(vmPath string) error {
	return networkx.CreateRandomMACAddress(filepath.Join(vmPath, "mac.address"))
}

func generateLinuxMachineID(vmPath string) error {
	machineIDPath := filepath.Join(vmPath, "linux-machine.id")
	machineID := vz.NewVZGenericMachineIdentifier()
	if machineID.ID == 0 {
		return fmt.Errorf("failed to create generic machine identifier")
	}
	return platformx.SaveGenericMachineIdentifier(machineID, machineIDPath)
}
