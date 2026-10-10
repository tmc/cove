package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/tmc/cove/internal/mutationguard"
	"github.com/tmc/cove/internal/storagepins"
	"github.com/tmc/cove/internal/vmconfig"
	"golang.org/x/sys/unix"
)

func workspaceCleanupTargets(root string, state taskDisposition) ([]workspaceTaskPinTarget, error) {
	if err := validateTaskDisposition(state, true); err != nil {
		return nil, err
	}
	if _, _, err := storagepins.ParseRef("run:" + state.RunID); err != nil {
		return nil, err
	}
	run, err := identifyTaskGuest(filepath.Join(root, "runs", state.RunID))
	if err != nil {
		return nil, err
	}
	return []workspaceTaskPinTarget{{Category: "vm", ID: vmconfig.NameForPath(state.Guest.Path), Identity: *state.Guest}, {Category: "run", ID: state.RunID, Identity: run}}, nil
}

func workspaceCleanupPins(root string, state taskDisposition) (bool, error) {
	targets, err := workspaceCleanupTargets(root, state)
	if err != nil {
		return false, err
	}
	pins, err := storagepins.Load(root)
	if err != nil {
		return false, err
	}
	owner := storagepins.TaskOwner{RunID: state.RunID, AttemptID: state.AttemptID, Generation: state.Generation, PID: state.OwnerPID, StartedAt: state.OwnerStartedAt}
	for _, target := range targets {
		if pins.IsOperatorPinned(target.Category, target.ID) {
			return true, nil
		}
		found := false
		for _, pin := range pins.TaskPins() {
			if pin.Category != target.Category || pin.ID != target.ID {
				continue
			}
			expected := storagepins.DirectoryIdentity{Path: target.Identity.Path, Device: target.Identity.Device, Inode: target.Identity.Inode}
			if pin.Owner != owner || pin.Identity != expected {
				return true, nil
			}
			found = true
		}
		if !found {
			return false, fmt.Errorf("owned cleanup task pin unavailable")
		}
	}
	return false, nil
}

func enableWorkspaceProductionCleanup(d *workspaceCleanupDeps, root string) {
	var guard *mutationguard.Guard
	var state taskDisposition
	capture := d.CaptureRuntime
	d.CaptureRuntime = func(s taskDisposition, generation string) (workspaceRuntimeOwner, error) {
		owner, err := capture(s, generation)
		if err != nil {
			return owner, err
		}
		if s.DiscardRequest != nil {
			if unix.Kill(owner.PID, 0) != unix.ESRCH || !workspaceRuntimeExited(owner) {
				return owner, fmt.Errorf("operator discard requires an exited runtime; retained")
			}
			return owner, nil
		}
		if err := inspectWorkspaceAttachments(s.Guest.Path); err != nil {
			return owner, err
		}
		return owner, nil
	}
	d.PinGuard = func(s taskDisposition) (workspaceCleanupGuard, bool, error) {
		g, err := mutationguard.Acquire(root)
		if err != nil {
			return nil, false, err
		}
		guard = g
		state = s
		durable, err := readTaskDisposition(filepath.Join(root, "runs", s.RunID))
		if err != nil {
			return g, false, err
		}
		if !reflect.DeepEqual(durable, s) {
			return g, false, fmt.Errorf("cleanup authority differs from durable disposition")
		}
		pinned, err := workspaceCleanupPins(root, s)
		return g, pinned, err
	}
	d.DiskHolders = workspaceGuestFileHolders
	d.Delete = func(identity taskGuestIdentity) error {
		if err := guard.Check(root); err != nil {
			return err
		}
		if state.Guest == nil || identity != *state.Guest {
			return fmt.Errorf("cleanup guest identity differs")
		}
		pinned, err := workspaceCleanupPins(root, state)
		if err != nil {
			return err
		}
		if pinned {
			return fmt.Errorf("workspace cleanup pins changed")
		}
		children, err := childVMNames(vmconfig.NameForPath(identity.Path))
		if err != nil {
			return err
		}
		if len(children) != 0 {
			return fmt.Errorf("workspace guest has fork descendants")
		}
		receipt, err := quarantineWorkspaceGuest(root, state, state.Generation, guard)
		if err != nil {
			return err
		}
		if err := deleteWorkspaceQuarantine(root, receipt, state.Generation, guard); err != nil {
			return err
		}
		return removeDiscardedWorkspaceAliases(root, guard, identity)
	}
}

func finalizeDiscardedWorkspaceGuest(state taskDisposition, generation string) error {
	root := coveRoot()
	guard, err := mutationguard.Acquire(root)
	if err != nil {
		return err
	}
	defer guard.Release()
	if state.Guest == nil {
		return fmt.Errorf("discarded workspace guest unavailable")
	}
	targets, err := workspaceCleanupTargets(root, state)
	if err != nil {
		return err
	}
	return releaseWorkspaceTaskPinsWithGuard(root, guard, state, generation, targets, func(guest taskGuestIdentity) error {
		parent, rootIdentity, parentIdentity, err := openWorkspaceQuarantineParent(root, guard)
		if err != nil {
			return err
		}
		defer parent.Close()
		quarantine, err := openWorkspaceQuarantine(parent, false)
		if err != nil {
			return err
		}
		defer quarantine.Close()
		receipt, err := readWorkspaceQuarantineReceipt(quarantine, generation+".json")
		if err != nil {
			return err
		}
		if receipt.Phase != "deleted" || receipt.Task.Generation != generation || *receipt.Task.Guest != guest || receipt.Root != rootIdentity || receipt.Parent != parentIdentity || receipt.Task.RunID != state.RunID || receipt.Task.AttemptID != state.AttemptID || receipt.Task.OwnerPID != state.OwnerPID || receipt.Task.OwnerStartedAt != state.OwnerStartedAt || receipt.Task.Source != state.Source || !reflect.DeepEqual(receipt.Task.SourceGuest, state.SourceGuest) || receipt.Task.Policy != state.Policy || receipt.Task.TaskSucceeded != state.TaskSucceeded || !reflect.DeepEqual(receipt.Task.DiscardRequest, state.DiscardRequest) || !workspaceDiscardAuthorized(state) {
			return fmt.Errorf("workspace deletion evidence differs")
		}
		if err := checkWorkspaceRootIdentity(parent, workspaceQuarantineDirectory, receipt.Quarantine); err != nil {
			return err
		}
		if _, err := quarantine.Lstat(receipt.Name); !os.IsNotExist(err) {
			return fmt.Errorf("workspace quarantine remains or cannot be observed")
		}
		if err := removeDiscardedWorkspaceAliases(root, guard, guest); err != nil {
			return err
		}
		return nil
	})
}

func workspaceGuestFileHolders(dir string) ([]int, error) {
	return workspaceGuestFileHoldersWith(dir, openFileHolderPIDs)
}

func workspaceGuestFileHoldersWith(dir string, holders func(string) ([]int, error)) ([]int, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	seen := map[int]bool{}
	entries := 0
	err = fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path != "." {
			entries++
			if entries > 4096 || strings.Count(path, "/")+1 > 32 {
				return fmt.Errorf("workspace holder inventory exceeds entry or depth bounds")
			}
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("workspace file holder inventory refuses symlinks")
		}
		if entry.IsDir() {
			return nil
		}
		if path == runLockFile {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		absolute := filepath.Join(dir, filepath.FromSlash(path))
		observed, err := os.Lstat(absolute)
		if err != nil || !os.SameFile(info, observed) {
			return fmt.Errorf("workspace holder inventory path identity changed")
		}
		pids, err := holders(absolute)
		if err != nil {
			return err
		}
		for _, pid := range pids {
			seen[pid] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var pids []int
	for pid := range seen {
		pids = append(pids, pid)
	}
	sort.Ints(pids)
	return pids, nil
}

func removeDiscardedWorkspaceAliases(root string, guard *mutationguard.Guard, guest taskGuestIdentity) error {
	if err := guard.Check(root); err != nil {
		return err
	}
	name := vmconfig.NameForPath(guest.Path)
	paths := []string{filepath.Join(root, "vms", name), vmconfig.PackageAliasPath(name), vmconfig.CurrentLink()}
	for _, path := range paths {
		if path == guest.Path {
			continue
		}
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink == 0 {
			continue
		}
		target, err := os.Readlink(path)
		if err != nil {
			return err
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(path), target)
		}
		if filepath.Clean(target) != guest.Path {
			continue
		}
		if err := os.Remove(path); err != nil {
			return err
		}
		if err := syncDiscardAliasParent(filepath.Dir(path)); err != nil {
			return err
		}
	}
	return nil
}
func syncDiscardAliasParent(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func productionWorkspaceCleanupDeps() workspaceCleanupDeps {
	d := defaultWorkspaceCleanupDeps()
	enableWorkspaceProductionCleanup(&d, coveRoot())
	return d
}
