package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tmc/cove/internal/checkpoint"
	"github.com/tmc/cove/internal/vmconfig"
	"golang.org/x/sys/unix"
)

type checkpointInventory struct {
	Platform      string              `json:"platform"`
	Mode          string              `json:"mode"`
	Compatibility string              `json:"compatibility"`
	Files         []checkpoint.Source `json:"files"`
	Excluded      []string            `json:"excluded"`
}

func coldCheckpointInventory(root string, additional []string) (checkpointInventory, error) {
	inventory := checkpointInventory{Platform: vmconfig.DetectOSType(root), Mode: "cold declared bundle files; additional disks are explicit declarations, not certified runtime inventory", Excluded: []string{"guest memory", "host shared-folder data", "external, raw, USB and hotplug devices", "runtime-only device configuration"}}
	if _, err := vmconfig.Load(root); err != nil {
		return inventory, err
	}
	for _, name := range []string{"suspend.vmstate", vmFrameworkConfigFileName} {
		if _, err := os.Lstat(filepath.Join(root, name)); err == nil {
			return inventory, fmt.Errorf("cold checkpoint refuses %s; use a quiesced guest without saved memory or opaque framework configuration", name)
		} else if !os.IsNotExist(err) {
			return inventory, err
		}
	}
	var identities []string
	switch inventory.Platform {
	case "macOS":
		inventory.Files = []checkpoint.Source{{Path: "disk.img", Role: "disk"}, {Path: "aux.img", Role: "firmware"}, {Path: "hw.model", Role: "identity"}, {Path: "machine.id", Role: "identity"}}
		identities = []string{"hw.model", "machine.id"}
	case "Linux":
		inventory.Files = []checkpoint.Source{{Path: "linux-disk.img", Role: "disk"}, {Path: "linux-machine.id", Role: "identity"}}
		identities = []string{"linux-machine.id"}
		firmware := false
		for _, name := range []string{"efi.nvram", "efi-vars.img"} {
			if _, err := os.Lstat(filepath.Join(root, name)); err == nil {
				inventory.Files = append(inventory.Files, checkpoint.Source{Path: name, Role: "firmware"})
				firmware = true
			} else if !os.IsNotExist(err) {
				return inventory, err
			}
		}
		if !firmware {
			return inventory, fmt.Errorf("cold Linux checkpoint requires a declared EFI store; direct-kernel configuration is not yet supported")
		}
	default:
		return inventory, fmt.Errorf("checkpoint platform %s is not supported", inventory.Platform)
	}
	for _, name := range []string{"config.json", "mac.address", "boot-args.txt", "linux-installed", "vmlinuz", "initrd", linuxRootUUIDFileName, linuxRootDeviceFileName} {
		if _, err := os.Lstat(filepath.Join(root, name)); err == nil {
			inventory.Files = append(inventory.Files, checkpoint.Source{Path: name, Role: "config"})
		} else if !os.IsNotExist(err) {
			return inventory, err
		}
	}
	for _, path := range additional {
		inventory.Files = append(inventory.Files, checkpoint.Source{Path: path, Role: "disk"})
	}
	seen := map[string]bool{}
	for _, file := range inventory.Files {
		if seen[file.Path] {
			return inventory, fmt.Errorf("duplicate checkpoint file %s", file.Path)
		}
		seen[file.Path] = true
		if err := checkpointLocalRegularFile(root, file.Path); err != nil {
			return inventory, err
		}
	}
	sort.Strings(identities)
	var identity strings.Builder
	fmt.Fprintf(&identity, "checkpoint-identity-v1\n%s\n", inventory.Platform)
	for _, name := range identities {
		data, err := readCheckpointIdentity(root, name)
		if err != nil {
			return inventory, err
		}
		if len(data) == 0 {
			return inventory, fmt.Errorf("checkpoint identity %s is empty", name)
		}
		fmt.Fprintf(&identity, "%s %s\n", name, digestBytes(data))
	}
	inventory.Compatibility = digestBytes([]byte(identity.String()))
	return inventory, nil
}

func checkpointLocalRegularFile(root, path string) error {
	if filepath.IsAbs(path) || filepath.Clean(path) != path || path == "." || strings.HasPrefix(path, "../") || strings.Contains(path, "\\") || strings.HasPrefix(path, ".") {
		return fmt.Errorf("checkpoint disk must be a bundle-relative regular file: %s", path)
	}
	current := root
	parts := strings.Split(path, string(filepath.Separator))
	for i, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("checkpoint file path contains a symlink: %s", path)
		}
		if i < len(parts)-1 {
			if !info.IsDir() {
				return fmt.Errorf("checkpoint file parent is not a directory: %s", path)
			}
		} else if !info.Mode().IsRegular() {
			return fmt.Errorf("checkpoint source is not a regular file: %s", path)
		}
	}
	return nil
}

func validateColdCheckpointManifest(inventory checkpointInventory, manifest checkpoint.Manifest) error {
	known := make(map[string]string)
	for _, file := range inventory.Files {
		known[file.Path] = file.Role
	}
	seen := make(map[string]bool)
	for _, file := range manifest.Files {
		seen[file.Path] = true
		switch file.Role {
		case "disk":
			if file.Path == "suspend.vmstate" || file.Path == vmFrameworkConfigFileName {
				return fmt.Errorf("cold checkpoint cannot restore saved memory or opaque framework configuration")
			}
		case "firmware", "identity":
			if known[file.Path] != file.Role {
				return fmt.Errorf("unsupported cold checkpoint %s file %s", file.Role, file.Path)
			}
		case "config":
			allowed := false
			for _, name := range []string{"config.json", "mac.address", "boot-args.txt", "linux-installed", "vmlinuz", "initrd", linuxRootUUIDFileName, linuxRootDeviceFileName} {
				if file.Path == name {
					allowed = true
					break
				}
			}
			if !allowed {
				return fmt.Errorf("unsupported cold checkpoint config file %s", file.Path)
			}
		default:
			return fmt.Errorf("cold checkpoint does not support role %s", file.Role)
		}
	}
	for _, file := range inventory.Files {
		if file.Role != "config" && !seen[file.Path] {
			return fmt.Errorf("cold checkpoint is missing required file %s", file.Path)
		}
	}
	return nil
}

func readCheckpointIdentity(root, name string) ([]byte, error) {
	const limit = 1 << 20
	path := filepath.Join(root, name)
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Size() <= 0 || before.Size() > limit {
		return nil, fmt.Errorf("checkpoint identity %s must be a nonempty regular file at most %d bytes", name, limit)
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(before, opened) {
		return nil, fmt.Errorf("checkpoint identity %s changed while opening", name)
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	after, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if len(data) > limit || int64(len(data)) != before.Size() || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return nil, fmt.Errorf("checkpoint identity %s changed while reading or exceeds size limit", name)
	}
	return data, nil
}
