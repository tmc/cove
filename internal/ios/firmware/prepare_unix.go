//go:build darwin || linux

package firmware

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

func prepareRecipe(ctx context.Context, source, dir, cache, iphone, cloudos string, log io.Writer) error {
	python := os.Getenv("VPHONE_PYTHON")
	if python == "" {
		python = "python3"
	}
	python, err := exec.LookPath(python)
	if err != nil {
		return fmt.Errorf("prepare Python: %w", err)
	}
	cmd := exec.CommandContext(ctx, "/bin/bash", filepath.Join(source, "scripts/fw_prepare.sh"), iphone, cloudos)
	cmd.Dir = dir
	for _, key := range []string{"PATH", "HOME", "TMPDIR", "LANG"} {
		if value, ok := os.LookupEnv(key); ok {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	// Preparation is variant-independent. The less variant's mounted sealing-tool
	// acquisition belongs to setup, which must journal its mounts separately.
	cmd.Env = append(cmd.Env, "VPHONE_PYTHON="+python, "IPSW_DIR="+cache, "VPHONE_KEEP_ARTIFACTS=1", "VARIANT=regular")
	cmd.Stdout, cmd.Stderr = log, log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	return nil
}
