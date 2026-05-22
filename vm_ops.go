// vm_ops.go - VM management operations (delete, rename, export, import)
package main

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tmc/cove/internal/vmconfig"
)

// ErrVMNotFound is returned by DeleteVM/RenameVM (and friends) when
// the named VM directory is missing or fails vmconfig.Validate.
// Callers can branch on this with errors.Is to format a "did you mean"
// hint without parsing the message.
var ErrVMNotFound = errors.New("vm not found")

// ErrVMRenameTargetExists is returned by RenameVM when the requested
// new name already corresponds to an existing VM directory.
var ErrVMRenameTargetExists = errors.New("rename target VM already exists")

// DeleteVMOptions configures DeleteVMWithOptions. Cascade descends to
// children before removing the named VM, mirroring rm -r semantics.
// Without Cascade, a VM with live children is refused.
type DeleteVMOptions struct {
	Cascade bool
}

// DeleteVM deletes a VM by name (no cascade). See DeleteVMWithOptions
// for the cascade-aware form.
func DeleteVM(name string) error {
	return DeleteVMWithOptions(name, DeleteVMOptions{})
}

// DeleteVMWithOptions deletes a VM by name. If the directory exists
// but does not contain a valid VM (e.g. an orphan left over from a
// prior failure), the directory is still removed so users can clean
// up without editing ~/.vz/vms by hand.
//
// If the named VM is the active VM, deletion is allowed when the VM
// is stopped or is an orphan (missing disk image). The active-VM
// marker is cleared in the same step. Deletion is refused only when
// the VM is currently running.
//
// If the named VM is the parent of any other VM (lineage recorded by
// fork paths in vmconfig.ParentVM) and opts.Cascade is false,
// deletion is refused with a list of dependent children. With
// Cascade, children are deleted first (recursively).
func DeleteVMWithOptions(name string, opts DeleteVMOptions) error {
	vmPath := vmconfig.Path(name)
	info, err := os.Stat(vmPath)
	if err != nil {
		if os.IsNotExist(err) {
			candidate := filepath.Join(vmconfig.BaseDir(), name)
			if candidateInfo, candidateErr := os.Lstat(candidate); candidateErr == nil && !candidateInfo.IsDir() {
				return fmt.Errorf("not a VM directory: %s", candidate)
			}
			return fmt.Errorf("%w: %s\n  list VMs: cove list\n  create a VM: cove up -user <name>", ErrVMNotFound, name)
		}
		return fmt.Errorf("stat VM dir: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("not a VM directory: %s", vmPath)
	}

	if isVMRunningAt(vmPath) && !waitForVMNotRunning(vmPath, 3*time.Second) {
		return fmt.Errorf("cannot delete VM %q: it is currently running\n  request stop: cove ctl -vm %s request-stop\n  check status: cove list\n  if still running: cove ctl -vm %s stop\n  then retry: cove vm delete %s", name, name, name, name)
	}

	children, err := childVMNames(name)
	if err != nil {
		return err
	}
	if len(children) > 0 {
		if !opts.Cascade {
			return fmt.Errorf("VM '%s' has %d fork descendant(s): %s — pass --cascade to delete them too", name, len(children), strings.Join(children, ", "))
		}
		// Cascade: delete children first. This refuses if any child
		// is itself running, surfacing the original error.
		for _, child := range children {
			if err := DeleteVMWithOptions(child, opts); err != nil {
				return fmt.Errorf("cascade delete child '%s': %w", child, err)
			}
		}
	}

	wasActive := vmconfig.ActiveName() == name

	if !vmconfig.Validate(vmPath) {
		fmt.Printf("Deleting orphan VM directory '%s' (no disk image found)...\n", name)
	} else {
		fmt.Printf("Deleting VM '%s'...\n", name)
	}
	if err := os.RemoveAll(vmPath); err != nil {
		return fmt.Errorf("delete VM: %w", err)
	}
	if err := vmconfig.RemoveCompatibilityAlias(name); err != nil {
		fmt.Printf("warning: remove VM compatibility alias: %v\n", err)
	}
	if err := vmconfig.RemovePackageAlias(name); err != nil {
		fmt.Printf("warning: remove Finder VM package alias: %v\n", err)
	}

	if wasActive {
		if err := vmconfig.UnsetActive(); err != nil {
			fmt.Printf("warning: clear active VM marker: %v\n", err)
		} else {
			fmt.Println("Cleared active-VM marker.")
		}
	}

	fmt.Println("VM deleted.")
	return nil
}

func waitForVMNotRunning(vmPath string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		if !isVMRunningAt(vmPath) {
			return true
		}
	}
	return !isVMRunningAt(vmPath)
}

// childVMNames returns the sorted names of VMs whose ParentVM
// matches parent. An empty slice (no children) means delete is
// safe with respect to lineage.
func childVMNames(parent string) ([]string, error) {
	vms, err := vmconfig.List(nil)
	if err != nil {
		return nil, fmt.Errorf("list VMs: %w", err)
	}
	var children []string
	for _, vm := range vms {
		if vm.Name == parent {
			continue
		}
		cfg, err := vmconfig.Load(vm.Path)
		if err != nil {
			// Skip unreadable configs; they can't claim parentage.
			continue
		}
		if cfg.ParentVM == parent {
			children = append(children, vm.Name)
		}
	}
	sort.Strings(children)
	return children, nil
}

// RenameVM renames a VM.
func RenameVM(oldName, newName string) error {
	oldPath := vmconfig.Path(oldName)
	newPath := vmconfig.Path(newName)

	if !vmconfig.Validate(oldPath) {
		return fmt.Errorf("%w: %s\n  list VMs: cove list\n  create a VM: cove up -user <name>", ErrVMNotFound, oldName)
	}

	if _, err := os.Stat(newPath); !os.IsNotExist(err) {
		return fmt.Errorf("%w: %s", ErrVMRenameTargetExists, newName)
	}

	fmt.Printf("Renaming VM '%s' -> '%s'...\n", oldName, newName)
	if err := os.Rename(oldPath, newPath); err != nil {
		return fmt.Errorf("rename VM: %w", err)
	}
	if err := vmconfig.RemovePackageAlias(oldName); err != nil {
		fmt.Printf("warning: remove old Finder VM package alias: %v\n", err)
	}
	if err := vmconfig.RemoveCompatibilityAlias(oldName); err != nil {
		fmt.Printf("warning: remove old VM compatibility alias: %v\n", err)
	}
	if err := vmconfig.EnsureCompatibilityAlias(newName, newPath); err != nil {
		fmt.Printf("warning: create VM compatibility alias: %v\n", err)
	}
	if err := vmconfig.EnsurePackageAlias(newName, newPath); err != nil {
		fmt.Printf("warning: create Finder VM package alias: %v\n", err)
	}

	// Update active VM symlink if needed
	if vmconfig.ActiveName() == oldName {
		if err := vmconfig.SetActive(newName); err != nil {
			fmt.Printf("warning: could not update active VM symlink: %v\n", err)
		}
	}

	fmt.Println("VM renamed.")
	return nil
}

// ExportVM exports a VM to a tar.gz archive.
func ExportVM(name, destPath string) error {
	vmPath := vmconfig.Path(name)
	if !vmconfig.Validate(vmPath) {
		return fmt.Errorf("vm not found: %s", name)
	}

	// Add .tar.gz extension if not present
	if filepath.Ext(destPath) != ".gz" && filepath.Ext(destPath) != ".tgz" {
		if filepath.Ext(destPath) != ".tar" {
			destPath += ".tar.gz"
		} else {
			destPath += ".gz"
		}
	}

	fmt.Printf("Exporting VM '%s' to %s...\n", name, destPath)

	// Create output file
	outFile, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("create output file: %w", err)
	}
	defer outFile.Close()

	// Create gzip writer
	gzWriter := gzip.NewWriter(outFile)
	defer gzWriter.Close()

	// Create tar writer
	tarWriter := tar.NewWriter(gzWriter)
	defer tarWriter.Close()

	// Walk the VM directory and add all files
	err = filepath.Walk(vmPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Create tar header
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}

		// Use relative path within archive
		relPath, err := filepath.Rel(vmPath, path)
		if err != nil {
			return err
		}
		header.Name = filepath.Join(name, relPath)

		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}

		// Write file content if it's a regular file
		if info.Mode().IsRegular() {
			fmt.Printf("  Adding: %s\n", relPath)
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			defer file.Close()

			if _, err := io.Copy(tarWriter, file); err != nil {
				return err
			}
		}

		return nil
	})

	if err != nil {
		os.Remove(destPath)
		return fmt.Errorf("create archive: %w", err)
	}

	fmt.Println("Export complete.")
	return nil
}

// ImportVM imports a VM from a tar.gz archive.
func ImportVM(archivePath, name string) error {
	// Validate archive exists
	if _, err := os.Stat(archivePath); os.IsNotExist(err) {
		return fmt.Errorf("archive not found: %s", archivePath)
	}

	// Check VM doesn't exist
	vmPath := vmconfig.Path(name)
	if _, err := os.Stat(vmPath); !os.IsNotExist(err) {
		return fmt.Errorf("vm already exists: %s", name)
	}

	fmt.Printf("Importing VM '%s' from %s...\n", name, archivePath)

	// Open archive
	file, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer file.Close()

	// Create gzip reader
	gzReader, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("create gzip reader: %w", err)
	}
	defer gzReader.Close()

	// Create tar reader
	tarReader := tar.NewReader(gzReader)

	// Create VM directory
	if err := os.MkdirAll(vmPath, 0755); err != nil {
		return fmt.Errorf("create VM dir: %w", err)
	}
	if err := vmconfig.EnsureCompatibilityAlias(name, vmPath); err != nil {
		os.RemoveAll(vmPath)
		return fmt.Errorf("create VM compatibility alias: %w", err)
	}

	// Extract files
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			os.RemoveAll(vmPath)
			return fmt.Errorf("read tar: %w", err)
		}

		// Get the path relative to the archive's root directory
		// Archive format is: vmname/file.img, so we skip the first component
		parts := filepath.SplitList(header.Name)
		if len(parts) == 0 {
			continue
		}

		// Extract just the filename, ignore original VM name
		baseName := filepath.Base(header.Name)
		if baseName == "." || baseName == ".." {
			continue
		}

		// For directory entries, skip (we handle files only)
		if header.Typeflag == tar.TypeDir {
			continue
		}

		destPath := filepath.Join(vmPath, baseName)
		fmt.Printf("  Extracting: %s\n", baseName)

		outFile, err := os.OpenFile(destPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, os.FileMode(header.Mode))
		if err != nil {
			os.RemoveAll(vmPath)
			return fmt.Errorf("create file %s: %w", baseName, err)
		}

		if _, err := io.Copy(outFile, tarReader); err != nil {
			outFile.Close()
			os.RemoveAll(vmPath)
			return fmt.Errorf("extract %s: %w", baseName, err)
		}
		outFile.Close()
	}

	// Validate the imported VM
	if !vmconfig.Validate(vmPath) {
		os.RemoveAll(vmPath)
		return fmt.Errorf("imported archive does not contain a valid VM")
	}

	fmt.Println("Import complete.")
	return nil
}

// For sparse files, we need to check actual blocks used

// On macOS, we can get actual disk usage via stat
// info.Size() is the logical size, but for sparse files actual usage is less
// For now, return the logical size - actual disk usage requires syscall
