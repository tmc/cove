package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

func publishCopy(stage, dest string, overwrite bool) error {
	flag := uint32(unix.RENAME_EXCL)
	if overwrite {
		info, err := os.Lstat(stage)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return os.Rename(stage, dest)
		}
		if _, err := os.Lstat(dest); err == nil {
			flag = unix.RENAME_SWAP
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	if err := unix.RenamexNp(stage, dest, flag); err != nil {
		return fmt.Errorf("publish %q: %w", dest, err)
	}
	return nil
}

func copyFileToHost(ctx context.Context, dest string, overwrite bool, copy func(string) error) error {
	f, err := os.CreateTemp(filepath.Dir(dest), ".cove-copy-*")
	if err != nil {
		return err
	}
	stage := f.Name()
	if err := f.Close(); err != nil {
		os.Remove(stage)
		return err
	}
	defer os.RemoveAll(stage)
	if err := copy(stage); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return publishCopy(stage, dest, overwrite)
}

func copyDirectoryToHost(ctx context.Context, dest string, overwrite bool, copy func(io.Writer) error) error {
	stage, err := os.MkdirTemp(filepath.Dir(dest), ".cove-copy-*")
	if err != nil {
		return fmt.Errorf("stage host directory: %w", err)
	}
	defer os.RemoveAll(stage)
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		defer pr.Close()
		cmd := exec.CommandContext(ctx, "tar", "xf", "-", "--strip-components=1", "-C", stage)
		cmd.Stdin = pr
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		err := cmd.Run()
		if err != nil {
			err = fmt.Errorf("extract directory: %w: %s", err, strings.TrimSpace(stderr.String()))
		}
		done <- err
	}()
	copyErr := copy(pw)
	pw.CloseWithError(copyErr)
	extractErr := <-done
	if copyErr != nil {
		return fmt.Errorf("stream directory: %w", copyErr)
	}
	if extractErr != nil {
		return extractErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Chmod(stage, 0755); err != nil {
		return err
	}
	return publishCopy(stage, dest, overwrite)
}
