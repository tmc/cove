package vmconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/tmc/cove/internal/mutationguard"
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
	if _, err := os.Stat(diskPath); os.IsNotExist(err) {
		diskPath = WindowsDiskPath(dir)
	}
	diskInfo, err := os.Stat(diskPath)
	if err != nil {
		return nil, fmt.Errorf("stat VM disk: %w", err)
	}
	vmState := defaultState(dir)
	if state != nil {
		vmState = state(dir)
	}
	return &Info{
		Name:     NameForPath(dir),
		Path:     dir,
		DiskSize: diskInfo.Size(),
		Created:  diskInfo.ModTime(),
		State:    vmState,
		OSType:   DetectOSType(dir),
	}, nil
}

// NameForPath returns the VM name for a regular or .covevm package path.
func NameForPath(dir string) string {
	name := filepath.Base(dir)
	if filepath.Ext(name) == ".covevm" {
		name = name[:len(name)-len(".covevm")]
	}
	return name
}

// List returns all valid VMs in the base directory and any legacy
// `~/.vz/<name>/` VMs reachable via the legacy layout. Discovered legacy
// VMs are aliased into BaseDir on first sight so subsequent lists see them
// through the regular BaseDir scan.
func List(state StateFunc) ([]Info, error) {
	guard, err := mutationguard.Acquire(StateDir())
	if err != nil {
		return nil, err
	}
	defer guard.Release()
	return listVMs(state, true)
}

// ListReadOnly lists registered and legacy VMs without migrating or creating aliases.
func ListReadOnly(state StateFunc) ([]Info, error) {
	return listVMs(state, false)
}

func listVMs(state StateFunc, mutate bool) ([]Info, error) {
	baseDir := BaseDir()
	if mutate {
		if err := os.MkdirAll(baseDir, 0755); err != nil {
			return nil, fmt.Errorf("create base dir: %w", err)
		}
	}
	entries, err := os.ReadDir(baseDir)
	if err != nil && !(os.IsNotExist(err) && !mutate) {
		return nil, fmt.Errorf("read base dir: %w", err)
	}

	seen := make(map[string]bool)
	seenPath := make(map[string]bool)
	var vms []Info
	for _, entry := range entries {
		name := entry.Name()
		vmPath := filepath.Join(baseDir, name)
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
		if !entry.Type().IsRegular() {
			realPath := resolvePath(vmPath)
			if seenPath[realPath] {
				continue
			}
			seenPath[realPath] = true
		}
		if mutate {
			packaged, err := ensurePackageLayoutLocked(info.Name, info.Path)
			if err != nil {
				return nil, err
			}
			info.Path = packaged
		}
		seen[info.Name] = true
		vms = append(vms, *info)
	}

	// Legacy layout: ~/.vz/<name>/ peers of BaseDir. Used by older vz-macos
	// builds before EnsureAlias planted symlinks under BaseDir on every
	// EnsureDir call. List() needs to surface them so `cove list` works on
	// trees that predate the alias migration; once visible we plant the
	// alias so future lists hit the normal BaseDir scan.
	legacyRoot := filepath.Dir(baseDir)
	if legacyEntries, err := os.ReadDir(legacyRoot); err == nil {
		for _, entry := range legacyEntries {
			if !entry.IsDir() {
				continue
			}
			name := entry.Name()
			vmName := NameForPath(name)
			if name == filepath.Base(baseDir) || seen[vmName] {
				continue
			}
			legacyPath := filepath.Join(legacyRoot, name)
			if !Validate(legacyPath) {
				continue
			}
			info, err := InfoFor(legacyPath, state)
			if err != nil {
				continue
			}
			if mutate {
				_ = ensureAliasLocked(name, legacyPath)
			}
			seen[info.Name] = true
			vms = append(vms, *info)
		}
	}

	sort.Slice(vms, func(i, j int) bool {
		return vms[i].Name < vms[j].Name
	})
	if mutate {
		if err := ensurePackageAliasesLocked(vms); err != nil {
			return nil, err
		}
	}
	return vms, nil
}

func defaultState(dir string) string {
	if HasSuspendState(dir) {
		return "suspended"
	}
	return "stopped"
}
