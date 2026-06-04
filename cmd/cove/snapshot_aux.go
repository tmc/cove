package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func auxSnapshotPath(vmDir, name string) string {
	return filepath.Join(vmDir, "snapshots", name+".aux")
}

func captureAuxSidecar(vmDir, name string) error {
	src := filepath.Join(vmDir, "aux.img")
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("aux sidecar: read aux.img: %w", err)
	}
	dst := auxSnapshotPath(vmDir, name)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("aux sidecar: create snapshots dir: %w", err)
	}
	if err := copyFile(src, dst); err != nil {
		return fmt.Errorf("aux sidecar: copy aux.img: %w", err)
	}
	return nil
}

func removeAuxSidecar(vmDir, name string) error {
	path := auxSnapshotPath(vmDir, name)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("aux sidecar: remove %s: %w", path, err)
	}
	return nil
}
