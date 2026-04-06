// snapshots.go - VM state save/restore (snapshots) support
//
// Two types of snapshots are supported:
//
//  1. VM State Snapshots: Save/restore CPU and memory state (fast resume)
//     - Delegates to vzkit.SnapshotManager
//     - Stored in vmDir/snapshots/<name>.vmstate
//     - VM must be paused to save, stopped to restore
//
//  2. Disk Snapshots: Save/restore disk image state (clone the disk)
//     - Uses APFS clonefile for instant copy-on-write snapshots
//     - Stored in vmDir/disk-snapshots/<name>/
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	snapshotx "github.com/tmc/apple/x/vzkit/snapshot"
	vmruntime "github.com/tmc/apple/x/vzkit/vm"
	"golang.org/x/sys/unix"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

// DiskSnapshotTarget specifies which disk(s) to snapshot.
type DiskSnapshotTarget int

const (
	DiskSnapshotSystem DiskSnapshotTarget = 1 << iota // System disk (disk.img)
	DiskSnapshotBoth                      = DiskSnapshotSystem
)

// DiskSnapshotInfo contains metadata about a disk snapshot.
type DiskSnapshotInfo struct {
	Name        string             `json:"name"`
	Created     time.Time          `json:"created"`
	Target      DiskSnapshotTarget `json:"target"`
	SystemSize  int64              `json:"systemSize,omitempty"`
	Description string             `json:"description,omitempty"`
	FilePath    string             `json:"filePath"`
}

// DiskSnapshotManager handles disk-level snapshot operations.
type DiskSnapshotManager struct {
	vmDir string
}

// NewDiskSnapshotManager creates a new disk snapshot manager.
func NewDiskSnapshotManager(vmDir string) *DiskSnapshotManager {
	return &DiskSnapshotManager{vmDir: vmDir}
}

func (m *DiskSnapshotManager) diskSnapshotsDir() string {
	return filepath.Join(m.vmDir, "disk-snapshots")
}

func (m *DiskSnapshotManager) snapshotDir(name string) string {
	return filepath.Join(m.diskSnapshotsDir(), name)
}

func (m *DiskSnapshotManager) metadataPath(name string) string {
	return filepath.Join(m.snapshotDir(name), "metadata.json")
}

func (m *DiskSnapshotManager) ensureDir() error {
	return os.MkdirAll(m.diskSnapshotsDir(), 0755)
}

// validateSnapshotName checks that a snapshot name is safe for use as a
// directory component. Empty names, path separators, and path traversal
// components are rejected.
func validateSnapshotName(name string) error {
	if name == "" {
		return fmt.Errorf("snapshot name must not be empty")
	}
	if strings.ContainsAny(name, "/\\") {
		return fmt.Errorf("snapshot name must not contain path separators: %q", name)
	}
	if name == "." || name == ".." {
		return fmt.Errorf("snapshot name must not be %q", name)
	}
	return nil
}

// Save creates a disk snapshot using APFS clonefile.
// VM should be stopped for consistent snapshots.
func (m *DiskSnapshotManager) Save(name string, target DiskSnapshotTarget, description string) error {
	if err := validateSnapshotName(name); err != nil {
		return err
	}
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
			fmt.Printf("  warning: system disk not found at %s\n", srcPath)
		}
	}

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
	if err := validateSnapshotName(name); err != nil {
		return err
	}
	snapDir := m.snapshotDir(name)
	if _, err := os.Stat(snapDir); os.IsNotExist(err) {
		return fmt.Errorf("disk snapshot '%s' not found", name)
	}

	var info DiskSnapshotInfo
	if metaBytes, err := os.ReadFile(m.metadataPath(name)); err == nil {
		json.Unmarshal(metaBytes, &info)
	}

	if target&DiskSnapshotSystem != 0 {
		srcPath := filepath.Join(snapDir, "disk.img")
		dstPath := filepath.Join(m.vmDir, "disk.img")
		if _, err := os.Stat(srcPath); err == nil {
			fmt.Printf("  Restoring system disk...\n")
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

		metaPath := m.metadataPath(name)
		if metaBytes, err := os.ReadFile(metaPath); err == nil {
			json.Unmarshal(metaBytes, &info)
		}

		if info.Name == "" {
			info.Name = name
		}
		if fileInfo, err := entry.Info(); err == nil && info.Created.IsZero() {
			info.Created = fileInfo.ModTime()
		}
		info.FilePath = filepath.Join(dir, entry.Name())

		snapshots = append(snapshots, info)
	}

	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].Created.After(snapshots[j].Created)
	})

	return snapshots, nil
}

// Delete removes a disk snapshot.
func (m *DiskSnapshotManager) Delete(name string) error {
	if err := validateSnapshotName(name); err != nil {
		return err
	}
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

func (m *DiskSnapshotManager) cloneFileWithFallback(src, dst string) error {
	err := unix.Clonefile(src, dst, 0)
	if err == nil {
		return nil
	}
	return copyFile(src, dst)
}

// =============================================================================
// Control socket command handlers for snapshots
// =============================================================================

func (s *ControlServer) handleSnapshotCommand(cmd *controlpb.SnapshotCommand) *controlpb.ControlResponse {
	if s.vm.ID == 0 || s.vmQueue.Handle() == 0 {
		return &controlpb.ControlResponse{Error: "VM not configured"}
	}

	mgr := snapshotx.NewManager(vmDir)

	switch cmd.Action {
	case "save":
		if cmd.Name == "" {
			return &controlpb.ControlResponse{Error: "snapshot name required"}
		}
		if err := mgr.Save(s.vm, vmruntime.WrapQueue(s.vmQueue), cmd.Name); err != nil {
			return &controlpb.ControlResponse{Error: err.Error()}
		}
		msg := fmt.Sprintf("snapshot '%s' saved", cmd.Name)
		return &controlpb.ControlResponse{Success: true, Data: msg, Result: &controlpb.ControlResponse_SnapshotAction{SnapshotAction: &controlpb.SnapshotActionResponse{Message: msg}}}

	case "restore":
		if cmd.Name == "" {
			return &controlpb.ControlResponse{Error: "snapshot name required"}
		}
		if err := mgr.Restore(s.vm, vmruntime.WrapQueue(s.vmQueue), cmd.Name); err != nil {
			return &controlpb.ControlResponse{Error: err.Error()}
		}
		msg := fmt.Sprintf("snapshot '%s' restored (VM paused)", cmd.Name)
		return &controlpb.ControlResponse{Success: true, Data: msg, Result: &controlpb.ControlResponse_SnapshotAction{SnapshotAction: &controlpb.SnapshotActionResponse{Message: msg}}}

	case "list":
		snapshots, err := mgr.List()
		if err != nil {
			return &controlpb.ControlResponse{Error: err.Error()}
		}
		data, _ := json.Marshal(snapshots)
		var pbSnapshots []*controlpb.SnapshotInfo
		for _, s := range snapshots {
			pbSnapshots = append(pbSnapshots, &controlpb.SnapshotInfo{
				Name:     s.Name,
				Created:  s.Created.Format(time.RFC3339),
				Size:     s.Size,
				VmState:  s.VMState,
				FilePath: s.FilePath,
			})
		}
		return &controlpb.ControlResponse{Success: true, Data: string(data), Result: &controlpb.ControlResponse_SnapshotList{SnapshotList: &controlpb.SnapshotListResponse{Snapshots: pbSnapshots}}}

	case "delete":
		if cmd.Name == "" {
			return &controlpb.ControlResponse{Error: "snapshot name required"}
		}
		if err := mgr.Delete(cmd.Name); err != nil {
			return &controlpb.ControlResponse{Error: err.Error()}
		}
		msg := fmt.Sprintf("snapshot '%s' deleted", cmd.Name)
		return &controlpb.ControlResponse{Success: true, Data: msg, Result: &controlpb.ControlResponse_SnapshotAction{SnapshotAction: &controlpb.SnapshotActionResponse{Message: msg}}}

	default:
		return &controlpb.ControlResponse{Error: fmt.Sprintf("unknown snapshot action: %s", cmd.Action)}
	}
}

// =============================================================================
// CLI handlers for disk snapshots
// =============================================================================

func handleDiskSnapshotCommand(args []string) error {
	if len(args) == 0 {
		printDiskSnapshotUsage()
		return nil
	}

	mgr := NewDiskSnapshotManager(vmDir)

	switch args[0] {
	case "save":
		return handleDiskSnapshotSave(mgr, args[1:])
	case "restore":
		return handleDiskSnapshotRestore(mgr, args[1:])
	case "list":
		return handleDiskSnapshotList(mgr)
	case "delete":
		return handleDiskSnapshotDelete(mgr, args[1:])
	case "help", "-h", "--help":
		printDiskSnapshotUsage()
		return nil
	default:
		printDiskSnapshotUsage()
		return fmt.Errorf("unknown disk-snapshot command: %s\nRun 'vz-macos -help' for usage.", args[0])
	}
}

func printDiskSnapshotUsage() {
	fmt.Println(`Usage: vz-macos disk-snapshot <command> [options]

Disk-level snapshots using APFS clonefile (copy-on-write).
Unlike VM state snapshots, these snapshot the actual disk contents.

Commands:
  save <name> [-system] [-desc "..."]
      Save disk snapshot

  restore <name> [-system]
      Restore disks from snapshot

  list
      List all disk snapshots

  delete <name>
      Delete a disk snapshot

Examples:
  # Snapshot system disk
  vz-macos disk-snapshot save checkpoint1

  # List all disk snapshots
  vz-macos disk-snapshot list

Note: VM should be stopped for consistent disk snapshots.`)
}

func handleDiskSnapshotSave(mgr *DiskSnapshotManager, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("snapshot name required")
	}

	name := args[0]
	target := DiskSnapshotBoth
	description := ""

	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "-system":
			target = DiskSnapshotSystem
		case "-desc":
			if i+1 < len(args) {
				i++
				description = args[i]
			}
		}
	}

	fmt.Printf("Creating disk snapshot '%s'...\n", name)
	if err := mgr.Save(name, target, description); err != nil {
		return err
	}
	return nil
}

func handleDiskSnapshotRestore(mgr *DiskSnapshotManager, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("snapshot name required")
	}

	name := args[0]
	target := DiskSnapshotBoth

	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "-system":
			target = DiskSnapshotSystem
		}
	}

	fmt.Printf("Restoring disk snapshot '%s'...\n", name)
	if err := mgr.Restore(name, target); err != nil {
		return err
	}
	return nil
}

func handleDiskSnapshotList(mgr *DiskSnapshotManager) error {
	snapshots, err := mgr.List()
	if err != nil {
		return err
	}

	if len(snapshots) == 0 {
		fmt.Println("No disk snapshots found.")
		return nil
	}

	fmt.Println("Disk snapshots:")
	fmt.Printf("  %-20s  %-12s  %s\n", "NAME", "SYSTEM", "CREATED")
	for _, s := range snapshots {
		systemStr := "-"
		if s.SystemSize > 0 {
			systemStr = FormatSize(s.SystemSize)
		}

		fmt.Printf("  %-20s  %-12s  %s\n",
			s.Name, systemStr, s.Created.Format("2006-01-02 15:04"))
	}
	return nil
}

func handleDiskSnapshotDelete(mgr *DiskSnapshotManager, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("snapshot name required")
	}

	name := args[0]
	ok, err := confirmDeletef("Delete disk snapshot %q? This cannot be undone. [y/N] ", name)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if err := mgr.Delete(name); err != nil {
		return err
	}
	return nil
}

// snapshotViaControlSocket sends a snapshot save or restore command to the
// running VM via its control socket. Returns an error if the VM is not running
// or the operation fails.
func snapshotViaControlSocket(action, name string) error {
	sock := GetControlSocketPath()
	req := &controlpb.ControlRequest{
		Type: "snapshot",
		Command: &controlpb.ControlRequest_Snapshot{
			Snapshot: &controlpb.SnapshotCommand{
				Action: action,
				Name:   name,
			},
		},
	}
	resp, err := ctlSendRequest(sock, req, 30*time.Second, "snapshot")
	if err != nil {
		return fmt.Errorf("%w\n\nNote: save/restore require a running VM with the control socket active", err)
	}
	return ctlPrintResponse(resp, "snapshot", false, "")
}
