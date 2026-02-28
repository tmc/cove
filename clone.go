// clone.go - VM cloning functionality
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	vz "github.com/tmc/apple/virtualization"
	"golang.org/x/sys/unix"
)

// CloneOptions configures VM cloning behavior.
type CloneOptions struct {
	Source        string // Source VM name
	Target        string // Target VM name
	Linked        bool   // Use APFS clonefile for disk.img (copy-on-write)
	CopyMachineID bool   // Keep same machine identity (default: false)
}

// CloneVM creates a copy of a VM.
func CloneVM(opts CloneOptions) error {
	// Validate source exists
	srcPath := GetVMPath(opts.Source)
	if !ValidateVM(srcPath) {
		return fmt.Errorf("source VM not found: %s", opts.Source)
	}

	// Validate target doesn't exist
	dstPath := GetVMPath(opts.Target)
	if _, err := os.Stat(dstPath); !os.IsNotExist(err) {
		return fmt.Errorf("target VM already exists: %s", opts.Target)
	}

	fmt.Printf("Cloning %s -> %s\n", opts.Source, opts.Target)

	// Create target directory
	if err := os.MkdirAll(dstPath, 0755); err != nil {
		return fmt.Errorf("create target dir: %w", err)
	}

	// Copy files
	filesToCopy := []struct {
		name     string
		required bool
		useClone bool // Use APFS clonefile if linked option is set
	}{
		{"disk.img", true, true},     // Large, benefits from CoW
		{"aux.img", true, false},     // Small, just copy
		{"hw.model", true, false},    // Tiny, just copy
		{"machine.id", false, false}, // Generate new unless CopyMachineID
	}

	for _, f := range filesToCopy {
		srcFile := filepath.Join(srcPath, f.name)
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
				// Fall back to regular copy if clonefile fails
				fmt.Printf("  CoW failed, falling back to copy: %v\n", err)
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

	// Generate new machine ID if not copying
	if !opts.CopyMachineID {
		fmt.Println("  Generating new machine ID")
		if err := generateMachineID(dstPath); err != nil {
			os.RemoveAll(dstPath)
			return fmt.Errorf("generate machine ID: %w", err)
		}
	}

	// Copy optional files (boot-args.txt, etc.)
	optionalFiles := []string{"boot-args.txt"}
	for _, f := range optionalFiles {
		srcFile := filepath.Join(srcPath, f)
		dstFile := filepath.Join(dstPath, f)
		if _, err := os.Stat(srcFile); err == nil {
			copyFile(srcFile, dstFile)
		}
	}

	fmt.Println("Clone complete.")
	return nil
}

// cloneFile uses APFS clonefile for copy-on-write cloning.
func cloneFile(src, dst string) error {
	return unix.Clonefile(src, dst, 0)
}

// copyFile is defined in ipsw.go

// generateMachineID creates a new machine identifier for a VM.
func generateMachineID(vmPath string) error {
	machineIDPath := filepath.Join(vmPath, "machine.id")

	// Create new machine identifier
	machineID := vz.NewVZMacMachineIdentifier()
	if machineID.ID == 0 {
		return fmt.Errorf("failed to create machine identifier")
	}

	// Get data representation
	data := machineID.DataRepresentation()
	if data.GetID() == 0 {
		return fmt.Errorf("machine identifier has no data representation")
	}

	// Extract bytes and save
	ptr := data.Bytes()
	if ptr == nil {
		return fmt.Errorf("no data bytes")
	}
	length := data.Length()
	if length == 0 {
		return fmt.Errorf("empty data")
	}

	bytes := unsafe.Slice((*byte)(ptr), length)
	return os.WriteFile(machineIDPath, bytes, 0600)
}
