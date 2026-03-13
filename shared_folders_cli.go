package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const defaultSharedFoldersMountPoint = "/Volumes/My Shared Files"

func handleVMSharedFolderCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf(`usage: vz-macos vm shared-folder <command>

Commands:
  list
  status [mount-point]
  add <host-path> [tag] [ro|rw]
  remove <tag-or-path>
  clear
  mount [mount-point]`)
	}

	switch args[0] {
	case "list":
		return listSharedFolders(vmDir)
	case "status":
		mountPoint := defaultSharedFoldersMountPoint
		if len(args) >= 2 {
			mountPoint = args[1]
		}
		return sharedFolderStatus(vmDir, mountPoint)
	case "add":
		return handleVMSharedFolderAdd(args[1:])
	case "remove":
		if len(args) < 2 {
			return fmt.Errorf("usage: vz-macos vm shared-folder remove <tag-or-path>")
		}
		return handleVMSharedFolderRemove(args[1])
	case "clear":
		return handleVMSharedFolderClear()
	case "mount":
		mountPoint := defaultSharedFoldersMountPoint
		if len(args) >= 2 {
			mountPoint = args[1]
		}
		mounted, err := mountSharedFoldersInGuest(vmDir, mountPoint)
		if err != nil {
			return err
		}
		if mounted {
			fmt.Printf("Mounted shared folders at %s\n", mountPoint)
		} else {
			fmt.Printf("Shared folders already mounted at %s\n", mountPoint)
		}
		return nil
	default:
		return fmt.Errorf("unknown shared-folder command: %s", args[0])
	}
}

func handleVMSharedFolderAdd(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: vz-macos vm shared-folder add <host-path> [tag] [ro|rw]")
	}
	hostPath := args[0]
	tag := ""
	readOnly := false
	if len(args) >= 2 {
		tag = args[1]
	}
	if len(args) >= 3 {
		mode := strings.ToLower(args[2])
		switch mode {
		case "ro":
			readOnly = true
		case "rw":
			readOnly = false
		default:
			return fmt.Errorf("invalid mode %q (expected ro or rw)", args[2])
		}
	}
	if len(args) > 3 {
		return fmt.Errorf("usage: vz-macos vm shared-folder add <host-path> [tag] [ro|rw]")
	}

	entry, added, err := addSharedFolderEntry(vmDir, hostPath, tag, readOnly)
	if err != nil {
		return err
	}
	if added {
		mode := "rw"
		if entry.ReadOnly {
			mode = "ro"
		}
		fmt.Printf("Added shared folder: %s (tag=%s, %s)\n", entry.Path, entry.Tag, mode)
	} else {
		fmt.Printf("Shared folder already configured: %s (tag=%s)\n", entry.Path, entry.Tag)
	}

	client := NewControlClient(GetControlSocketPathForVM(vmDir))
	client.SetTimeout(15 * time.Second)
	if msg, err := client.SharedFoldersApply(); err == nil {
		fmt.Printf("Applied to running VM: %s\n", msg)
	} else {
		handled := false
		if strings.Contains(err.Error(), "unknown command type: shared-folders-apply") {
			fmt.Println("warning: running VM control server does not support shared-folders-apply")
			fmt.Println("         restart VM with the latest binary to hot-apply in this session")
			handled = true
		}
		if !handled {
			fmt.Printf("warning: could not hot-apply to running VM: %v\n", err)
		}
		fmt.Println("         share is saved and will apply on next boot")
	}

	mounted, err := mountSharedFoldersInGuest(vmDir, defaultSharedFoldersMountPoint)
	if err != nil {
		fmt.Printf("warning: could not mount in guest: %v\n", err)
		vmName := filepath.Base(vmDir)
		if vmName == "" || vmName == "." || vmName == "/" {
			vmName = "default"
		}
		fmt.Printf("         you can retry with: vz-macos -vm %s vm shared-folder mount %q\n", vmName, defaultSharedFoldersMountPoint)
		return nil
	}
	if mounted {
		fmt.Printf("Mounted in guest at %s\n", defaultSharedFoldersMountPoint)
	} else {
		fmt.Printf("Already mounted in guest at %s\n", defaultSharedFoldersMountPoint)
	}
	fmt.Printf("Guest path for this folder: %s/%s\n", defaultSharedFoldersMountPoint, entry.Tag)
	return nil
}

func handleVMSharedFolderRemove(selector string) error {
	removed, err := removeSharedFolderEntry(vmDir, selector)
	if err != nil {
		return err
	}
	if !removed {
		return fmt.Errorf("no shared folder matches %q", selector)
	}
	fmt.Printf("Removed shared folder %q\n", selector)
	return applySharedFoldersAndPrint(vmDir)
}

func handleVMSharedFolderClear() error {
	if err := saveSharedFolders(vmDir, nil); err != nil {
		return err
	}
	fmt.Println("Cleared all shared folders")
	return applySharedFoldersAndPrint(vmDir)
}

func applySharedFoldersAndPrint(vmDirectory string) error {
	client := NewControlClient(GetControlSocketPathForVM(vmDirectory))
	client.SetTimeout(15 * time.Second)
	if msg, err := client.SharedFoldersApply(); err == nil {
		fmt.Printf("Applied to running VM: %s\n", msg)
		return nil
	} else {
		if strings.Contains(err.Error(), "unknown command type: shared-folders-apply") {
			fmt.Println("warning: running VM control server does not support shared-folders-apply")
			fmt.Println("         restart VM with the latest binary to hot-apply in this session")
		} else {
			fmt.Printf("warning: could not hot-apply to running VM: %v\n", err)
		}
		fmt.Println("         changes are saved and will apply on next boot")
		return nil
	}
}

func addSharedFolderEntry(vmDirectory, hostPath, tag string, readOnly bool) (SharedFolderEntry, bool, error) {
	abs, err := normalizeSharedFolderPath(hostPath)
	if err != nil {
		return SharedFolderEntry{}, false, err
	}

	folders := LoadSharedFolders(vmDirectory)
	for _, f := range folders {
		if f.Path == abs {
			return f, false, nil
		}
	}

	if tag == "" {
		tag = uniqueTag(filepath.Base(abs), folders)
	} else {
		for _, f := range folders {
			if f.Tag == tag {
				return SharedFolderEntry{}, false, fmt.Errorf("tag %q already exists", tag)
			}
		}
	}

	entry := SharedFolderEntry{
		Path:     abs,
		Tag:      tag,
		ReadOnly: readOnly,
	}
	folders = append(folders, entry)
	if err := saveSharedFolders(vmDirectory, folders); err != nil {
		return SharedFolderEntry{}, false, err
	}
	return entry, true, nil
}

func normalizeSharedFolderPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("host path is required")
	}
	resolved := resolvePath(path)
	abs, err := filepath.Abs(resolved)
	if err != nil {
		return "", fmt.Errorf("resolve %q: %w", path, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("stat %q: %w", abs, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("path is not a directory: %s", abs)
	}
	return abs, nil
}

func saveSharedFolders(vmDirectory string, folders []SharedFolderEntry) error {
	configPath := filepath.Join(vmDirectory, "shared_folders.json")
	if len(folders) == 0 {
		if err := os.Remove(configPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove shared folder config: %w", err)
		}
		return nil
	}
	data, err := json.MarshalIndent(folders, "", "  ")
	if err != nil {
		return fmt.Errorf("encode shared folders: %w", err)
	}
	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("write shared folder config: %w", err)
	}
	return nil
}

func listSharedFolders(vmDirectory string) error {
	folders := LoadSharedFolders(vmDirectory)
	if len(folders) == 0 {
		fmt.Println("No shared folders configured.")
		return nil
	}
	for _, f := range folders {
		mode := "rw"
		if f.ReadOnly {
			mode = "ro"
		}
		fmt.Printf("%s\t%s\t%s\n", f.Tag, mode, f.Path)
	}
	return nil
}

func sharedFolderStatus(vmDirectory, mountPoint string) error {
	folders := LoadSharedFolders(vmDirectory)
	fmt.Printf("Configured shared folders: %d\n", len(folders))
	for _, f := range folders {
		mode := "rw"
		if f.ReadOnly {
			mode = "ro"
		}
		fmt.Printf("  %s\t%s\t%s\n", f.Tag, mode, f.Path)
	}

	client := NewControlClient(GetControlSocketPathForVM(vmDirectory))
	client.SetTimeout(10 * time.Second)
	if err := client.Ping(); err != nil {
		printSharedFolderStatusError("Control socket: unavailable", err)
		return nil
	}
	fmt.Println("Control socket: available")

	if _, err := client.AgentPingTyped(); err != nil {
		printSharedFolderStatusError("Guest agent: unavailable", err)
		return nil
	}
	fmt.Println("Guest agent: available")

	mountRes, err := client.AgentExecTyped([]string{"mount"}, nil, "")
	if err != nil {
		return fmt.Errorf("query mounts: %w", err)
	}
	if strings.Contains(mountRes.Stdout, " on "+mountPoint+" ") {
		fmt.Printf("Guest mount: mounted at %s\n", mountPoint)
		return nil
	}
	fmt.Printf("Guest mount: not mounted at %s\n", mountPoint)
	return nil
}

func removeSharedFolderEntry(vmDirectory, selector string) (bool, error) {
	normalized := selector
	if strings.HasPrefix(selector, "/") || strings.HasPrefix(selector, "~") {
		p, err := normalizeSharedFolderPath(selector)
		if err == nil {
			normalized = p
		}
	}

	folders := LoadSharedFolders(vmDirectory)
	if len(folders) == 0 {
		return false, nil
	}

	out := folders[:0]
	removed := false
	for _, f := range folders {
		if f.Tag == selector || f.Path == selector || f.Path == normalized {
			removed = true
			continue
		}
		out = append(out, f)
	}
	if !removed {
		return false, nil
	}
	if err := saveSharedFolders(vmDirectory, out); err != nil {
		return false, err
	}
	return true, nil
}

func mountSharedFoldersInGuest(vmDirectory, mountPoint string) (bool, error) {
	client := NewControlClient(GetControlSocketPathForVM(vmDirectory))
	client.SetTimeout(20 * time.Second)

	if _, err := client.AgentPingTyped(); err != nil {
		return false, fmt.Errorf("guest agent unavailable: %w", err)
	}

	if _, err := client.AgentExecTyped([]string{"mkdir", "-p", mountPoint}, nil, ""); err != nil {
		return false, fmt.Errorf("create mount point %q: %w", mountPoint, err)
	}

	mountRes, err := client.AgentExecTyped([]string{"mount"}, nil, "")
	if err != nil {
		return false, fmt.Errorf("query mounts: %w", err)
	}
	if strings.Contains(mountRes.Stdout, " on "+mountPoint+" ") {
		return false, nil
	}

	res, err := client.AgentExecTyped([]string{"mount_virtiofs", SharedFoldersVirtioFSTag, mountPoint}, nil, "")
	if err != nil {
		return false, fmt.Errorf("mount shared folders: %w", err)
	}
	if res.ExitCode != 0 {
		msg := strings.TrimSpace(res.Stderr)
		if msg == "" {
			msg = strings.TrimSpace(res.Stdout)
		}
		if msg == "" {
			msg = "unknown error"
		}
		return false, fmt.Errorf("mount_virtiofs exit %d: %s", res.ExitCode, msg)
	}
	return true, nil
}

func printSharedFolderStatusError(prefix string, err error) {
	fmt.Println(prefix)
	for _, line := range strings.Split(strings.TrimSpace(err.Error()), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fmt.Printf("  %s\n", line)
	}
}
