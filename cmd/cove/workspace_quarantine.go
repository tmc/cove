package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/tmc/cove/internal/mutationguard"
)

const workspaceQuarantineDirectory = ".workspace-quarantine"
const workspaceQuarantineReceiptLimit = 64 << 10

type workspaceQuarantineReceipt struct {
	Version    int               `json:"version"`
	Task       taskDisposition   `json:"task"`
	Root       taskGuestIdentity `json:"root"`
	Parent     taskGuestIdentity `json:"parent"`
	Quarantine taskGuestIdentity `json:"quarantine"`
	Name       string            `json:"name"`
	Phase      string            `json:"phase"`
}

func quarantineWorkspaceGuest(storageRoot string, state taskDisposition, generation string, guard *mutationguard.Guard) (workspaceQuarantineReceipt, error) {
	var receipt workspaceQuarantineReceipt
	if err := validateTaskDisposition(state, true); err != nil {
		return receipt, err
	}
	if state.State != "stopping" || !workspaceDiscardAuthorized(state) || generation != state.Generation {
		return receipt, fmt.Errorf("workspace quarantine requires matching stopping owned discard authority")
	}
	parent, rootIdentity, parentIdentity, err := openWorkspaceQuarantineParent(storageRoot, guard)
	if err != nil {
		return receipt, err
	}
	defer parent.Close()
	name := filepath.Base(state.Guest.Path)
	if filepath.Dir(state.Guest.Path) != parentIdentity.Path || !strings.HasSuffix(name, ".covevm") || strings.HasPrefix(name, ".") {
		return receipt, fmt.Errorf("workspace guest must be an immediate vm directory")
	}
	if err := checkWorkspaceRootIdentity(parent, name, *state.Guest); err != nil {
		return receipt, err
	}
	quarantine, err := openWorkspaceQuarantine(parent, true)
	if err != nil {
		return receipt, err
	}
	defer quarantine.Close()
	quarantineInfo, err := quarantine.Lstat(".")
	if err != nil {
		return receipt, err
	}
	quarantineIdentity, err := workspaceIdentityFromInfo(filepath.Join(parentIdentity.Path, workspaceQuarantineDirectory), quarantineInfo)
	if err != nil {
		return receipt, err
	}
	receipt = workspaceQuarantineReceipt{Version: 1, Task: state, Root: rootIdentity, Parent: parentIdentity, Quarantine: quarantineIdentity, Name: generation + ".covevm", Phase: "prepared"}
	if _, err := quarantine.Lstat(receipt.Name); !os.IsNotExist(err) {
		return receipt, fmt.Errorf("workspace quarantine destination exists or is unavailable")
	}
	if err := writeWorkspaceQuarantineReceipt(quarantine, receipt, false); err != nil {
		return receipt, fmt.Errorf("persist workspace quarantine receipt: %w", err)
	}
	if err := syncWorkspaceRoot(parent); err != nil {
		return receipt, err
	}
	if err := guard.Check(storageRoot); err != nil {
		return receipt, err
	}
	if err := checkWorkspaceQuarantineParent(storageRoot, parentIdentity, guard); err != nil {
		return receipt, err
	}
	if err := checkWorkspaceRootIdentity(parent, name, *state.Guest); err != nil {
		return receipt, err
	}
	if err := checkWorkspaceRootIdentity(parent, workspaceQuarantineDirectory, receipt.Quarantine); err != nil {
		return receipt, err
	}
	// The mutation guard excludes Cove renames and collision creation.
	if _, err := quarantine.Lstat(receipt.Name); !os.IsNotExist(err) {
		return receipt, fmt.Errorf("workspace quarantine destination changed")
	}
	if err := parent.Rename(name, filepath.Join(workspaceQuarantineDirectory, receipt.Name)); err != nil {
		return receipt, err
	}
	if err := checkWorkspaceRootIdentity(quarantine, receipt.Name, *state.Guest); err != nil {
		return receipt, err
	}
	if err := errors.Join(syncWorkspaceRoot(parent), syncWorkspaceRoot(quarantine)); err != nil {
		return receipt, err
	}
	receipt.Phase = "moved"
	if err := writeWorkspaceQuarantineReceipt(quarantine, receipt, true); err != nil {
		return receipt, err
	}
	return receipt, nil
}

func deleteWorkspaceQuarantine(storageRoot string, receipt workspaceQuarantineReceipt, generation string, guard *mutationguard.Guard) error {
	if err := validateWorkspaceQuarantineReceipt(receipt); err != nil {
		return err
	}
	if receipt.Phase != "moved" || generation != receipt.Task.Generation {
		return fmt.Errorf("workspace quarantine deletion requires matching moved receipt")
	}
	parent, rootIdentity, parentIdentity, err := openWorkspaceQuarantineParent(storageRoot, guard)
	if err != nil {
		return err
	}
	defer parent.Close()
	if rootIdentity != receipt.Root || parentIdentity != receipt.Parent {
		return fmt.Errorf("workspace quarantine root identity changed")
	}
	quarantine, err := openWorkspaceQuarantine(parent, false)
	if err != nil {
		return err
	}
	defer quarantine.Close()
	if err := checkWorkspaceRootIdentity(parent, workspaceQuarantineDirectory, receipt.Quarantine); err != nil {
		return err
	}
	if err := checkWorkspaceRootIdentity(quarantine, ".", receipt.Quarantine); err != nil {
		return err
	}
	recorded, err := readWorkspaceQuarantineReceipt(quarantine, receipt.Task.Generation+".json")
	if err != nil {
		return err
	}
	expected, _ := json.Marshal(receipt)
	actual, _ := json.Marshal(recorded)
	if !bytes.Equal(expected, actual) {
		return fmt.Errorf("workspace quarantine receipt changed")
	}
	if err := checkWorkspaceRootIdentity(quarantine, receipt.Name, *receipt.Task.Guest); err != nil {
		return err
	}
	guest, err := quarantine.OpenRoot(receipt.Name)
	if err != nil {
		return err
	}
	defer guest.Close()
	if err := checkWorkspaceRootIdentity(guest, ".", *receipt.Task.Guest); err != nil {
		return err
	}
	directory, err := guest.Open(".")
	if err != nil {
		return err
	}
	entries, readErr := directory.ReadDir(-1)
	closeErr := directory.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return err
	}
	for _, entry := range entries {
		if err := checkWorkspaceRootIdentity(parent, workspaceQuarantineDirectory, receipt.Quarantine); err != nil {
			return err
		}
		if err := checkWorkspaceQuarantineParent(storageRoot, parentIdentity, guard); err != nil {
			return err
		}
		if err := guest.RemoveAll(entry.Name()); err != nil {
			return err
		}
	}
	if err := syncWorkspaceRoot(guest); err != nil {
		return err
	}
	if err := checkWorkspaceQuarantineParent(storageRoot, parentIdentity, guard); err != nil {
		return err
	}
	if err := checkWorkspaceRootIdentity(quarantine, receipt.Name, *receipt.Task.Guest); err != nil {
		return err
	}
	if err := quarantine.Remove(receipt.Name); err != nil {
		return err
	}
	if err := syncWorkspaceRoot(quarantine); err != nil {
		return err
	}
	receipt.Phase = "deleted"
	return writeWorkspaceQuarantineReceipt(quarantine, receipt, true)
}

func openWorkspaceQuarantineParent(storageRoot string, guard *mutationguard.Guard) (*os.Root, taskGuestIdentity, taskGuestIdentity, error) {
	var zero taskGuestIdentity
	canonical, err := filepath.EvalSymlinks(storageRoot)
	if err != nil {
		return nil, zero, zero, err
	}
	if !filepath.IsAbs(storageRoot) || filepath.Clean(storageRoot) != storageRoot || canonical != storageRoot {
		return nil, zero, zero, fmt.Errorf("workspace storage root must be canonical")
	}
	if err := guard.Check(storageRoot); err != nil {
		return nil, zero, zero, err
	}
	identity, err := identifyTaskGuest(storageRoot)
	if err != nil {
		return nil, zero, zero, err
	}
	root, err := os.OpenRoot(storageRoot)
	if err != nil {
		return nil, zero, zero, err
	}
	defer root.Close()
	if err := checkWorkspaceRootIdentity(root, ".", identity); err != nil {
		return nil, zero, zero, err
	}
	info, err := root.Lstat("vms")
	if err != nil {
		return nil, zero, zero, err
	}
	parentIdentity, err := workspaceIdentityFromInfo(filepath.Join(storageRoot, "vms"), info)
	if err != nil {
		return nil, zero, zero, err
	}
	parent, err := root.OpenRoot("vms")
	if err != nil {
		return nil, zero, zero, err
	}
	if err := checkWorkspaceRootIdentity(parent, ".", parentIdentity); err != nil {
		parent.Close()
		return nil, zero, zero, err
	}
	if err := guard.Check(storageRoot); err != nil {
		parent.Close()
		return nil, zero, zero, err
	}
	return parent, identity, parentIdentity, nil
}

func workspaceIdentityFromInfo(path string, info os.FileInfo) (taskGuestIdentity, error) {
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return taskGuestIdentity{}, fmt.Errorf("workspace directory is not a real directory")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return taskGuestIdentity{}, fmt.Errorf("workspace directory identity unavailable")
	}
	return taskGuestIdentity{Path: path, Device: uint64(stat.Dev), Inode: uint64(stat.Ino)}, nil
}
func checkWorkspaceRootIdentity(root *os.Root, name string, expected taskGuestIdentity) error {
	info, err := root.Lstat(name)
	if err != nil {
		return err
	}
	identity, err := workspaceIdentityFromInfo(expected.Path, info)
	if err != nil {
		return err
	}
	if identity != expected {
		return fmt.Errorf("workspace directory identity changed")
	}
	return nil
}
func syncWorkspaceRoot(root *os.Root) error {
	file, err := root.Open(".")
	if err != nil {
		return err
	}
	return errors.Join(file.Sync(), file.Close())
}
func openWorkspaceQuarantine(parent *os.Root, create bool) (*os.Root, error) {
	if create {
		if err := parent.Mkdir(workspaceQuarantineDirectory, 0700); err != nil && !os.IsExist(err) {
			return nil, err
		}
	}
	info, err := parent.Lstat(workspaceQuarantineDirectory)
	if err != nil {
		return nil, err
	}
	identity, err := workspaceIdentityFromInfo("", info)
	if err != nil {
		return nil, err
	}
	root, err := parent.OpenRoot(workspaceQuarantineDirectory)
	if err != nil {
		return nil, err
	}
	if err := checkWorkspaceRootIdentity(root, ".", identity); err != nil {
		root.Close()
		return nil, err
	}
	return root, nil
}
func validateWorkspaceQuarantineReceipt(receipt workspaceQuarantineReceipt) error {
	if receipt.Version != 1 || receipt.Name != receipt.Task.Generation+".covevm" || (receipt.Phase != "prepared" && receipt.Phase != "moved" && receipt.Phase != "deleted") {
		return fmt.Errorf("invalid workspace quarantine receipt")
	}
	if err := validateTaskDisposition(receipt.Task, true); err != nil {
		return err
	}
	if receipt.Task.State != "stopping" || !workspaceDiscardAuthorized(receipt.Task) || !validTaskGuestIdentity(&receipt.Root) || !validTaskGuestIdentity(&receipt.Parent) || !validTaskGuestIdentity(&receipt.Quarantine) || receipt.Quarantine.Path != filepath.Join(receipt.Parent.Path, workspaceQuarantineDirectory) || receipt.Parent.Path != filepath.Join(receipt.Root.Path, "vms") || filepath.Dir(receipt.Task.Guest.Path) != receipt.Parent.Path {
		return fmt.Errorf("invalid workspace quarantine ownership")
	}
	return nil
}
func writeWorkspaceQuarantineReceipt(root *os.Root, receipt workspaceQuarantineReceipt, replace bool) error {
	if err := validateWorkspaceQuarantineReceipt(receipt); err != nil {
		return err
	}
	data, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	if len(data) > workspaceQuarantineReceiptLimit {
		return fmt.Errorf("workspace quarantine receipt exceeds bounds")
	}
	name := receipt.Task.Generation + ".json"
	temporary := name + ".pending"
	target := name
	if replace {
		target = temporary
	}
	file, err := root.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(append(data, '\n'))
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	if replace {
		if err := root.Rename(temporary, name); err != nil {
			return err
		}
	}
	return syncWorkspaceRoot(root)
}
func readWorkspaceQuarantineReceipt(root *os.Root, name string) (workspaceQuarantineReceipt, error) {
	var receipt workspaceQuarantineReceipt
	info, err := root.Lstat(name)
	if err != nil {
		return receipt, err
	}
	if !info.Mode().IsRegular() || info.Size() > workspaceQuarantineReceiptLimit {
		return receipt, fmt.Errorf("workspace quarantine receipt is not a bounded regular file")
	}
	file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return receipt, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return receipt, err
	}
	if !os.SameFile(info, opened) {
		return receipt, fmt.Errorf("workspace quarantine receipt changed")
	}
	data, err := io.ReadAll(io.LimitReader(file, workspaceQuarantineReceiptLimit+1))
	if err != nil {
		return receipt, err
	}
	if len(data) > workspaceQuarantineReceiptLimit {
		return receipt, fmt.Errorf("workspace quarantine receipt exceeds bounds")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err != nil {
		return receipt, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return receipt, fmt.Errorf("workspace quarantine receipt has trailing data")
	}
	return receipt, validateWorkspaceQuarantineReceipt(receipt)
}

func discoverWorkspaceQuarantines(storageRoot string) ([]workspaceQuarantineReceipt, error) {
	parent, err := os.OpenRoot(filepath.Join(storageRoot, "vms"))
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	root, err := openWorkspaceQuarantine(parent, false)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	entries, readErr := dir.ReadDir(-1)
	closeErr := dir.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, err
	}
	var receipts []workspaceQuarantineReceipt
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".json") {
			receipt, err := readWorkspaceQuarantineReceipt(root, entry.Name())
			if err != nil {
				return receipts, fmt.Errorf("read quarantine receipt %s: %w", entry.Name(), err)
			}
			if entry.Name() != receipt.Task.Generation+".json" {
				return receipts, fmt.Errorf("workspace quarantine receipt name differs")
			}
			receipts = append(receipts, receipt)
		}
	}
	return receipts, nil
}

func checkWorkspaceQuarantineParent(storageRoot string, expected taskGuestIdentity, guard *mutationguard.Guard) error {
	if err := guard.Check(storageRoot); err != nil {
		return err
	}
	canonical, err := filepath.EvalSymlinks(storageRoot)
	if err != nil {
		return err
	}
	if canonical != storageRoot {
		return fmt.Errorf("workspace root changed")
	}
	current, err := identifyTaskGuest(expected.Path)
	if err != nil {
		return err
	}
	if current != expected {
		return fmt.Errorf("workspace vm parent changed")
	}
	return nil
}
