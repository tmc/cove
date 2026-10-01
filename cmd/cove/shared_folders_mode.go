package main

import (
	"fmt"
	"path/filepath"
	"time"
)

func parseSharedFolderMode(mode string) (bool, error) {
	switch mode {
	case "ro":
		return true, nil
	case "rw":
		return false, nil
	default:
		return false, fmt.Errorf("invalid shared folder mode %q: use ro or rw", mode)
	}
}

func setSharedFolderMode(folders []SharedFolderEntry, selector string, readOnly bool) ([]SharedFolderEntry, SharedFolderEntry, error) {
	selected := -1
	for i, f := range folders {
		if f.Tag == selector {
			selected = i
			break
		}
	}
	if selected < 0 {
		path, err := filepath.Abs(resolvePath(selector))
		if err != nil {
			return nil, SharedFolderEntry{}, fmt.Errorf("resolve shared folder: %w", err)
		}
		for i, f := range folders {
			if f.Path == path {
				selected = i
				break
			}
		}
	}
	if selected < 0 {
		return nil, SharedFolderEntry{}, fmt.Errorf("%w: %q", ErrSharedFolderNotFound, selector)
	}
	updated := append([]SharedFolderEntry(nil), folders...)
	updated[selected].ReadOnly = readOnly
	return updated, updated[selected], nil
}

func handleVMSharedFolderMode(vmDirectory, selector string, readOnly bool) error {
	folders, entry, err := setSharedFolderMode(LoadSharedFolders(vmDirectory), selector, readOnly)
	if err != nil {
		return err
	}
	if err := saveSharedFolders(vmDirectory, folders); err != nil {
		return err
	}
	mode := "read/write"
	if readOnly {
		mode = "read-only"
	}
	fmt.Printf("Saved shared folder %q as %s\n", entry.Tag, mode)
	client := NewControlClient(GetControlSocketPathForVM(vmDirectory))
	client.SetTimeout(time.Minute)
	msg, err := client.SharedFoldersApply()
	if err != nil {
		if sharedFolderVMStopped(err) {
			fmt.Println("VM is stopped; access mode will apply on next boot")
			return nil
		}
		return fmt.Errorf("access mode saved but not live-applied: %w", err)
	}
	fmt.Printf("applied to running VM: %s\n", msg)
	return nil
}
