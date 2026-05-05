package softreset

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

type FilesystemAttributeProbe struct {
	Root  string
	Reset ResetFunc
}

func (p FilesystemAttributeProbe) Run(ctx context.Context) (Result, error) {
	if err := checkScratchRoot(p.Root); err != nil {
		return Result{}, err
	}
	reset := p.Reset
	if reset == nil {
		reset = RemoveAndRecreate
	}
	if err := os.MkdirAll(p.Root, 0700); err != nil {
		return Result{}, fmt.Errorf("create root: %w", err)
	}

	path := filepath.Join(p.Root, "attribute-residue")
	if err := os.WriteFile(path, []byte("softreset filesystem attribute probe\n"), 0600); err != nil {
		return Result{}, fmt.Errorf("write sentinel: %w", err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		return Result{}, fmt.Errorf("chmod sentinel: %w", err)
	}
	old := time.Unix(946684800, 0)
	if err := os.Chtimes(path, old, old); err != nil {
		return Result{}, fmt.Errorf("chtime sentinel: %w", err)
	}
	xattrState := setProbeXattr(path)

	if err := reset(ctx, p.Root); err != nil {
		return Result{}, fmt.Errorf("reset: %w", err)
	}

	evidence := []string{
		"sentinel=attribute-residue",
		"mode=0600",
		"mtime=2000-01-01T00:00:00Z",
		"xattr=" + xattrState,
	}
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return Result{Probe: "filesystem-attributes", Status: StatusPass, Evidence: append(evidence, "sentinel=absent-after-reset")}, nil
	}
	if err != nil {
		return Result{}, fmt.Errorf("stat sentinel after reset: %w", err)
	}
	evidence = append(evidence, "sentinel=present-after-reset")
	if info.Mode().Perm() == 0600 {
		evidence = append(evidence, "mode-residue=present")
	}
	if info.ModTime().Equal(old) {
		evidence = append(evidence, "mtime-residue=present")
	}
	if hasProbeXattr(path) {
		evidence = append(evidence, "xattr-residue=present")
	}
	return Result{Probe: "filesystem-attributes", Status: StatusFail, Evidence: evidence}, nil
}

func setProbeXattr(path string) string {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return "unsupported"
	}
	if err := setXattr(path, "user.cove.softreset", []byte("residue")); err != nil {
		return "limit:" + err.Error()
	}
	return "set"
}

func hasProbeXattr(path string) bool {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return false
	}
	return hasXattr(path, "user.cove.softreset")
}
