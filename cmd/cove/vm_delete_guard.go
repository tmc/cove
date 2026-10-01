package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"syscall"

	"github.com/tmc/cove/internal/mutationguard"
	"github.com/tmc/cove/internal/storagepins"
	"github.com/tmc/cove/internal/vmconfig"
)

type vmDeletionDeps struct {
	live    func(string) (bool, error)
	holders func(string) ([]int, error)
}

func defaultVMDeletionDeps() vmDeletionDeps {
	return vmDeletionDeps{
		live: func(dir string) (bool, error) {
			procs, err := collectVMProcessesWithCollector(filepath.Join(coveRoot(), "vms"), defaultVMProcessCollector())
			if err != nil {
				return false, err
			}
			for _, proc := range procs {
				if proc.OpenFilesErr != nil {
					return false, fmt.Errorf("vm process %d ownership unavailable: %w", proc.PID, proc.OpenFilesErr)
				}
				if vmProcessHasDirectory(proc, dir, vmProcessRealPath(dir)) {
					return true, nil
				}
			}
			return false, nil
		},
		holders: func(path string) ([]int, error) {
			if runtime.GOOS != "darwin" {
				return nil, fmt.Errorf("file holder observation unsupported")
			}
			return openFileHolderPIDs(path)
		},
	}
}

func admitVMDeletion(guard *mutationguard.Guard, name, dir string, deps vmDeletionDeps) (*RunLock, error) {
	if err := guard.Check(coveRoot()); err != nil {
		return nil, err
	}
	if deps.live == nil || deps.holders == nil {
		return nil, fmt.Errorf("vm deletion ownership observations unavailable")
	}
	pins, err := storagepins.Load(coveRoot())
	if err != nil {
		return nil, fmt.Errorf("load vm deletion pins: %w", err)
	}
	if pins.IsPinned("vm", vmconfig.NameForPath(name)) || pins.IsPinned("vm", vmconfig.NameForPath(dir)) {
		return nil, fmt.Errorf("vm %q is pinned; retained", name)
	}
	if info, err := os.Lstat(filepath.Join(dir, runLockFile)); err == nil {
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("vm deletion run lock is not a regular file; retained")
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	lock, err := AcquireRunLock(dir)
	if err != nil {
		return nil, fmt.Errorf("lock vm deletion: %w", err)
	}
	fail := func(err error) (*RunLock, error) { lock.Release(); return nil, err }
	live, err := deps.live(dir)
	if err != nil {
		return fail(fmt.Errorf("observe vm deletion owner: %w", err))
	}
	if live {
		return fail(fmt.Errorf("vm %q has a live runtime owner; retained", name))
	}
	if err := checkVMDeletionFileHolders(dir, deps.holders); err != nil {
		return fail(err)
	}
	return lock, nil
}

func checkVMDeletionFileHolders(dir string, holders func(string) ([]int, error)) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	count := 0
	var walk func(string, int) error
	walk = func(name string, depth int) error {
		if depth > 32 {
			return fmt.Errorf("vm deletion holder inventory exceeds bound; retained")
		}
		dirFile, err := root.OpenFile(name, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
		if err != nil {
			return err
		}
		defer dirFile.Close()
		for {
			entries, err := dirFile.ReadDir(64)
			if err != nil && err != io.EOF {
				return err
			}
			for _, entry := range entries {
				count++
				if count > 4096 {
					return fmt.Errorf("vm deletion holder inventory exceeds bound; retained")
				}
				path := filepath.Join(name, entry.Name())
				if entry.Type()&os.ModeSymlink != 0 {
					return fmt.Errorf("vm deletion holder inventory has ambiguous symlink %q; retained", path)
				}
				if entry.IsDir() {
					if err := walk(path, depth+1); err != nil {
						return err
					}
					continue
				}
				if !entry.Type().IsRegular() {
					continue
				}
				pids, err := holders(filepath.Join(dir, path))
				if err != nil {
					return fmt.Errorf("observe vm file holders for %q: %w", path, err)
				}
				for _, pid := range pids {
					if path == runLockFile && pid == os.Getpid() {
						continue
					}
					return fmt.Errorf("vm file %q has holder pid %d; retained", path, pid)
				}
			}
			if err == io.EOF {
				return nil
			}
		}
	}
	return walk(".", 0)
}
