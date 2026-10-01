package main

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func publishAgentCopy(stage, dest string, overwrite bool) error {
	if dest == "" {
		return fmt.Errorf("copy destination required")
	}
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
		return fmt.Errorf("publish copy: %w", err)
	}
	return nil
}
