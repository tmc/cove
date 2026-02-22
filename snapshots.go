// snapshots.go - VM state save/restore (snapshots) support
//
// Two types of snapshots are supported:
//
//  1. VM State Snapshots: Save/restore CPU and memory state (fast resume)
//     - Stored in vmDir/snapshots/<name>.vmstate
//     - VM must be paused to save, stopped to restore
//
//  2. Disk Snapshots: Save/restore disk image state (clone the disk)
//     - Uses APFS clonefile for instant copy-on-write snapshots
//     - Can snapshot system disk and/or userdata disk independently
//     - Stored in vmDir/disk-snapshots/<name>/
//
// The dual-disk architecture allows independent operations:
//   - Snapshot only userdata (preserve user files)
//   - Snapshot only system (preserve OS state)
//   - Snapshot both (full checkpoint)
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/tmc/appledocs/generated/dispatch"
	"github.com/tmc/appledocs/generated/foundation"
	vz "github.com/tmc/appledocs/generated/virtualization"
	"golang.org/x/sys/unix"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

// snapshotMeta is the on-disk metadata format for VM state snapshots.
type snapshotMeta struct {
	Name     string    `json:"name"`
	Created  time.Time `json:"created"`
	Size     int64     `json:"size"`
	VMState  string    `json:"vmState"` // State when snapshot was taken
	FilePath string    `json:"filePath"`
}

// SnapshotManager handles VM state snapshot operations
type SnapshotManager struct {
	vmDir string
}

// DiskSnapshotTarget specifies which disk(s) to snapshot.
type DiskSnapshotTarget int

const (
	DiskSnapshotSystem   DiskSnapshotTarget = 1 << iota // System disk (disk.img)
	DiskSnapshotUserData                                // User data disk (userdata.sparsebundle)
	DiskSnapshotBoth     = DiskSnapshotSystem | DiskSnapshotUserData
)

// DiskSnapshotInfo contains metadata about a disk snapshot.
type DiskSnapshotInfo struct {
	Name         string             `json:"name"`
	Created      time.Time          `json:"created"`
	Target       DiskSnapshotTarget `json:"target"`
	SystemSize   int64              `json:"systemSize,omitempty"`
	UserDataSize int64              `json:"userDataSize,omitempty"`
	Description  string             `json:"description,omitempty"`
	FilePath     string             `json:"filePath"`
}

// DiskSnapshotManager handles disk-level snapshot operations.
type DiskSnapshotManager struct {
	vmDir string
}

// NewSnapshotManager creates a new snapshot manager for the given VM directory
func NewSnapshotManager(vmDir string) *SnapshotManager {
	return &SnapshotManager{vmDir: vmDir}
}

// snapshotsDir returns the path to the snapshots directory
func (m *SnapshotManager) snapshotsDir() string {
	return filepath.Join(m.vmDir, "snapshots")
}

// snapshotPath returns the path to a specific snapshot file
func (m *SnapshotManager) snapshotPath(name string) string {
	return filepath.Join(m.snapshotsDir(), name+".vmstate")
}

// metadataPath returns the path to snapshot metadata file
func (m *SnapshotManager) metadataPath(name string) string {
	return filepath.Join(m.snapshotsDir(), name+".json")
}

// ensureDir creates the snapshots directory if it doesn't exist
func (m *SnapshotManager) ensureDir() error {
	return os.MkdirAll(m.snapshotsDir(), 0755)
}

// Save saves the current VM state to a snapshot
// The VM must be paused before saving
func (m *SnapshotManager) Save(vm vz.VZVirtualMachine, queue dispatch.Queue, name string) error {
	if err := m.ensureDir(); err != nil {
		return fmt.Errorf("create snapshots directory: %w", err)
	}

	// Check if VM can be saved (must be paused)
	var state vz.VZVirtualMachineState
	checkDone := make(chan struct{})
	DispatchAsyncQueue(queue, func() {
		defer close(checkDone)
		state = vz.VZVirtualMachineState(vm.State())
	})
	<-checkDone

	// If running, pause first
	wasPaused := state == vz.VZVirtualMachineStatePaused
	if state == vz.VZVirtualMachineStateRunning {
		fmt.Println("Pausing VM for snapshot...")
		errCh := make(chan error, 1)
		DispatchAsyncQueue(queue, func() {
			vm.PauseWithCompletionHandler(func(err error) {
				errCh <- err
			})
		})
		if err := <-errCh; err != nil {
			return fmt.Errorf("pause VM: %w", err)
		}
	} else if state != vz.VZVirtualMachineStatePaused {
		return fmt.Errorf("VM must be running or paused to save snapshot (current state: %s)", state.String())
	}

	// Save the snapshot
	snapshotFile := m.snapshotPath(name)
	saveURL := foundation.FileURL(snapshotFile)
	saveURL.Retain()

	fmt.Printf("Saving snapshot '%s' to: %s\n", name, snapshotFile)

	errCh := make(chan error, 1)
	DispatchAsyncQueue(queue, func() {
		vm.SaveMachineStateToURLCompletionHandler(saveURL, func(err error) {
			errCh <- err
		})
	})

	// Wait for save with timeout
	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("save snapshot: %w", err)
		}
	case <-time.After(60 * time.Second):
		return fmt.Errorf("save snapshot timed out")
	}

	// Get file size
	info, err := os.Stat(snapshotFile)
	var size int64
	if err == nil {
		size = info.Size()
	}

	// Save metadata
	metadata := snapshotMeta{
		Name:     name,
		Created:  time.Now(),
		Size:     size,
		VMState:  "paused",
		FilePath: snapshotFile,
	}
	metaBytes, _ := json.MarshalIndent(metadata, "", "  ")
	if err := os.WriteFile(m.metadataPath(name), metaBytes, 0644); err != nil {
		fmt.Printf("Warning: could not save snapshot metadata: %v\n", err)
	}

	// Resume if VM was running before
	if !wasPaused {
		fmt.Println("Resuming VM...")
		resumeErrCh := make(chan error, 1)
		DispatchAsyncQueue(queue, func() {
			vm.ResumeWithCompletionHandler(func(err error) {
				resumeErrCh <- err
			})
		})
		if err := <-resumeErrCh; err != nil {
			return fmt.Errorf("resume VM after snapshot: %w", err)
		}
	}

	fmt.Printf("Snapshot '%s' saved successfully (%s)\n", name, FormatSize(size))
	return nil
}

// Restore restores the VM from a saved snapshot
// The VM must be stopped before restoring, and will be in paused state after
func (m *SnapshotManager) Restore(vm vz.VZVirtualMachine, queue dispatch.Queue, name string) error {
	snapshotFile := m.snapshotPath(name)
	if _, err := os.Stat(snapshotFile); os.IsNotExist(err) {
		return fmt.Errorf("snapshot '%s' not found", name)
	}

	// Check VM state (must be stopped)
	var state vz.VZVirtualMachineState
	checkDone := make(chan struct{})
	DispatchAsyncQueue(queue, func() {
		defer close(checkDone)
		state = vz.VZVirtualMachineState(vm.State())
	})
	<-checkDone

	if state != vz.VZVirtualMachineStateStopped {
		return fmt.Errorf("VM must be stopped to restore snapshot (current state: %s)", state.String())
	}

	// Restore the snapshot
	restoreURL := foundation.FileURL(snapshotFile)
	restoreURL.Retain()

	fmt.Printf("Restoring snapshot '%s' from: %s\n", name, snapshotFile)

	errCh := make(chan error, 1)
	DispatchAsyncQueue(queue, func() {
		vm.RestoreMachineStateFromURLCompletionHandler(restoreURL, func(err error) {
			errCh <- err
		})
	})

	// Wait for restore with timeout
	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("restore snapshot: %w", err)
		}
	case <-time.After(60 * time.Second):
		return fmt.Errorf("restore snapshot timed out")
	}

	fmt.Printf("Snapshot '%s' restored successfully (VM is now paused)\n", name)
	return nil
}

// List returns all available snapshots
func (m *SnapshotManager) List() ([]snapshotMeta, error) {
	dir := m.snapshotsDir()
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil, nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read snapshots directory: %w", err)
	}

	var snapshots []snapshotMeta
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".vmstate" {
			continue
		}

		name := entry.Name()[:len(entry.Name())-8] // Remove .vmstate extension

		// Try to load metadata
		var info snapshotMeta
		metaPath := m.metadataPath(name)
		if metaBytes, err := os.ReadFile(metaPath); err == nil {
			json.Unmarshal(metaBytes, &info)
		}

		// Fill in basic info if metadata missing
		if info.Name == "" {
			info.Name = name
		}
		if fileInfo, err := entry.Info(); err == nil {
			info.Size = fileInfo.Size()
			if info.Created.IsZero() {
				info.Created = fileInfo.ModTime()
			}
		}
		info.FilePath = filepath.Join(dir, entry.Name())

		snapshots = append(snapshots, info)
	}

	// Sort by creation time (newest first)
	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].Created.After(snapshots[j].Created)
	})

	return snapshots, nil
}

// Delete removes a snapshot
func (m *SnapshotManager) Delete(name string) error {
	snapshotFile := m.snapshotPath(name)
	if _, err := os.Stat(snapshotFile); os.IsNotExist(err) {
		return fmt.Errorf("snapshot '%s' not found", name)
	}

	// Remove snapshot file
	if err := os.Remove(snapshotFile); err != nil {
		return fmt.Errorf("remove snapshot file: %w", err)
	}

	// Remove metadata file (ignore errors)
	os.Remove(m.metadataPath(name))

	fmt.Printf("Snapshot '%s' deleted\n", name)
	return nil
}

// =============================================================================
// Disk Snapshot Manager - independent disk-level snapshots
// =============================================================================

// NewDiskSnapshotManager creates a new disk snapshot manager.
func NewDiskSnapshotManager(vmDir string) *DiskSnapshotManager {
	return &DiskSnapshotManager{vmDir: vmDir}
}

// diskSnapshotsDir returns the path to the disk snapshots directory.
func (m *DiskSnapshotManager) diskSnapshotsDir() string {
	return filepath.Join(m.vmDir, "disk-snapshots")
}

// snapshotDir returns the path to a specific disk snapshot directory.
func (m *DiskSnapshotManager) snapshotDir(name string) string {
	return filepath.Join(m.diskSnapshotsDir(), name)
}

// metadataPath returns the path to disk snapshot metadata.
func (m *DiskSnapshotManager) metadataPath(name string) string {
	return filepath.Join(m.snapshotDir(name), "metadata.json")
}

// ensureDir creates the disk snapshots directory if needed.
func (m *DiskSnapshotManager) ensureDir() error {
	return os.MkdirAll(m.diskSnapshotsDir(), 0755)
}

// Save creates a disk snapshot using APFS clonefile.
// VM should be stopped for consistent snapshots.
func (m *DiskSnapshotManager) Save(name string, target DiskSnapshotTarget, description string) error {
	if err := m.ensureDir(); err != nil {
		return fmt.Errorf("create disk snapshots directory: %w", err)
	}

	snapDir := m.snapshotDir(name)
	if _, err := os.Stat(snapDir); !os.IsNotExist(err) {
		return fmt.Errorf("disk snapshot '%s' already exists", name)
	}

	if err := os.MkdirAll(snapDir, 0755); err != nil {
		return fmt.Errorf("create snapshot directory: %w", err)
	}

	info := DiskSnapshotInfo{
		Name:        name,
		Created:     time.Now(),
		Target:      target,
		Description: description,
		FilePath:    snapDir,
	}

	// Snapshot system disk
	if target&DiskSnapshotSystem != 0 {
		srcPath := filepath.Join(m.vmDir, "disk.img")
		if _, err := os.Stat(srcPath); err == nil {
			dstPath := filepath.Join(snapDir, "disk.img")
			fmt.Printf("  Snapshotting system disk (clonefile)...\n")
			if err := m.cloneFileWithFallback(srcPath, dstPath); err != nil {
				os.RemoveAll(snapDir)
				return fmt.Errorf("clone system disk: %w", err)
			}
			if fi, err := os.Stat(srcPath); err == nil {
				info.SystemSize = fi.Size()
			}
		} else {
			fmt.Printf("  Warning: system disk not found at %s\n", srcPath)
		}
	}

	// Snapshot userdata disk
	if target&DiskSnapshotUserData != 0 {
		srcPath := filepath.Join(m.vmDir, "userdata.sparsebundle")
		if _, err := os.Stat(srcPath); err == nil {
			dstPath := filepath.Join(snapDir, "userdata.sparsebundle")
			fmt.Printf("  Snapshotting userdata disk...\n")
			if err := m.cloneDirWithFallback(srcPath, dstPath); err != nil {
				os.RemoveAll(snapDir)
				return fmt.Errorf("clone userdata disk: %w", err)
			}
			if size, err := dirSize(srcPath); err == nil {
				info.UserDataSize = size
			}
		} else {
			fmt.Printf("  Info: no userdata disk found (single-disk setup)\n")
		}
	}

	// Save metadata
	metaBytes, _ := json.MarshalIndent(info, "", "  ")
	if err := os.WriteFile(m.metadataPath(name), metaBytes, 0644); err != nil {
		return fmt.Errorf("save snapshot metadata: %w", err)
	}

	fmt.Printf("Disk snapshot '%s' created\n", name)
	return nil
}

// Restore restores disk(s) from a snapshot.
// VM must be stopped before restoring.
func (m *DiskSnapshotManager) Restore(name string, target DiskSnapshotTarget) error {
	snapDir := m.snapshotDir(name)
	if _, err := os.Stat(snapDir); os.IsNotExist(err) {
		return fmt.Errorf("disk snapshot '%s' not found", name)
	}

	// Load metadata to verify what's in the snapshot
	var info DiskSnapshotInfo
	if metaBytes, err := os.ReadFile(m.metadataPath(name)); err == nil {
		json.Unmarshal(metaBytes, &info)
	}

	// Restore system disk
	if target&DiskSnapshotSystem != 0 {
		srcPath := filepath.Join(snapDir, "disk.img")
		dstPath := filepath.Join(m.vmDir, "disk.img")
		if _, err := os.Stat(srcPath); err == nil {
			fmt.Printf("  Restoring system disk...\n")
			// Remove existing disk first
			if err := os.Remove(dstPath); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove existing system disk: %w", err)
			}
			if err := m.cloneFileWithFallback(srcPath, dstPath); err != nil {
				return fmt.Errorf("restore system disk: %w", err)
			}
		} else if info.Target&DiskSnapshotSystem != 0 {
			return fmt.Errorf("snapshot claims to have system disk but file not found")
		}
	}

	// Restore userdata disk
	if target&DiskSnapshotUserData != 0 {
		srcPath := filepath.Join(snapDir, "userdata.sparsebundle")
		dstPath := filepath.Join(m.vmDir, "userdata.sparsebundle")
		if _, err := os.Stat(srcPath); err == nil {
			fmt.Printf("  Restoring userdata disk...\n")
			// Remove existing disk first
			if err := os.RemoveAll(dstPath); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove existing userdata disk: %w", err)
			}
			if err := m.cloneDirWithFallback(srcPath, dstPath); err != nil {
				return fmt.Errorf("restore userdata disk: %w", err)
			}
		} else if info.Target&DiskSnapshotUserData != 0 {
			return fmt.Errorf("snapshot claims to have userdata disk but directory not found")
		}
	}

	fmt.Printf("Disk snapshot '%s' restored\n", name)
	return nil
}

// List returns all available disk snapshots.
func (m *DiskSnapshotManager) List() ([]DiskSnapshotInfo, error) {
	dir := m.diskSnapshotsDir()
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil, nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read disk snapshots directory: %w", err)
	}

	var snapshots []DiskSnapshotInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		name := entry.Name()
		var info DiskSnapshotInfo

		// Try to load metadata
		metaPath := m.metadataPath(name)
		if metaBytes, err := os.ReadFile(metaPath); err == nil {
			json.Unmarshal(metaBytes, &info)
		}

		// Fill in defaults if metadata missing
		if info.Name == "" {
			info.Name = name
		}
		if fileInfo, err := entry.Info(); err == nil && info.Created.IsZero() {
			info.Created = fileInfo.ModTime()
		}
		info.FilePath = filepath.Join(dir, entry.Name())

		snapshots = append(snapshots, info)
	}

	// Sort by creation time (newest first)
	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].Created.After(snapshots[j].Created)
	})

	return snapshots, nil
}

// Delete removes a disk snapshot.
func (m *DiskSnapshotManager) Delete(name string) error {
	snapDir := m.snapshotDir(name)
	if _, err := os.Stat(snapDir); os.IsNotExist(err) {
		return fmt.Errorf("disk snapshot '%s' not found", name)
	}

	if err := os.RemoveAll(snapDir); err != nil {
		return fmt.Errorf("remove disk snapshot: %w", err)
	}

	fmt.Printf("Disk snapshot '%s' deleted\n", name)
	return nil
}

// cloneFileWithFallback uses APFS clonefile with fallback to regular copy.
func (m *DiskSnapshotManager) cloneFileWithFallback(src, dst string) error {
	err := unix.Clonefile(src, dst, 0)
	if err == nil {
		return nil
	}
	// Fall back to regular copy
	return copyFile(src, dst)
}

// cloneDirWithFallback clones a directory (for sparse bundles).
func (m *DiskSnapshotManager) cloneDirWithFallback(src, dst string) error {
	// Try clonefile on the directory itself first (works on APFS)
	err := unix.Clonefile(src, dst, 0)
	if err == nil {
		return nil
	}
	// Fall back to recursive copy
	return copyDirRecursiveDisk(src, dst)
}

// copyDirRecursiveDisk copies a directory recursively (for disk snapshots).
func copyDirRecursiveDisk(src, dst string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dst, srcInfo.Mode()); err != nil {
		return err
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			if err := copyDirRecursiveDisk(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			if err := copyFile(srcPath, dstPath); err != nil {
				return err
			}
		}
	}

	return nil
}

// dirSize calculates the total size of a directory.
func dirSize(path string) (int64, error) {
	var size int64
	err := filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	return size, err
}

// =============================================================================
// Control socket command handlers for snapshots
// =============================================================================

// handleSnapshotCommand handles snapshot commands from the control socket
func (s *ControlServer) handleSnapshotCommand(cmd *controlpb.SnapshotCommand) *controlpb.ControlResponse {
	if s.vm.ID == 0 || s.vmQueue.Handle() == 0 {
		return &controlpb.ControlResponse{Error: "VM not configured"}
	}

	mgr := NewSnapshotManager(vmDir)

	switch cmd.Action {
	case "save":
		if cmd.Name == "" {
			return &controlpb.ControlResponse{Error: "snapshot name required"}
		}
		if err := mgr.Save(s.vm, s.vmQueue, cmd.Name); err != nil {
			return &controlpb.ControlResponse{Error: err.Error()}
		}
		return &controlpb.ControlResponse{Success: true, Data: fmt.Sprintf("snapshot '%s' saved", cmd.Name)}

	case "restore":
		if cmd.Name == "" {
			return &controlpb.ControlResponse{Error: "snapshot name required"}
		}
		if err := mgr.Restore(s.vm, s.vmQueue, cmd.Name); err != nil {
			return &controlpb.ControlResponse{Error: err.Error()}
		}
		return &controlpb.ControlResponse{Success: true, Data: fmt.Sprintf("snapshot '%s' restored (VM paused)", cmd.Name)}

	case "list":
		snapshots, err := mgr.List()
		if err != nil {
			return &controlpb.ControlResponse{Error: err.Error()}
		}
		data, _ := json.Marshal(snapshots)
		return &controlpb.ControlResponse{Success: true, Data: string(data)}

	case "delete":
		if cmd.Name == "" {
			return &controlpb.ControlResponse{Error: "snapshot name required"}
		}
		if err := mgr.Delete(cmd.Name); err != nil {
			return &controlpb.ControlResponse{Error: err.Error()}
		}
		return &controlpb.ControlResponse{Success: true, Data: fmt.Sprintf("snapshot '%s' deleted", cmd.Name)}

	default:
		return &controlpb.ControlResponse{Error: fmt.Sprintf("unknown snapshot action: %s", cmd.Action)}
	}
}

// =============================================================================
// CLI handlers for disk snapshots
// =============================================================================

// handleDiskSnapshotCommand handles the disk-snapshot CLI subcommand.
func handleDiskSnapshotCommand(args []string) {
	if len(args) == 0 {
		printDiskSnapshotUsage()
		return
	}

	mgr := NewDiskSnapshotManager(vmDir)

	switch args[0] {
	case "save":
		handleDiskSnapshotSave(mgr, args[1:])
	case "restore":
		handleDiskSnapshotRestore(mgr, args[1:])
	case "list":
		handleDiskSnapshotList(mgr)
	case "delete":
		handleDiskSnapshotDelete(mgr, args[1:])
	case "help", "-h", "--help":
		printDiskSnapshotUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown disk-snapshot command: %s\n", args[0])
		printDiskSnapshotUsage()
		os.Exit(1)
	}
}

func printDiskSnapshotUsage() {
	fmt.Println(`Usage: vz-macos disk-snapshot <command> [options]

Disk-level snapshots using APFS clonefile (copy-on-write).
Unlike VM state snapshots, these snapshot the actual disk contents.

Commands:
  save <name> [-system] [-userdata] [-both] [-desc "..."]
      Save disk snapshot (default: both disks if present)

  restore <name> [-system] [-userdata] [-both]
      Restore disks from snapshot

  list
      List all disk snapshots

  delete <name>
      Delete a disk snapshot

Examples:
  # Snapshot both system and userdata disks
  vz-macos disk-snapshot save checkpoint1

  # Snapshot only the system disk
  vz-macos disk-snapshot save system-clean -system

  # Restore only userdata from snapshot
  vz-macos disk-snapshot restore checkpoint1 -userdata

  # List all disk snapshots
  vz-macos disk-snapshot list

Note: VM should be stopped for consistent disk snapshots.`)
}

func handleDiskSnapshotSave(mgr *DiskSnapshotManager, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Error: snapshot name required")
		os.Exit(1)
	}

	name := args[0]
	target := DiskSnapshotBoth // Default to both
	description := ""

	// Parse flags
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "-system":
			target = DiskSnapshotSystem
		case "-userdata":
			target = DiskSnapshotUserData
		case "-both":
			target = DiskSnapshotBoth
		case "-desc":
			if i+1 < len(args) {
				i++
				description = args[i]
			}
		}
	}

	fmt.Printf("Creating disk snapshot '%s'...\n", name)
	if err := mgr.Save(name, target, description); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func handleDiskSnapshotRestore(mgr *DiskSnapshotManager, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Error: snapshot name required")
		os.Exit(1)
	}

	name := args[0]
	target := DiskSnapshotBoth // Default to both

	// Parse flags
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "-system":
			target = DiskSnapshotSystem
		case "-userdata":
			target = DiskSnapshotUserData
		case "-both":
			target = DiskSnapshotBoth
		}
	}

	fmt.Printf("Restoring disk snapshot '%s'...\n", name)
	if err := mgr.Restore(name, target); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func handleDiskSnapshotList(mgr *DiskSnapshotManager) {
	snapshots, err := mgr.List()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if len(snapshots) == 0 {
		fmt.Println("No disk snapshots found.")
		return
	}

	fmt.Println("Disk snapshots:")
	fmt.Printf("  %-20s  %-10s  %-12s  %-12s  %s\n", "NAME", "TARGET", "SYSTEM", "USERDATA", "CREATED")
	for _, s := range snapshots {
		targetStr := "both"
		if s.Target == DiskSnapshotSystem {
			targetStr = "system"
		} else if s.Target == DiskSnapshotUserData {
			targetStr = "userdata"
		}

		systemStr := "-"
		if s.SystemSize > 0 {
			systemStr = FormatSize(s.SystemSize)
		}
		userdataStr := "-"
		if s.UserDataSize > 0 {
			userdataStr = FormatSize(s.UserDataSize)
		}

		fmt.Printf("  %-20s  %-10s  %-12s  %-12s  %s\n",
			s.Name, targetStr, systemStr, userdataStr, s.Created.Format("2006-01-02 15:04"))
	}
}

func handleDiskSnapshotDelete(mgr *DiskSnapshotManager, args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Error: snapshot name required")
		os.Exit(1)
	}

	name := args[0]
	if err := mgr.Delete(name); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
