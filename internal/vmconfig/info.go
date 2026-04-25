package vmconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Info holds information about a virtual machine.
type Info struct {
	Name     string
	Path     string
	DiskSize int64
	Created  time.Time
	State    string
	OSType   string
}

// StateFunc reports the state for a VM directory.
type StateFunc func(dir string) string

// InfoFor returns information about a VM directory.
func InfoFor(dir string, state StateFunc) (*Info, error) {
	if !Validate(dir) {
		return nil, fmt.Errorf("invalid VM: %s", dir)
	}

	diskPath := filepath.Join(dir, "disk.img")
	if _, err := os.Stat(diskPath); os.IsNotExist(err) {
		diskPath = filepath.Join(dir, "linux-disk.img")
	}
	diskInfo, err := os.Stat(diskPath)
	if err != nil {
		return nil, fmt.Errorf("stat disk.img: %w", err)
	}
	vmState := defaultState(dir)
	if state != nil {
		vmState = state(dir)
	}
	return &Info{
		Name:     filepath.Base(dir),
		Path:     dir,
		DiskSize: diskInfo.Size(),
		Created:  diskInfo.ModTime(),
		State:    vmState,
		OSType:   DetectOSType(dir),
	}, nil
}

// List returns all valid VMs in the base directory.
func List(state StateFunc) ([]Info, error) {
	baseDir := BaseDir()
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("create base dir: %w", err)
	}
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return nil, fmt.Errorf("read base dir: %w", err)
	}

	var vms []Info
	for _, entry := range entries {
		vmPath := filepath.Join(baseDir, entry.Name())
		if !entry.IsDir() {
			if entry.Type()&os.ModeSymlink == 0 {
				continue
			}
			target, err := os.Stat(vmPath)
			if err != nil || !target.IsDir() {
				continue
			}
		}
		info, err := InfoFor(vmPath, state)
		if err != nil {
			continue
		}
		vms = append(vms, *info)
	}
	sort.Slice(vms, func(i, j int) bool {
		return vms[i].Name < vms[j].Name
	})
	return vms, nil
}

func defaultState(dir string) string {
	if HasSuspendState(dir) {
		return "suspended"
	}
	return "stopped"
}
