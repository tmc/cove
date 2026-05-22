// fork_ephemeral.go — short-lived siblings via RAM-overlay over parent disk.
//
// Phase 3 of design 013. An ephemeral fork is a child VM that:
//
//   - shares the parent's disk.img read-only (RAM-overlay backs writes;
//     they vanish on shutdown)
//   - has its own vmDir for control.sock, screenshots, logs
//   - copies parent's aux.img + hw.model (immutable identity inputs)
//   - optionally preserves machine.id + MAC for vmstate fidelity
//   - records lineage so cove vm tree / cove gc see it
//
// On normal exit the vmDir is removed. On crash, the .ephemeral sentinel
// stays in place; cove gc discovers it later, verifies the run.lock is
// releasable (no live process), and removes the dir.
//
// Validation #1 (the empirical N-readers-on-one-file question raised by
// design 013:96) returned PASS with no-file-lock — VZ does not lock the
// underlying disk for VZTemporaryRAMStorageDeviceAttachmentWithURLReadOnly.
// Phase 3 uses Model B (RAM-overlay against parent's disk) directly with
// no clonefile fallback path. See validation1_test.go.
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tmc/cove/internal/vmconfig"
	"github.com/tmc/cove/internal/vmidentity"
)

// ephemeralSentinel marks a vmDir as auto-deletable. cove gc removes
// directories that contain this file once their run.lock is releasable.
const ephemeralSentinel = ".ephemeral"

// EphemeralFork describes a live ephemeral child VM.
type EphemeralFork struct {
	Name      string
	Path      string
	Source    string
	CreatedAt time.Time
}

// EphemeralForkOptions configures ephemeral-fork creation.
type EphemeralForkOptions struct {
	// Parent is the source VM name. Must exist and be stopped.
	Parent string
	// Name is the optional explicit ephemeral name. Empty defaults to
	// auto-generated "<parent>-eph-<timestamp>".
	Name string
	// Now is a clock injection point for deterministic names in tests.
	Now func() time.Time
	// PreserveIdentity copies the parent's machine.id and MAC into the
	// child. It is for vmstate-fidelity fork-from runs, not cold forks.
	PreserveIdentity bool
}

// SetupEphemeralFork creates an ephemeral child vmDir without cloning
// the parent's disk. The child's disk is the parent's disk.img attached
// read-only via RAM-overlay at boot time (see runtime wiring in
// runtime_lifecycle.go); only aux.img and hw.model are physically
// copied. Unless PreserveIdentity is set, machine.id and MAC are
// generated fresh by the VM init path on first boot. A `.ephemeral`
// sentinel is dropped so cove gc can sweep orphaned siblings after
// host crashes.
func SetupEphemeralFork(opts EphemeralForkOptions) (EphemeralFork, error) {
	if opts.Parent == "" {
		return EphemeralFork{}, errors.New("ephemeral fork: parent VM name required")
	}
	parentDir := vmconfig.Path(opts.Parent)
	if !vmconfig.Validate(parentDir) {
		return EphemeralFork{}, fmt.Errorf("ephemeral fork: parent VM not found: %s", opts.Parent)
	}
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	createdAt := now()

	name := opts.Name
	if name == "" {
		name = fmt.Sprintf("%s-eph-%s", opts.Parent, createdAt.Format("20060102-150405"))
	}
	if name == opts.Parent {
		return EphemeralFork{}, errors.New("ephemeral fork: child name must differ from parent")
	}
	childDir := vmconfig.Path(name)
	if _, err := os.Stat(childDir); err == nil {
		return EphemeralFork{}, fmt.Errorf("ephemeral fork: vm '%s' already exists", name)
	} else if !os.IsNotExist(err) {
		return EphemeralFork{}, fmt.Errorf("ephemeral fork: stat child dir: %w", err)
	}

	// Pre-flight on the parent's identity files. Required for VZ start.
	for _, f := range []string{"aux.img", "hw.model"} {
		if _, err := os.Stat(filepath.Join(parentDir, f)); err != nil {
			return EphemeralFork{}, fmt.Errorf("ephemeral fork: parent missing %s: %w", f, err)
		}
	}
	var identity *vmidentity.Identity
	if opts.PreserveIdentity {
		var err error
		identity, err = vmidentity.Read(parentDir, vmPrimaryDiskPath(parentDir))
		if err != nil {
			return EphemeralFork{}, fmt.Errorf("ephemeral fork: read parent identity: %w", err)
		}
	}

	if err := os.MkdirAll(childDir, 0o755); err != nil {
		return EphemeralFork{}, fmt.Errorf("ephemeral fork: create child dir: %w", err)
	}

	cleanup := func() {
		os.RemoveAll(childDir)
	}
	if err := vmconfig.EnsureCompatibilityAlias(name, childDir); err != nil {
		cleanup()
		return EphemeralFork{}, fmt.Errorf("ephemeral fork: create compatibility alias: %w", err)
	}

	if err := os.WriteFile(filepath.Join(childDir, ephemeralSentinel), nil, 0o644); err != nil {
		cleanup()
		return EphemeralFork{}, fmt.Errorf("ephemeral fork: write sentinel: %w", err)
	}
	if err := copyFile(filepath.Join(parentDir, "hw.model"), filepath.Join(childDir, "hw.model")); err != nil {
		cleanup()
		return EphemeralFork{}, fmt.Errorf("ephemeral fork: copy hw.model: %w", err)
	}
	if opts.PreserveIdentity {
		if err := vmidentity.Write(childDir, identity); err != nil {
			cleanup()
			return EphemeralFork{}, fmt.Errorf("ephemeral fork: write child identity: %w", err)
		}
	} else if err := copyFile(filepath.Join(parentDir, "aux.img"), filepath.Join(childDir, "aux.img")); err != nil {
		cleanup()
		return EphemeralFork{}, fmt.Errorf("ephemeral fork: copy aux.img: %w", err)
	}

	cfg, err := vmconfig.Load(childDir)
	if err != nil {
		cleanup()
		return EphemeralFork{}, fmt.Errorf("ephemeral fork: load child config: %w", err)
	}
	cfg.ParentVM = opts.Parent
	cfg.ForkedAt = createdAt
	if err := vmconfig.Save(childDir, cfg); err != nil {
		cleanup()
		return EphemeralFork{}, fmt.Errorf("ephemeral fork: save child config: %w", err)
	}

	return EphemeralFork{
		Name:      name,
		Path:      childDir,
		Source:    opts.Parent,
		CreatedAt: createdAt,
	}, nil
}

// CleanupEphemeralFork removes an ephemeral child vmDir. It refuses to
// remove a path that does not contain the .ephemeral sentinel, to keep
// the cleanup defensive against accidental misuse.
func CleanupEphemeralFork(path string) error {
	if path == "" {
		return errors.New("ephemeral fork: cleanup path required")
	}
	clean := filepath.Clean(path)
	if clean == "." || clean == string(filepath.Separator) {
		return fmt.Errorf("ephemeral fork: refusing to remove %q", path)
	}
	if _, err := os.Stat(filepath.Join(clean, ephemeralSentinel)); err != nil {
		return fmt.Errorf("ephemeral fork: %s lacks .ephemeral sentinel; refusing to remove", clean)
	}
	return os.RemoveAll(clean)
}

// EphemeralGCOptions configures ephemeral-fork garbage collection.
type EphemeralGCOptions struct {
	BaseDir   string
	DryRun    bool
	IsActive  func(string) bool
	RemoveAll func(string) error
}

// EphemeralGCResult summarizes a sweep.
type EphemeralGCResult struct {
	Scanned      int
	Candidates   int
	SkippedAlive int
	Removed      int
	Paths        []string
}

// GCEphemeralForks removes ephemeral child vmDirs whose run.lock is
// releasable (no live process holding it). Marker is the .ephemeral
// sentinel; presence is required (and sufficient) for sweeping.
func GCEphemeralForks(opts EphemeralGCOptions) (EphemeralGCResult, error) {
	baseDir := opts.BaseDir
	if baseDir == "" {
		baseDir = vmconfig.BaseDir()
	}
	isActive := opts.IsActive
	if isActive == nil {
		isActive = ephemeralForkIsActive
	}
	removeAll := opts.RemoveAll
	if removeAll == nil {
		removeAll = os.RemoveAll
	}

	entries, err := os.ReadDir(baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return EphemeralGCResult{}, nil
		}
		return EphemeralGCResult{}, fmt.Errorf("read vm base dir: %w", err)
	}

	var result EphemeralGCResult
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(baseDir, entry.Name())
		if _, err := os.Stat(filepath.Join(path, ephemeralSentinel)); err != nil {
			continue
		}
		result.Scanned++
		if isActive(path) {
			result.SkippedAlive++
			continue
		}
		result.Candidates++
		result.Paths = append(result.Paths, path)
		if opts.DryRun {
			continue
		}
		if err := removeAll(path); err != nil {
			return result, fmt.Errorf("remove ephemeral fork %s: %w", path, err)
		}
		result.Removed++
	}
	return result, nil
}

// ephemeralForkIsActive returns true if a live process holds the
// vmDir's run.lock. Stale lock files (process gone) are not active.
func ephemeralForkIsActive(path string) bool {
	return isVMRunningAt(path)
}
