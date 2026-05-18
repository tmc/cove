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
	"strings"
	"time"

	"github.com/tmc/vz-macos/internal/vmconfig"
)

// isImageForkFromRef returns true when ref does NOT match an existing
// VM name and either matches a local image or looks like a tagged image
// ref. This intentionally favors the VM-name path on collision so
// existing `-fork-from <vm>` invocations keep their RAM-overlay
// semantics.
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
	return ImageExists(parsed) || strings.Contains(ref, ":")
}

func validateImageForkFromBeforeBundle(refText string) error {
	if !strings.Contains(refText, ":") && !isImageForkFromRef(refText) {
		return nil
	}
	ref, err := ParseImageRef(refText)
	if err != nil {
		return fmt.Errorf("cove run -fork-from <image>: %w", err)
	}
	if !ImageExists(ref) {
		return missingForkFromImageError(ref)
	}
	return nil
}

func missingForkFromImageError(ref ImageRef) error {
	return fmt.Errorf("cove run -fork-from <image>: image %s not found; run 'cove image list' or 'cove image search %s' to find local images, or 'cove image verify %s' for manifest details", ref, ref.Name, ref)
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
		return missingForkFromImageError(ref)
	}

	verification := VerifyImage(ref, imageVerifyOptions{})
	if verification.Verdict == imageVerifyFail && os.Getenv("COVE_ALLOW_STALE_IMAGE") != "1" {
		return fmt.Errorf("cove run -fork-from <image>: image %s failed verify; run cove image verify %s for details", ref, ref)
	}
	if verification.Verdict == imageVerifyWarn {
		fmt.Fprintf(os.Stderr, "note: image %s has non-fatal verification warnings; booting anyway (run 'cove image verify %s' for details)\n", ref, ref)
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
	childName := vmconfig.NameForPath(childPath)

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
	restoreSerial := maybeQuietImageForkSerial(cfg)
	defer restoreSerial()

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

func maybeQuietImageForkSerial(cfg RunConfig) func() {
	if !cfg.Linux || !headlessMode || flagWasSet("serial") || serialOutput != "stdout" {
		return func() {}
	}
	serialOutput = "none"
	fmt.Fprintln(os.Stderr, "note: suppressing Linux serial console for headless image fork; use -serial stdout to show boot logs")
	return func() {
		serialOutput = "stdout"
	}
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
