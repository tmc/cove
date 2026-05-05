package softreset

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

var defaultMemoryRoots = []string{"/tmp", "/var/tmp", "/dev/shm"}

type MemoryProbe struct {
	Roots []string
	Reset ResetFunc
}

func (p MemoryProbe) Run(ctx context.Context) (Result, error) {
	roots := p.Roots
	if len(roots) == 0 {
		roots = defaultMemoryRoots
	}
	reset := p.Reset
	if reset == nil {
		reset = removeMemoryMarkers
	}

	var markers []string
	evidence := make([]string, 0, len(roots)*2)
	for _, root := range roots {
		if root == "" {
			continue
		}
		if err := os.MkdirAll(root, 0700); err != nil {
			if os.IsPermission(err) {
				evidence = append(evidence, fmt.Sprintf("%s=limit:%v", root, err))
				continue
			}
			return Result{}, fmt.Errorf("create %s: %w", root, err)
		}
		path := filepath.Join(root, "cove-softreset-memory-marker")
		if err := os.WriteFile(path, []byte("softreset memory probe\n"), 0600); err != nil {
			if os.IsPermission(err) {
				evidence = append(evidence, fmt.Sprintf("%s=limit:%v", root, err))
				continue
			}
			return Result{}, fmt.Errorf("write marker %s: %w", path, err)
		}
		markers = append(markers, path)
		evidence = append(evidence, fmt.Sprintf("%s=armed", root))
	}
	if len(markers) == 0 {
		return Result{Probe: "memory", Status: StatusLimit, Evidence: append(evidence, "markers=not-armed")}, nil
	}

	if err := reset(ctx, ""); err != nil {
		return Result{}, fmt.Errorf("reset: %w", err)
	}

	var survivors int
	for _, path := range markers {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			evidence = append(evidence, fmt.Sprintf("%s=absent-after-reset", path))
			continue
		} else if err != nil {
			return Result{}, fmt.Errorf("stat marker %s: %w", path, err)
		}
		survivors++
		evidence = append(evidence, fmt.Sprintf("%s=present-after-reset", path))
	}
	if survivors > 0 {
		return Result{Probe: "memory", Status: StatusFail, Evidence: append(evidence, fmt.Sprintf("survivors=%d", survivors))}, nil
	}
	return Result{Probe: "memory", Status: StatusPass, Evidence: append(evidence, "markers=absent-after-reset")}, nil
}

func removeMemoryMarkers(_ context.Context, _ string) error {
	for _, root := range defaultMemoryRoots {
		if err := os.Remove(filepath.Join(root, "cove-softreset-memory-marker")); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
