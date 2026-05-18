//go:build darwin

package vmconfig

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

const finderInfoXattr = "com.apple.FinderInfo"

func markFinderPackage(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return nil
	}
	var data [32]byte
	if got, err := unix.Getxattr(path, finderInfoXattr, data[:]); err == nil && got == len(data) {
		// FinderInfo file flags are stored big-endian at bytes 8:10.
		data[8] |= 0x20
	} else {
		data[8] = 0x20
	}
	if err := unix.Setxattr(path, finderInfoXattr, data[:], 0); err != nil {
		return fmt.Errorf("set Finder package bit: %w", err)
	}
	return nil
}
