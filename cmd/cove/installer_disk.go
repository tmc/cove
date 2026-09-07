package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/tmc/apple/x/plist"
)

func validateInstalledDisk(ctx context.Context, path string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return checkInstalledDisk(ctx, path, func(ctx context.Context, args ...string) ([]byte, error) {
		out, err := exec.CommandContext(ctx, args[0], args[1:]...).Output()
		if err != nil {
			return out, fmt.Errorf("%s: %w", strings.Join(args, " "), err)
		}
		return out, nil
	})
}

func checkInstalledDisk(ctx context.Context, path string, run func(context.Context, ...string) ([]byte, error)) (err error) {
	attach := []string{"diskutil", "image", "attach", "--readOnly", "--noMount", "--plist", path}
	detach := []string{"diskutil", "eject"}
	if _, err := run(ctx, "diskutil", "image", "attach", "--help"); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		attach = []string{"hdiutil", "attach", "-readonly", "-nomount", "-plist", path}
		detach = []string{"hdiutil", "detach"}
	}
	out, err := run(ctx, attach...)
	if err != nil {
		return fmt.Errorf("attach installed disk read-only: %w", err)
	}
	device, layoutErr := installedDiskLayout(out)
	if device != "" {
		defer func() {
			// Cancellation must not leave an image attached on the next boot path.
			cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if _, detachErr := run(cleanup, append(detach, device)...); detachErr != nil {
				err = errors.Join(err, fmt.Errorf("eject installed disk %s: %w", device, detachErr))
			}
		}()
	}
	return layoutErr
}

var installedDiskDevicePattern = regexp.MustCompile(`^disk[0-9]+$`)

func installedDiskLayout(data []byte) (string, error) {
	var info struct {
		Entities []struct {
			Device string `plist:"dev-entry"`
			Hint   string `plist:"content-hint"`
		} `plist:"system-entities"`
	}
	if _, err := plist.Unmarshal(data, &info); err != nil {
		return "", fmt.Errorf("read installed disk layout: %w", err)
	}
	var device string
	var gpt, apfs bool
	for _, entity := range info.Entities {
		name := strings.TrimPrefix(entity.Device, "/dev/")
		if device == "" && installedDiskDevicePattern.MatchString(name) {
			device = "/dev/" + name
		}
		gpt = gpt || entity.Hint == "GUID_partition_scheme"
		apfs = apfs || entity.Hint == "Apple_APFS" || entity.Hint == "Apple_APFS_Container"
	}
	if device == "" {
		return "", fmt.Errorf("installed disk has no device")
	}
	if !gpt || !apfs {
		return device, fmt.Errorf("installed disk has no readable GPT and APFS partition; preserve the image for diagnosis before reinstalling")
	}
	return device, nil
}
