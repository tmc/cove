package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tmc/cove/internal/bytefmt"
	"github.com/tmc/cove/internal/vmconfig"
)

func runDiskCommand(env commandEnv, _ string, args []string) int {
	env = env.WithDefaultIO()
	if len(args) == 0 || isHelpArg(args[0]) {
		printDiskUsage(env.Stderr)
		return usageExitCode(args)
	}
	return commandError(env, handleDiskCommand(env, args))
}

func printDiskUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage:
  cove disk resize <vm> <size>
  cove -vm <vm> disk resize <size>

Resize stopped VM disk images.

Use cove ctl -vm <vm> disk resize 0 <size> for running VMs. For macOS
primary disks, the live ctl path also expands the guest APFS container.`)
}

func handleDiskCommand(env commandEnv, args []string) error {
	switch args[0] {
	case "resize":
		vm, size, err := parseDiskResizeArgs(args[1:])
		if err != nil {
			return err
		}
		return resizeStoppedVMDisk(env.Stdout, vm, size, defaultStoppedDiskResizeDeps())
	default:
		return fmt.Errorf("unknown disk command: %s", args[0])
	}
}

func parseDiskResizeArgs(args []string) (string, string, error) {
	switch len(args) {
	case 1:
		if strings.TrimSpace(vmName) == "" {
			return "", "", fmt.Errorf("usage: cove disk resize <vm> <size>")
		}
		return vmName, args[0], nil
	case 2:
		return args[0], args[1], nil
	default:
		return "", "", fmt.Errorf("usage: cove disk resize <vm> <size>")
	}
}

type stoppedDiskResizeDeps struct {
	acquireRunLock func(string) (*RunLock, error)
	processes      vmProcessCollector
	fileHolders    func(string) ([]int, error)
}

func defaultStoppedDiskResizeDeps() stoppedDiskResizeDeps {
	return stoppedDiskResizeDeps{
		acquireRunLock: AcquireRunLock,
		processes:      defaultVMProcessCollector(),
		fileHolders:    openFileHolderPIDs,
	}
}

func (d stoppedDiskResizeDeps) withDefaults() stoppedDiskResizeDeps {
	if d.acquireRunLock == nil {
		d.acquireRunLock = AcquireRunLock
	}
	if d.processes == nil {
		d.processes = defaultVMProcessCollector()
	}
	if d.fileHolders == nil {
		d.fileHolders = openFileHolderPIDs
	}
	return d
}

func resizeStoppedVMDisk(w io.Writer, name, size string, deps stoppedDiskResizeDeps) error {
	deps = deps.withDefaults()
	vmDirectory, err := requireExistingVMDir("disk resize", name)
	if err != nil {
		return err
	}
	if controlSocketResponds(vmDirectory) {
		return fmt.Errorf("VM %q is running; use: cove ctl -vm %s disk resize 0 %s", name, name, size)
	}
	runLock, err := deps.acquireRunLock(vmDirectory)
	if err != nil {
		if errors.Is(err, ErrRunLockHeld) {
			return fmt.Errorf("VM %q is running; use: cove ctl -vm %s disk resize 0 %s", name, name, size)
		}
		return fmt.Errorf("probe VM run lock: %w", err)
	}
	defer runLock.Release()
	if proc, ok, err := liveVMProcessForDirectory(vmDirectory, deps.processes); err != nil {
		return fmt.Errorf("verify VM %q is stopped: %w", name, err)
	} else if ok {
		return fmt.Errorf("VM %q appears to be running in PID %d; stop it before resizing the stopped disk, or use: cove ctl -vm %s disk resize 0 %s", name, proc.PID, name, size)
	}

	sizeBytes, err := bytefmt.Parse(size)
	if err != nil {
		return err
	}
	if sizeBytes > uint64(maxInt64) {
		return fmt.Errorf("size too large")
	}

	diskPath := vmPrimaryDiskPath(vmDirectory)
	info, err := os.Stat(diskPath)
	if err != nil {
		return fmt.Errorf("stat disk image: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("disk image path is a directory: %s", diskPath)
	}
	holders, err := deps.fileHolders(diskPath)
	if err != nil {
		return fmt.Errorf("verify disk image is closed: %w", err)
	}
	if len(holders) > 0 {
		return fmt.Errorf("VM %q disk image is open by PID %d; stop the owner before resizing the stopped disk, or use: cove ctl -vm %s disk resize 0 %s", name, holders[0], name, size)
	}
	current := uint64(info.Size())
	if sizeBytes < current {
		return fmt.Errorf("disk resize can only grow %s: current size is %d bytes, requested %d bytes", diskPath, current, sizeBytes)
	}
	if sizeBytes == current {
		fmt.Fprintf(w, "disk %s is already %d bytes\n", diskPath, sizeBytes)
		printStoppedResizeNextStep(w, name, size, vmDirectory)
		return nil
	}
	if err := os.Truncate(diskPath, int64(sizeBytes)); err != nil {
		return fmt.Errorf("resize disk image: %w", err)
	}
	fmt.Fprintf(w, "resized %s from %d to %d bytes\n", diskPath, current, sizeBytes)
	printStoppedResizeNextStep(w, name, size, vmDirectory)
	return nil
}

const maxInt64 = int64(^uint64(0) >> 1)

func controlSocketResponds(vmDirectory string) bool {
	sock := GetControlSocketPathForVM(vmDirectory)
	if _, err := os.Stat(sock); err != nil {
		return false
	}
	resp, err := ctlSendJSON(sock, map[string]interface{}{"type": "ping"}, 250*time.Millisecond)
	return err == nil && resp != nil && resp.GetSuccess()
}

func liveVMProcessForDirectory(vmDirectory string, collector vmProcessCollector) (vmProcessInfo, bool, error) {
	if collector == nil {
		return vmProcessInfo{}, false, fmt.Errorf("vm process collector required")
	}
	procs, err := collectVMProcessesWithCollector(vmconfig.BaseDir(), collector)
	if err != nil {
		return vmProcessInfo{}, false, err
	}
	target := filepath.Clean(vmDirectory)
	targetReal := vmProcessRealPath(target)
	for _, proc := range procs {
		if vmProcessHasDirectory(proc, target, targetReal) {
			return proc, true, nil
		}
	}
	return vmProcessInfo{}, false, nil
}

func vmProcessHasDirectory(proc vmProcessInfo, target, targetReal string) bool {
	for _, dir := range proc.VMDirs {
		dir = filepath.Clean(dir)
		if dir == target {
			return true
		}
		if targetReal != "" && vmProcessRealPath(dir) == targetReal {
			return true
		}
	}
	return false
}

func printStoppedResizeNextStep(w io.Writer, name, size, vmDirectory string) {
	switch vmconfig.DetectOSType(vmDirectory) {
	case "macOS":
		fmt.Fprintf(w, "next: boot the VM, then run `cove ctl -vm %s disk resize 0 %s` to expand APFS\n", name, size)
	case "Linux":
		fmt.Fprintln(w, "next: boot the VM and grow the guest partition/filesystem with the distro's disk tools")
	case "Windows":
		fmt.Fprintln(w, "next: boot the VM and extend the guest volume in Windows")
	}
}
