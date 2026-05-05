package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tmc/vz-macos/internal/vmconfig"
	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

const defaultSharedFoldersMountPoint = "/Volumes/My Shared Files"

type sharedFolderMountTimeouts struct {
	agentPing time.Duration
	mkdir     time.Duration
	mounts    time.Duration
	listTags  time.Duration
	unmount   time.Duration
	mount     time.Duration
}

func defaultSharedFolderMountTimeouts() sharedFolderMountTimeouts {
	return sharedFolderMountTimeouts{
		agentPing: 5 * time.Second,
		mkdir:     10 * time.Second,
		mounts:    10 * time.Second,
		listTags:  5 * time.Second,
		unmount:   10 * time.Second,
		mount:     15 * time.Second,
	}
}

func handleVMSharedFolderCommand(args []string) error {
	if len(args) == 0 {
		printSharedFolderUsage(os.Stderr)
		return fmt.Errorf("command required")
	}
	switch args[0] {
	case "help", "-h", "--help":
		printSharedFolderUsage(os.Stderr)
		return nil
	}

	targetDir, err := sharedFolderCommandVMDir()
	if err != nil {
		return err
	}

	switch args[0] {
	case "list":
		return listSharedFolders(targetDir)
	case "status":
		mountPoint := defaultSharedFoldersMountRoot(targetDir)
		if len(args) >= 2 {
			mountPoint = args[1]
		}
		return sharedFolderStatus(targetDir, mountPoint)
	case "pending":
		if len(args) > 2 {
			return fmt.Errorf("usage: cove shared-folder pending [vm]")
		}
		if len(args) == 2 {
			var err error
			targetDir, err = vmconfig.EnsureDir(args[1], vmDir)
			if err != nil {
				return err
			}
		}
		return pendingSharedFolders(targetDir, defaultSharedFoldersMountRoot(targetDir))
	case "add":
		return handleVMSharedFolderAdd(targetDir, args[1:])
	case "remove":
		if len(args) < 2 {
			return fmt.Errorf("usage: cove shared-folder remove <tag-or-path>")
		}
		return handleVMSharedFolderRemove(targetDir, args[1])
	case "clear":
		return handleVMSharedFolderClear(targetDir)
	case "mount":
		mountPoint := defaultSharedFoldersMountRoot(targetDir)
		if len(args) >= 2 {
			mountPoint = args[1]
		}
		mounted, err := mountSharedFoldersInGuest(targetDir, mountPoint)
		if err != nil {
			return err
		}
		if mounted {
			if mountPoint == "" {
				fmt.Printf("Mounted shared folders at %s\n", sharedFoldersMountSummary(targetDir))
				return nil
			}
			fmt.Printf("Mounted shared folders at %s\n", mountPoint)
		} else {
			if mountPoint == "" {
				fmt.Printf("Shared folders already mounted at %s\n", sharedFoldersMountSummary(targetDir))
				return nil
			}
			fmt.Printf("Shared folders already mounted at %s\n", mountPoint)
		}
		return nil
	default:
		return fmt.Errorf("unknown shared-folder command: %s\nValid commands: help, list, status, pending, add, remove, clear, mount", args[0])
	}
}

func sharedFolderCommandVMDir() (string, error) {
	if vmName != "" {
		return vmconfig.EnsureDir(vmName, vmDir)
	}
	if vmDir == "" {
		return vmconfig.EnsureDir("", vmDir)
	}
	if filepath.IsAbs(vmDir) {
		return vmDir, nil
	}
	return vmconfig.Path(vmDir), nil
}

func sharedFolderVMName(vmDirectory string) string {
	if vmName != "" {
		return vmName
	}
	name := filepath.Base(vmDirectory)
	if name == "" || name == "." || name == string(filepath.Separator) {
		return "default"
	}
	return name
}

func defaultSharedFolderMountPoint(vmDirectory, tag string) string {
	if vmconfig.DetectOSType(vmDirectory) == "Linux" {
		return "/mnt/" + tag
	}
	return filepath.Join(defaultSharedFoldersMountPoint, tag)
}

func defaultSharedFoldersMountRoot(vmDirectory string) string {
	if vmconfig.DetectOSType(vmDirectory) == "Linux" {
		return ""
	}
	return defaultSharedFoldersMountPoint
}

func sharedFoldersMountSummary(vmDirectory string) string {
	folders := LoadSharedFolders(vmDirectory)
	if len(folders) == 0 {
		return "(none)"
	}
	paths := make([]string, 0, len(folders))
	for _, f := range folders {
		paths = append(paths, defaultSharedFolderMountPoint(vmDirectory, f.Tag))
	}
	return strings.Join(paths, ", ")
}

func handleVMSharedFolderAdd(vmDirectory string, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: cove shared-folder add <host-path> [tag] [ro|rw]")
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
		return fmt.Errorf("usage: cove shared-folder add <host-path> [tag] [ro|rw]")
	}

	entry, added, err := addSharedFolderEntry(vmDirectory, hostPath, tag, readOnly)
	if err != nil {
		return err
	}

	client := NewControlClient(GetControlSocketPathForVM(vmDirectory))
	client.SetTimeout(15 * time.Second)
	status, err := client.SharedFoldersRuntimeStatus()
	if err != nil || !status.VirtioFS {
		fmt.Printf("shared folder saved; will mount on next boot of %s (this VM was not booted with VirtioFS device, so live attach is not possible)\n", sharedFolderVMName(vmDirectory))
		return nil
	}

	printSharedFolderAddResult(entry, added)
	fmt.Println("shared folder saved; applying to running VM ...")
	if msg, err := client.SharedFoldersApply(); err == nil {
		fmt.Printf("applied to running VM: %s\n", msg)
	} else {
		handled := false
		if strings.Contains(err.Error(), "unknown command type: shared-folders-apply") {
			fmt.Println("warning: running VM control server does not support shared-folders-apply")
			fmt.Println("         restart VM with the latest binary to live-apply in this session")
			handled = true
		}
		if !handled {
			fmt.Printf("warning: could not live-apply shared folders to VM %q: %v\n", sharedFolderVMName(vmDirectory), err)
		}
		fmt.Println("         share is saved and will apply on next boot")
		return nil
	}

	mountRoot := defaultSharedFoldersMountRoot(vmDirectory)
	mounted, err := mountSharedFoldersInGuest(vmDirectory, mountRoot)
	if err != nil {
		fmt.Printf("warning: could not mount in guest: %v\n", err)
		if mountRoot == "" {
			fmt.Printf("         you can retry with: cove -vm %s shared-folder mount\n", sharedFolderVMName(vmDirectory))
		} else {
			fmt.Printf("         you can retry with: cove -vm %s shared-folder mount %q\n", sharedFolderVMName(vmDirectory), mountRoot)
		}
		return nil
	}
	mountSummary := sharedFoldersMountSummary(vmDirectory)
	if mounted {
		fmt.Printf("mounted in guest at %s\n", mountSummary)
	} else {
		fmt.Printf("already mounted in guest at %s\n", mountSummary)
	}
	fmt.Printf("Guest path for this folder: %s\n", defaultSharedFolderMountPoint(vmDirectory, entry.Tag))
	return nil
}

func printSharedFolderAddResult(entry SharedFolderEntry, added bool) {
	if added {
		mode := "rw"
		if entry.ReadOnly {
			mode = "ro"
		}
		fmt.Printf("Added shared folder: %s (tag=%s, %s)\n", entry.Path, entry.Tag, mode)
		return
	}
	fmt.Printf("Shared folder already configured: %s (tag=%s)\n", entry.Path, entry.Tag)
}

func handleVMSharedFolderRemove(vmDirectory, selector string) error {
	removed, err := removeSharedFolderEntry(vmDirectory, selector)
	if err != nil {
		return err
	}
	if !removed {
		return fmt.Errorf("no shared folder matches %q", selector)
	}
	fmt.Printf("Removed shared folder %q\n", selector)
	return applySharedFoldersAndPrint(vmDirectory)
}

func handleVMSharedFolderClear(vmDirectory string) error {
	if err := saveSharedFolders(vmDirectory, nil); err != nil {
		return err
	}
	fmt.Println("Cleared all shared folders")
	return applySharedFoldersAndPrint(vmDirectory)
}

func applySharedFoldersAndPrint(vmDirectory string) error {
	client := NewControlClient(GetControlSocketPathForVM(vmDirectory))
	client.SetTimeout(15 * time.Second)
	if msg, err := client.SharedFoldersApply(); err == nil {
		fmt.Printf("applied to running VM: %s\n", msg)
		return nil
	} else {
		if strings.Contains(err.Error(), "unknown command type: shared-folders-apply") {
			fmt.Println("warning: running VM control server does not support shared-folders-apply")
			fmt.Println("         restart VM with the latest binary to live-apply in this session")
		} else {
			fmt.Printf("warning: could not live-apply shared folders to VM %q: %v\n", sharedFolderVMName(vmDirectory), err)
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
	if sharedFoldersUsePerTagMounts(vmDirectory, mountPoint) {
		for _, f := range folders {
			tagMountPoint := defaultSharedFolderMountPoint(vmDirectory, f.Tag)
			if strings.Contains(mountRes.Stdout, " on "+tagMountPoint+" ") {
				fmt.Printf("Guest mount %s: mounted at %s\n", f.Tag, tagMountPoint)
			} else {
				fmt.Printf("Guest mount %s: not mounted at %s\n", f.Tag, tagMountPoint)
			}
		}
		return nil
	}
	if strings.Contains(mountRes.Stdout, " on "+mountPoint+" ") {
		fmt.Printf("Guest mount: mounted at %s\n", mountPoint)
		return nil
	}
	fmt.Printf("Guest mount: not mounted at %s\n", mountPoint)
	return nil
}

func pendingSharedFolders(vmDirectory, mountPoint string) error {
	folders := LoadSharedFolders(vmDirectory)
	if len(folders) == 0 {
		fmt.Println("No shared folders configured.")
		return nil
	}

	mounted, err := mountedSharedFolderTags(vmDirectory, mountPoint)
	if err != nil {
		fmt.Printf("Running VM mount status unavailable: %v\n", err)
		mounted = map[string]bool{}
	}

	pending := make([]SharedFolderEntry, 0, len(folders))
	for _, f := range folders {
		if !mounted[f.Tag] {
			pending = append(pending, f)
		}
	}
	if len(pending) == 0 {
		fmt.Println("No pending shared folders.")
		return nil
	}

	fmt.Printf("Pending shared folders for next boot of %s:\n", sharedFolderVMName(vmDirectory))
	for _, f := range pending {
		mode := "rw"
		if f.ReadOnly {
			mode = "ro"
		}
		fmt.Printf("  %s\t%s\t%s\n", f.Tag, mode, f.Path)
	}
	return nil
}

func mountedSharedFolderTags(vmDirectory, mountPoint string) (map[string]bool, error) {
	client := NewControlClient(GetControlSocketPathForVM(vmDirectory))
	client.SetTimeout(10 * time.Second)
	status, err := client.SharedFoldersRuntimeStatus()
	if err != nil {
		return nil, err
	}
	if !status.VirtioFS {
		return map[string]bool{}, nil
	}
	if _, err := client.AgentPingTyped(); err != nil {
		return nil, fmt.Errorf("guest agent unavailable: %w", err)
	}
	mountRes, err := client.AgentExecTyped([]string{"mount"}, nil, "")
	if err != nil {
		return nil, fmt.Errorf("query mounts: %w", err)
	}
	if sharedFoldersUsePerTagMounts(vmDirectory, mountPoint) {
		mounted := map[string]bool{}
		for _, f := range LoadSharedFolders(vmDirectory) {
			tagMountPoint := defaultSharedFolderMountPoint(vmDirectory, f.Tag)
			if strings.Contains(mountRes.Stdout, " on "+tagMountPoint+" ") {
				mounted[f.Tag] = true
			}
		}
		return mounted, nil
	}
	if !strings.Contains(mountRes.Stdout, " on "+mountPoint+" ") {
		return map[string]bool{}, nil
	}
	lsRes, err := client.AgentExecTyped([]string{"ls", "-1", mountPoint}, nil, "")
	if err != nil {
		return nil, fmt.Errorf("list mounted shared folders: %w", err)
	}
	if lsRes.ExitCode != 0 {
		msg := strings.TrimSpace(lsRes.Stderr)
		if msg == "" {
			msg = strings.TrimSpace(lsRes.Stdout)
		}
		if msg == "" {
			msg = "unknown error"
		}
		return nil, fmt.Errorf("list mounted shared folders: exit %d: %s", lsRes.ExitCode, msg)
	}
	mounted := map[string]bool{}
	for _, line := range strings.Split(lsRes.Stdout, "\n") {
		tag := strings.TrimSpace(line)
		if tag != "" {
			mounted[tag] = true
		}
	}
	return mounted, nil
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
	return mountSharedFoldersInGuestWithTimeouts(vmDirectory, mountPoint, defaultSharedFolderMountTimeouts())
}

func mountSharedFoldersInGuestWithTimeouts(vmDirectory, mountPoint string, timeouts sharedFolderMountTimeouts) (bool, error) {
	if sharedFoldersUsePerTagMounts(vmDirectory, mountPoint) {
		return mountSharedFolderTagsInGuestWithTimeouts(vmDirectory, timeouts)
	}

	client := NewControlClient(GetControlSocketPathForVM(vmDirectory))

	client.SetTimeout(timeouts.agentPing)
	if _, err := client.AgentPingTyped(); err != nil {
		return false, fmt.Errorf("guest agent unavailable: %w", err)
	}

	if _, err := client.AgentExecTypedTimeout([]string{"mkdir", "-p", mountPoint}, nil, "", timeouts.mkdir); err != nil {
		return false, fmt.Errorf("create mount point %q: %w", mountPoint, err)
	}

	mountRes, err := client.AgentExecTypedTimeout([]string{"mount"}, nil, "", timeouts.mounts)
	if err != nil {
		return false, fmt.Errorf("query mounts: %w", err)
	}
	tags := expectedSharedFolderTags(vmDirectory)
	if strings.Contains(mountRes.Stdout, " on "+mountPoint+" ") {
		lsRes, lsErr := client.AgentExecTypedTimeout([]string{"ls", "-1", mountPoint}, nil, "", timeouts.listTags)
		if sharedFoldersMountedAndSynced(mountRes.Stdout, mountPoint, tags, lsRes, lsErr) {
			return false, nil
		}
		if lsErr != nil {
			return false, fmt.Errorf("inspect mounted shared folders at %q: %w", mountPoint, lsErr)
		}
		if lsRes == nil {
			return false, fmt.Errorf("inspect mounted shared folders at %q: missing response", mountPoint)
		}
		if lsRes.ExitCode != 0 {
			msg := strings.TrimSpace(lsRes.Stderr)
			if msg == "" {
				msg = strings.TrimSpace(lsRes.Stdout)
			}
			if msg == "" {
				msg = "unknown error"
			}
			return false, fmt.Errorf("inspect mounted shared folders at %q: exit %d: %s", mountPoint, lsRes.ExitCode, msg)
		}

		// Refresh mounted view to pick up newly hotplugged or removed tags.
		if _, err := client.AgentExecTypedTimeout([]string{"umount", mountPoint}, nil, "", timeouts.unmount); err != nil && !strings.Contains(strings.ToLower(err.Error()), "not currently mounted") {
			return false, fmt.Errorf("remount shared folders: unmount %q: %w", mountPoint, err)
		}
		if _, err := client.AgentExecTypedTimeout([]string{"mkdir", "-p", mountPoint}, nil, "", timeouts.mkdir); err != nil {
			return false, fmt.Errorf("recreate mount point %q: %w", mountPoint, err)
		}
	}

	mountArgs := sharedFoldersVirtioFSMountArgs(vmDirectory, mountPoint)
	res, err := client.AgentExecTypedTimeout(mountArgs, nil, "", timeouts.mount)
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
		return false, fmt.Errorf("%s exit %d: %s", mountArgs[0], res.ExitCode, msg)
	}
	return true, nil
}

func mountSharedFolderTagsInGuestWithTimeouts(vmDirectory string, timeouts sharedFolderMountTimeouts) (bool, error) {
	client := NewControlClient(GetControlSocketPathForVM(vmDirectory))

	client.SetTimeout(timeouts.agentPing)
	if _, err := client.AgentPingTyped(); err != nil {
		return false, fmt.Errorf("guest agent unavailable: %w", err)
	}

	mountRes, err := client.AgentExecTypedTimeout([]string{"mount"}, nil, "", timeouts.mounts)
	if err != nil {
		return false, fmt.Errorf("query mounts: %w", err)
	}

	mountedAny := false
	for _, f := range LoadSharedFolders(vmDirectory) {
		mountPoint := defaultSharedFolderMountPoint(vmDirectory, f.Tag)
		if _, err := client.AgentExecTypedTimeout([]string{"mkdir", "-p", mountPoint}, nil, "", timeouts.mkdir); err != nil {
			return false, fmt.Errorf("create mount point %q: %w", mountPoint, err)
		}
		if strings.Contains(mountRes.Stdout, " on "+mountPoint+" ") {
			continue
		}
		mountArgs := sharedFolderVirtioFSMountArgs(vmDirectory, f.Tag, mountPoint)
		res, err := client.AgentExecTypedTimeout(mountArgs, nil, "", timeouts.mount)
		if err != nil {
			return false, fmt.Errorf("mount shared folder %s: %w", f.Tag, err)
		}
		if res.ExitCode != 0 {
			msg := strings.TrimSpace(res.Stderr)
			if msg == "" {
				msg = strings.TrimSpace(res.Stdout)
			}
			if msg == "" {
				msg = "unknown error"
			}
			return false, fmt.Errorf("%s %s exit %d: %s", mountArgs[0], f.Tag, res.ExitCode, msg)
		}
		mountedAny = true
	}
	return mountedAny, nil
}

func sharedFoldersVirtioFSMountArgs(vmDirectory, mountPoint string) []string {
	return sharedFolderVirtioFSMountArgs(vmDirectory, SharedFoldersVirtioFSTag, mountPoint)
}

func sharedFolderVirtioFSMountArgs(vmDirectory, tag, mountPoint string) []string {
	linuxGuest := vmconfig.DetectOSType(vmDirectory) == "Linux"
	return virtioFSMountArgsWithOwner(vmconfig.VolumeMount{
		Tag: tag,
	}, mountPoint, linuxGuest, linuxVirtioFSOwner(vmDirectory))
}

func sharedFoldersUsePerTagMounts(vmDirectory, mountPoint string) bool {
	return mountPoint == "" && vmconfig.DetectOSType(vmDirectory) == "Linux"
}

func sharedFoldersMountedAndSynced(mountOutput, mountPoint string, tags []string, lsRes *controlpb.AgentExecResponse, lsErr error) bool {
	if !strings.Contains(mountOutput, " on "+mountPoint+" ") {
		return false
	}
	if lsErr != nil || lsRes == nil || lsRes.ExitCode != 0 {
		return false
	}
	return mountContainsAllTags(lsRes.Stdout, tags)
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

func expectedSharedFolderTags(vmDirectory string) []string {
	folders := LoadSharedFolders(vmDirectory)
	out := make([]string, 0, len(folders))
	for _, f := range folders {
		if strings.TrimSpace(f.Tag) == "" {
			continue
		}
		if _, err := os.Stat(f.Path); err != nil {
			continue
		}
		out = append(out, f.Tag)
	}
	return out
}

func mountContainsAllTags(listing string, tags []string) bool {
	if len(tags) == 0 {
		return true
	}
	seen := make(map[string]bool)
	for _, line := range strings.Split(listing, "\n") {
		name := strings.TrimSpace(line)
		if name == "" {
			continue
		}
		seen[name] = true
	}
	for _, tag := range tags {
		if !seen[tag] {
			return false
		}
	}
	return true
}
