package main

import (
	"fmt"
	"os"
)

func validateScratch(efi, disk, state, macos string) error {
	if macos != "" && (disk != "" || state != "") {
		return fmt.Errorf("disk and state require a generic EFI guest")
	}
	if state != "" && disk == "" && efi == "" {
		return fmt.Errorf("state requires a scratch efi image or disk")
	}
	if disk == "" {
		return nil
	}
	d, err := os.Stat(disk)
	if err != nil {
		return fmt.Errorf("stat scratch target: %w", err)
	}
	if !d.Mode().IsRegular() || d.Size() == 0 {
		return fmt.Errorf("scratch target must be a nonempty regular file")
	}
	if efi != "" {
		e, err := os.Stat(efi)
		if err != nil {
			return fmt.Errorf("stat scratch EFI image: %w", err)
		}
		if os.SameFile(d, e) {
			return fmt.Errorf("scratch target and EFI image must be different files")
		}
	}
	return nil
}
