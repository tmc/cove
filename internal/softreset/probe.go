package softreset

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Status string

const (
	StatusPass  Status = "pass"
	StatusFail  Status = "fail"
	StatusLimit Status = "limit"
)

type Result struct {
	Probe    string
	Status   Status
	Evidence []string
}

type ResetFunc func(context.Context, string) error

func RemoveAndRecreate(_ context.Context, root string) error {
	if err := os.RemoveAll(root); err != nil {
		return fmt.Errorf("remove root: %w", err)
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return fmt.Errorf("recreate root: %w", err)
	}
	return nil
}

func checkScratchRoot(root string) error {
	root = filepath.Clean(root)
	if root == "." || root == string(filepath.Separator) {
		return fmt.Errorf("scratch root %q is not safe", root)
	}
	base := filepath.Base(root)
	if !strings.Contains(base, "softreset") && !strings.Contains(base, "soft-reset") {
		return fmt.Errorf("scratch root %q must include softreset in its base name", root)
	}
	return nil
}
