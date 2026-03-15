// vm_ops.go - VM management operations (delete, rename, export, import)
package main

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// DeleteVM deletes a VM by name.
func DeleteVM(name string) error {
	vmPath := GetVMPath(name)
	if !ValidateVM(vmPath) {
		return fmt.Errorf("vm not found: %s", name)
	}

	// Check if it's the active VM
	if GetActiveVM() == name {
		return fmt.Errorf("cannot delete active VM; use 'vm set' to switch first")
	}

	fmt.Printf("Deleting VM '%s'...\n", name)
	if err := os.RemoveAll(vmPath); err != nil {
		return fmt.Errorf("delete VM: %w", err)
	}

	fmt.Println("VM deleted.")
	return nil
}

// RenameVM renames a VM.
func RenameVM(oldName, newName string) error {
	oldPath := GetVMPath(oldName)
	newPath := GetVMPath(newName)

	if !ValidateVM(oldPath) {
		return fmt.Errorf("vm not found: %s", oldName)
	}

	if _, err := os.Stat(newPath); !os.IsNotExist(err) {
		return fmt.Errorf("target VM already exists: %s", newName)
	}

	fmt.Printf("Renaming VM '%s' -> '%s'...\n", oldName, newName)
	if err := os.Rename(oldPath, newPath); err != nil {
		return fmt.Errorf("rename VM: %w", err)
	}

	// Update active VM symlink if needed
	if GetActiveVM() == oldName {
		if err := SetActiveVM(newName); err != nil {
			fmt.Printf("warning: could not update active VM symlink: %v\n", err)
		}
	}

	fmt.Println("VM renamed.")
	return nil
}

// ExportVM exports a VM to a tar.gz archive.
func ExportVM(name, destPath string) error {
	vmPath := GetVMPath(name)
	if !ValidateVM(vmPath) {
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
	vmPath := GetVMPath(name)
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
	if !ValidateVM(vmPath) {
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
