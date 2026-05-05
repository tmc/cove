// image_fork.go — runtime path for `cove run -fork-from <image-ref>`.
//
// When -fork-from points at a local image (not an existing VM name),
// we materialize a fresh VM bundle from the image (clonefile disk +
// copy aux/hw.model + fresh machine.id) and boot it. With -ephemeral,
// the materialized child is removed on stop via the existing
// fork_ephemeral.go cleanup machinery (it carries an .ephemeral
// sentinel for the same reason).
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tmc/vz-macos/internal/vmconfig"
)

// isImageForkFromRef returns true when ref does NOT match an existing
// VM name and DOES match a local image. This intentionally favors the
// VM-name path on collision so existing `-fork-from <vm>` invocations
// keep their RAM-overlay semantics.
func isImageForkFromRef(ref string) bool {
	if ref == "" {
		return false
	}
	if vmconfig.Validate(vmconfig.Path(ref)) {
		return false
	}
	parsed, err := ParseImageRef(ref)
	if err != nil {
		return false
	}
	return ImageExists(parsed)
}

// runImageForkFromWithConfig handles the design 024 fork-from-image
// path: materialize a fresh bundle, take the run.lock, boot, and
// (when -ephemeral) destroy on stop.
func runImageForkFromWithConfig(cfg RunConfig, originalVMName, originalVMDir string) error {
	ref, err := ParseImageRef(cfg.EphemeralForkParent)
	if err != nil {
		return fmt.Errorf("cove run -fork-from <image>: %w", err)
	}
	if !ImageExists(ref) {
		return fmt.Errorf("cove run -fork-from <image>: image %s not found", ref)
	}

	verification := VerifyImage(ref, imageVerifyOptions{})
	if verification.Verdict == imageVerifyFail && os.Getenv("COVE_ALLOW_STALE_IMAGE") != "1" {
		return fmt.Errorf("cove run -fork-from <image>: image %s failed verify; run cove image verify %s for details", ref, ref)
	}
	if verification.Verdict == imageVerifyWarn {
		fmt.Fprintf(os.Stderr, "warning: image %s verification returned WARN; continuing\n", ref)
	}
	manifest := verification.Manifest
	if manifest == nil {
		manifest, err = LoadImageManifest(ref)
		if err != nil {
			return fmt.Errorf("cove run -fork-from <image>: %w", err)
		}
	}
	cfg = runConfigForImageManifest(cfg, manifest)

	forkStarted := time.Now()
	childPath, err := MaterializeImage(MaterializeImageOptions{
		Ref:       ref,
		ChildName: cfg.EphemeralForkName,
		Ephemeral: cfg.Ephemeral,
	})
	if err != nil {
		return err
	}
	// MaterializeImage already wrote the bundle under BaseDir; its
	// directory basename is the child name.
	childName := filepath.Base(filepath.Clean(childPath))

	fmt.Printf("Image fork: %s\n", ref)
	fmt.Printf("  child:  %s\n", childName)
	fmt.Printf("  path:   %s\n", childPath)
	if cfg.Ephemeral {
		fmt.Printf("  mode:   ephemeral (destroyed on stop)\n")
	}
	emitMetricEvent("fork_created", forkStarted, "ok", map[string]any{
		"child_name": childName,
		"child_path": childPath,
	})

	vmName = childName
	vmDir = childPath
	defer func() {
		vmName = originalVMName
		vmDir = originalVMDir
	}()

	lock, err := acquireRunLockHook(vmDir)
	if err != nil {
		os.RemoveAll(childPath)
		return fmt.Errorf("cove run -fork-from <image>: %w", err)
	}
	defer func() {
		if releaseErr := lock.Release(); releaseErr != nil {
			fmt.Fprintf(os.Stderr, "warning: release run.lock: %v\n", releaseErr)
		}
	}()

	var runErr error
	if cfg.Linux {
		runErr = runLinuxVMHook()
	} else {
		runErr = runMacOSVMHook()
	}

	if !cfg.Ephemeral {
		return runErr
	}
	if cfg.EphemeralForkKeep {
		fmt.Printf("Ephemeral image fork retained: %s\n", childName)
		return runErr
	}
	if cleanupErr := cleanupEphemeralForkHook(childPath); cleanupErr != nil {
		// The cleanup helper refuses any path missing the .ephemeral
		// sentinel, which we wrote during MaterializeImage. Still log
		// any error so the operator notices left-over state.
		fmt.Fprintf(os.Stderr, "warning: cleanup ephemeral image fork: %v\n", cleanupErr)
	} else {
		fmt.Printf("Ephemeral image fork removed: %s\n", childName)
	}
	return runErr
}

func runConfigForImageManifest(cfg RunConfig, manifest *ImageManifest) RunConfig {
	if manifest == nil {
		return cfg
	}
	switch manifest.OSType {
	case "Linux":
		cfg.Linux = true
		cfg.Windows = false
	case "Windows":
		cfg.Windows = true
		cfg.Linux = false
	}
	return cfg
}
