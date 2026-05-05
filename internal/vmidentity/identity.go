// Package vmidentity reads and writes the host-side identity files that
// Virtualization.framework binds into saved macOS VM state.
package vmidentity

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Identity is the host-side identity tuple needed for future vmstate restore.
type Identity struct {
	MachineID []byte
	AuxPath   string
	MAC       string
	DiskPath  string
}

// Read reads identity metadata from bundle. diskPath records the disk image
// that future restore wiring must pair with the identity files.
func Read(bundle, diskPath string) (*Identity, error) {
	machineID, err := os.ReadFile(filepath.Join(bundle, "machine.id"))
	if err != nil {
		return nil, fmt.Errorf("read machine.id: %w", err)
	}
	if len(machineID) == 0 {
		return nil, fmt.Errorf("read machine.id: empty file")
	}
	auxPath := filepath.Join(bundle, "aux.img")
	if st, err := os.Stat(auxPath); err != nil {
		return nil, fmt.Errorf("stat aux.img: %w", err)
	} else if st.Size() == 0 {
		return nil, fmt.Errorf("stat aux.img: empty file")
	}
	if strings.TrimSpace(diskPath) == "" {
		return nil, fmt.Errorf("read disk identity: empty disk path")
	}
	if _, err := os.Stat(diskPath); err != nil {
		return nil, fmt.Errorf("stat disk: %w", err)
	}
	var mac string
	if data, err := os.ReadFile(filepath.Join(bundle, "mac.address")); err == nil {
		mac = strings.TrimSpace(string(data))
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read mac.address: %w", err)
	}
	return &Identity{
		MachineID: append([]byte(nil), machineID...),
		AuxPath:   auxPath,
		MAC:       mac,
		DiskPath:  diskPath,
	}, nil
}

// Write writes identity metadata into bundle and verifies the durable fields
// that must survive before a forked VM is booted.
func Write(bundle string, id *Identity) error {
	if id == nil {
		return fmt.Errorf("write identity: nil identity")
	}
	if len(id.MachineID) == 0 {
		return fmt.Errorf("write machine.id: empty identity")
	}
	if strings.TrimSpace(id.AuxPath) == "" {
		return fmt.Errorf("write aux.img: empty source path")
	}
	if err := os.MkdirAll(bundle, 0755); err != nil {
		return fmt.Errorf("create bundle: %w", err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "machine.id"), id.MachineID, 0600); err != nil {
		return fmt.Errorf("write machine.id: %w", err)
	}
	if err := copyFile(id.AuxPath, filepath.Join(bundle, "aux.img")); err != nil {
		return fmt.Errorf("copy aux.img: %w", err)
	}
	macPath := filepath.Join(bundle, "mac.address")
	if strings.TrimSpace(id.MAC) != "" {
		if err := os.WriteFile(macPath, []byte(strings.TrimSpace(id.MAC)+"\n"), 0644); err != nil {
			return fmt.Errorf("write mac.address: %w", err)
		}
	} else if err := os.Remove(macPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove mac.address: %w", err)
	}
	written, err := Read(bundle, id.DiskPath)
	if err != nil {
		return fmt.Errorf("verify identity: %w", err)
	}
	if !Equal(id, written) {
		return fmt.Errorf("verify identity: destination differs from source")
	}
	return nil
}

// Equal reports whether a and b describe the same identity values.
func Equal(a, b *Identity) bool {
	if a == nil || b == nil {
		return a == b
	}
	aAux, aErr := os.ReadFile(a.AuxPath)
	bAux, bErr := os.ReadFile(b.AuxPath)
	if aErr != nil || bErr != nil || !bytes.Equal(aAux, bAux) {
		return false
	}
	return bytes.Equal(a.MachineID, b.MachineID) &&
		strings.TrimSpace(a.MAC) == strings.TrimSpace(b.MAC) &&
		filepath.Clean(a.DiskPath) == filepath.Clean(b.DiskPath)
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}
