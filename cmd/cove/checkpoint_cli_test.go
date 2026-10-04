package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/cove/internal/checkpoint"
	"github.com/tmc/cove/internal/vmconfig"
)

func checkpointFixture(t *testing.T, platform string) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{"config.json": "{}"}
	if platform == "macOS" {
		files["disk.img"] = "disk"
		files["aux.img"] = "aux"
		files["hw.model"] = "hardware"
		files["machine.id"] = "machine"
	} else {
		files["linux-disk.img"] = "disk"
		files["linux-machine.id"] = "machine"
		files["efi.nvram"] = "firmware"
	}
	for name, value := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestCheckpointInventory(t *testing.T) {
	for _, platform := range []string{"macOS", "Linux"} {
		t.Run(platform, func(t *testing.T) {
			root := checkpointFixture(t, platform)
			if err := os.WriteFile(filepath.Join(root, "data.img"), []byte("data"), 0600); err != nil {
				t.Fatal(err)
			}
			inventory, err := coldCheckpointInventory(root, []string{"data.img"})
			if err != nil {
				t.Fatal(err)
			}
			if inventory.Platform != platform || len(inventory.Compatibility) != 71 {
				t.Fatalf("inventory: %+v", inventory)
			}
			roles := map[string]string{}
			for _, file := range inventory.Files {
				roles[file.Path] = file.Role
			}
			if roles["data.img"] != "disk" || roles["config.json"] != "config" {
				t.Fatal(roles)
			}
			if platform == "macOS" && roles["aux.img"] != "firmware" {
				t.Fatal(roles)
			}
			if platform == "Linux" && roles["efi.nvram"] != "firmware" {
				t.Fatal(roles)
			}
			old := inventory.Compatibility
			identity := "machine.id"
			if platform == "Linux" {
				identity = "linux-machine.id"
			}
			if err := os.WriteFile(filepath.Join(root, identity), []byte("different"), 0600); err != nil {
				t.Fatal(err)
			}
			inventory, err = coldCheckpointInventory(root, nil)
			if err != nil || inventory.Compatibility == old {
				t.Fatalf("identity change: %+v %v", inventory, err)
			}
		})
	}
}

func TestCheckpointInventoryRefusals(t *testing.T) {
	for _, name := range []string{"suspend.vmstate", vmFrameworkConfigFileName, "duplicate", "outside", "symlink", "no-efi"} {
		t.Run(name, func(t *testing.T) {
			root := checkpointFixture(t, "Linux")
			var disks []string
			switch name {
			case "duplicate":
				disks = []string{"linux-disk.img"}
			case "outside":
				disks = []string{"../disk.img"}
			case "symlink":
				if err := os.Symlink("linux-disk.img", filepath.Join(root, "other.img")); err != nil {
					t.Fatal(err)
				}
				disks = []string{"other.img"}
			case "no-efi":
				if err := os.Remove(filepath.Join(root, "efi.nvram")); err != nil {
					t.Fatal(err)
				}
			default:
				if err := os.WriteFile(filepath.Join(root, name), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := coldCheckpointInventory(root, disks); err == nil {
				t.Fatal("unsupported inventory accepted")
			}
			if _, err := os.Stat(filepath.Join(root, "checkpoints")); !os.IsNotExist(err) {
				t.Fatal("inventory created checkpoint storage")
			}
		})
	}
}

func TestCheckpointFilesClosedChecksEveryTarget(t *testing.T) {
	var visited []string
	check := func(path string) ([]int, error) {
		visited = append(visited, filepath.Base(path))
		if filepath.Base(path) == "aux.img" {
			return []int{42}, nil
		}
		return nil, nil
	}
	err := checkpointFilesClosed("/vm", []string{"disk.img", "disk.img", "data.img", "aux.img"}, check)
	if err == nil || !strings.Contains(err.Error(), "42") || strings.Join(visited, ",") != "disk.img,data.img,aux.img" {
		t.Fatalf("visited=%v error=%v", visited, err)
	}
	err = checkpointFilesClosed("/vm", []string{"disk.img"}, func(string) ([]int, error) { return nil, errors.New("probe failed") })
	if err == nil || !strings.Contains(err.Error(), "probe failed") {
		t.Fatal(err)
	}
}

func TestCheckpointPendingRuntimeAndReaders(t *testing.T) {
	root := checkpointFixture(t, "Linux")
	if err := os.Mkdir(filepath.Join(root, ".checkpoint-restore"), 0700); err != nil {
		t.Fatal(err)
	}
	if lock, err := AcquireRunLock(root); err == nil {
		lock.Release()
		t.Fatal("pending restore allowed runtime")
	}
	lock, err := acquireRunLock(root, true)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	if _, err := checkpoint.AcquireRead(root); err == nil {
		t.Fatal("pending restore allowed reader")
	}
	if err := ensurePushSourceInactive(root); err == nil {
		t.Fatal("pending restore allowed push plan")
	}
}

func TestCheckpointCloneUnderExistingRunLock(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	src := vmconfig.Path("checkpoint-parent")
	if err := os.MkdirAll(src, 0700); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"linux-disk.img": "disk", "linux-machine.id": "machine", "config.json": "{}"} {
		if err := os.WriteFile(filepath.Join(src, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	lock, err := AcquireRunLock(src)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	if err := CloneVM(CloneOptions{Source: "checkpoint-parent", Target: "checkpoint-child", CopyMachineID: true}); err != nil {
		t.Fatalf("fork-style caller holding run.lock: %v", err)
	}
}

func TestCheckpointIdentityBounded(t *testing.T) {
	for _, name := range []string{"empty", "oversize", "symlink"} {
		t.Run(name, func(t *testing.T) {
			root := checkpointFixture(t, "macOS")
			path := filepath.Join(root, "hw.model")
			switch name {
			case "empty":
				if err := os.WriteFile(path, nil, 0600); err != nil {
					t.Fatal(err)
				}
			case "oversize":
				f, err := os.OpenFile(path, os.O_WRONLY, 0600)
				if err != nil {
					t.Fatal(err)
				}
				err = f.Truncate((1 << 20) + 1)
				f.Close()
				if err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("machine.id", path); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := readCheckpointIdentity(root, "hw.model"); err == nil {
				t.Fatal("invalid identity accepted")
			}
			if _, err := coldCheckpointInventory(root, nil); err == nil {
				t.Fatal("invalid inventory accepted")
			}
		})
	}
}
