package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	vz "github.com/tmc/apple/virtualization"
	virtiofsx "github.com/tmc/apple/x/vzkit/virtiofs"
	agentstate "github.com/tmc/vz-macos/internal/agent"
	"github.com/tmc/vz-macos/internal/vmconfig"
)

// volumeSlice implements flag.Value for collecting multiple -v flags.
type volumeSlice []vmconfig.VolumeMount

func (v *volumeSlice) String() string {
	if v == nil || len(*v) == 0 {
		return ""
	}
	var parts []string
	for _, m := range *v {
		s := m.HostPath
		if m.Tag != "" {
			s += ":" + m.Tag
		}
		if m.ReadOnly {
			s += ":ro"
		}
		if len(m.MountOpts) > 0 {
			s += ":" + strings.Join(m.MountOpts, ",")
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, ", ")
}

func (v *volumeSlice) Set(value string) error {
	mount, err := virtiofsx.ParseMount(value)
	if err != nil {
		return err
	}
	*v = append(*v, mount)
	return nil
}

// createVolumeConfigs creates VirtioFS configurations for all volume mounts
// and prints mount instructions for each volume.
func createVolumeConfigs(mounts []vmconfig.VolumeMount) ([]vz.VZVirtioFileSystemDeviceConfiguration, error) {
	if len(mounts) == 0 {
		return nil, nil
	}

	configs, err := virtiofsx.CreateDevices(mounts)
	if err != nil {
		return nil, err
	}

	printVolumeMountInfo(mounts)
	return configs, nil
}

// printVolumeMountInfo prints mount instructions for each volume.
func printVolumeMountInfo(mounts []vmconfig.VolumeMount) {
	if len(mounts) == 0 {
		return
	}

	untaggedCount := 0
	for _, mount := range mounts {
		if mount.Tag == "" {
			untaggedCount++
		}
	}

	usedKeys := make(map[string]bool)

	fmt.Println("Volume mounts:")
	for _, mount := range mounts {
		mode := "rw"
		if mount.ReadOnly {
			mode = "ro"
		}

		if mount.Tag != "" {
			opts := ""
			if len(mount.MountOpts) > 0 {
				opts = " [" + strings.Join(mount.MountOpts, ",") + "]"
			}
			guestPath := volumeGuestMountPoint(mount.Tag, linuxMode)
			fmt.Printf("  %s -> tag %q (%s%s)\n", mount.HostPath, mount.Tag, mode, opts)
			fmt.Printf("    guest: %s\n", volumeGuestMountCommand(mount.Tag, guestPath, linuxMode))
		} else {
			guestPath := "/Volumes/My Shared Files"
			if untaggedCount > 1 {
				baseName := filepath.Base(mount.HostPath)
				key := baseName
				for i := 2; usedKeys[key]; i++ {
					key = fmt.Sprintf("%s-%d", baseName, i)
				}
				usedKeys[key] = true
				if linuxMode {
					guestPath = volumeGuestMountPoint(key, true)
				} else {
					guestPath = "/Volumes/My Shared Files/" + key
				}
			} else if linuxMode {
				guestPath = volumeGuestMountPoint(filepath.Base(mount.HostPath), true)
			}
			fmt.Printf("  %s -> %s (%s)\n", mount.HostPath, guestPath, mode)
		}
	}
}

func volumeGuestMountPoint(tag string, linuxGuest bool) string {
	if linuxGuest {
		return "/mnt/" + tag
	}
	return "/Volumes/" + tag
}

func volumeGuestMountCommand(tag, mountPoint string, linuxGuest bool) string {
	if linuxGuest {
		return fmt.Sprintf("mount -t virtiofs %s %s", tag, mountPoint)
	}
	return fmt.Sprintf("mount_virtiofs %s %s", tag, mountPoint)
}

// getEffectiveVolumes returns the combined list of volumes from -vol flags,
// legacy -share-dir, and saved VM configuration.
//
// When -vol flags are provided on the command line, they are used and saved
// to the VM config for future runs. When no -vol flags are given, saved
// volumes from the VM config are loaded instead.
func getEffectiveVolumes() []vmconfig.VolumeMount {
	policy, err := currentSandboxPolicy()
	if err != nil {
		fmt.Printf("warning: sandbox policy: %v\n", err)
		return nil
	}

	cliVolumes := make([]vmconfig.VolumeMount, len(volumes))
	copy(cliVolumes, volumes)

	// Add legacy -share-dir as a volume if specified
	if shareDir != "" {
		absShareDir, _ := filepath.Abs(shareDir)
		absShareDir = resolvePath(absShareDir)
		alreadyMounted := false
		for _, v := range cliVolumes {
			if v.HostPath == absShareDir {
				alreadyMounted = true
				break
			}
		}
		if !alreadyMounted {
			cliVolumes = append(cliVolumes, vmconfig.VolumeMount{
				HostPath: absShareDir,
				Tag:      "",
				ReadOnly: false,
			})
		}
	}

	// If volumes were specified on the command line, save them to config.
	if len(cliVolumes) > 0 {
		if !policy.AllowsVolumes() {
			return nil
		}
		if err := saveVolumesToConfig(vmDir, cliVolumes); err != nil {
			fmt.Printf("warning: save volume config: %v\n", err)
		}
		return cliVolumes
	}

	// No CLI volumes: load saved volumes from config.
	cfg, err := vmconfig.Load(vmDir)
	if err != nil {
		fmt.Printf("warning: load volume config: %v\n", err)
		return nil
	}
	if len(cfg.Volumes) > 0 && policy.AllowsVolumes() {
		fmt.Printf("Using saved volume mounts from %s\n", filepath.Join(vmDir, "config.json"))
	}
	return policy.EffectiveVolumes(nil, cfg.Volumes)
}

// saveVolumesToConfig persists volume mounts to the VM config file.
func saveVolumesToConfig(dir string, mounts []vmconfig.VolumeMount) error {
	return vmconfig.SetVolumes(dir, mounts)
}

// taggedVolumes returns only the volumes that have custom tags (not auto-mount).
func taggedVolumes(mounts []vmconfig.VolumeMount) []vmconfig.VolumeMount {
	var tagged []vmconfig.VolumeMount
	for _, m := range mounts {
		if m.Tag != "" {
			tagged = append(tagged, m)
		}
	}
	return tagged
}

// autoMountTaggedVolumes connects to the guest agent and mounts tagged
// VirtioFS volumes. It retries the agent connection with backoff until
// ctx is cancelled. This is intended to be run in a background goroutine
// after VM start.
func autoMountTaggedVolumes(ctx context.Context, cs *ControlServer, mounts []vmconfig.VolumeMount) {
	tagged := taggedVolumes(mounts)
	if len(tagged) == 0 && len(effectiveSharedFolders(vmDir)) == 0 && (!linuxMode || !enableRosetta) {
		return
	}

	// Keep running for the VM lifetime so guest reboot cycles get re-mounted
	// when the agent becomes available again.
	for {
		if err := waitForAgent(ctx, cs); err != nil {
			return
		}

		if len(tagged) > 0 {
			fmt.Println("Auto-mounting tagged volumes in guest...")
			mountTaggedVolumesOnce(ctx, cs, tagged, linuxVirtioFSOwner(vmDir))
		}

		if linuxMode && enableRosetta {
			setupRosettaInGuest(ctx, cs)
		}

		sharedConfigured := len(effectiveSharedFolders(vmDir)) > 0
		if sharedConfigured && !linuxMode {
			mounted, err := mountSharedFoldersInGuest(vmDir, defaultSharedFoldersMountPoint)
			if err != nil {
				fmt.Printf("auto-mount shared folders: %v\n", err)
			} else if mounted {
				fmt.Printf("Auto-mounted shared folders at %s\n", defaultSharedFoldersMountPoint)
			} else if verbose {
				fmt.Printf("Shared folders already mounted at %s\n", defaultSharedFoldersMountPoint)
			}
		}

		// Wait here until the guest agent goes away (e.g., guest reboot).
		// Then loop and perform mount reconciliation again when it returns.
		if err := waitForAgentLoss(ctx, cs); err != nil {
			return
		}
	}
}

func waitForAgent(ctx context.Context, cs *ControlServer) error {
	for attempt := 0; ; attempt++ {
		delay := time.Duration(min(attempt+1, 5)) * time.Second
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}

		_, err := cs.getAgent()
		if err == nil {
			return nil
		}
		if verbose {
			fmt.Printf("auto-mount: waiting for agent (attempt %d): %v\n", attempt+1, err)
		}
	}
}

func waitForAgentLoss(ctx context.Context, cs *ControlServer) error {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			_, err := cs.getAgent()
			if err != nil {
				if verbose {
					fmt.Printf("auto-mount: agent unavailable; will re-mount after reconnect: %v\n", err)
				}
				return nil
			}
		}
	}
}

func autoMountAgent(cs *ControlServer, label string) (*agentstate.AgentClient, bool) {
	a, err := cs.getAgent()
	if err != nil {
		fmt.Printf("%s: agent unavailable: %v\n", label, err)
		return nil, false
	}
	if a == nil {
		fmt.Printf("%s: agent unavailable\n", label)
		return nil, false
	}
	return a, true
}

func mountTaggedVolumesOnce(ctx context.Context, cs *ControlServer, tagged []vmconfig.VolumeMount, owner virtioFSOwner) {
	for _, m := range tagged {
		var mountPoint string
		if linuxMode {
			mountPoint = "/mnt/" + m.Tag
		} else {
			mountPoint = "/Volumes/" + m.Tag
		}

		// Create mount point
		a, ok := autoMountAgent(cs, "  auto-mount "+m.Tag)
		if !ok {
			continue
		}
		cs.mu.Lock()
		mkdirCtx, mkdirCancel := context.WithTimeout(ctx, 10*time.Second)
		_, mkdirErr := a.Exec(mkdirCtx, []string{"mkdir", "-p", mountPoint}, nil, "")
		mkdirCancel()
		cs.mu.Unlock()

		if mkdirErr != nil {
			fmt.Printf("  auto-mount %s: mkdir failed: %v\n", m.Tag, mkdirErr)
			continue
		}

		// Check if already mounted (common after VM resume).
		a, ok = autoMountAgent(cs, "  auto-mount "+m.Tag)
		if !ok {
			continue
		}
		cs.mu.Lock()
		checkCtx, checkCancel := context.WithTimeout(ctx, 5*time.Second)
		checkResult, checkErr := a.Exec(checkCtx, []string{"mount"}, nil, "")
		checkCancel()
		cs.mu.Unlock()

		if checkErr == nil && checkResult.ExitCode == 0 {
			if strings.Contains(string(checkResult.Stdout), mountPoint) {
				if verbose {
					fmt.Printf("  %s already mounted at %s\n", m.Tag, mountPoint)
				}
				continue
			}
		}

		// Mount the VirtioFS tag using guest-native mount semantics.
		mountArgs := virtioFSMountArgsWithOwner(m, mountPoint, linuxMode, owner)

		a, ok = autoMountAgent(cs, "  auto-mount "+m.Tag)
		if !ok {
			continue
		}
		cs.mu.Lock()
		mountCtx, mountCancel := context.WithTimeout(ctx, 10*time.Second)
		result, mountErr := a.Exec(mountCtx, mountArgs, nil, "")
		mountCancel()
		cs.mu.Unlock()

		if mountErr != nil {
			fmt.Printf("  auto-mount %s: mount failed: %v\n", m.Tag, mountErr)
			continue
		}
		if result.ExitCode != 0 {
			fmt.Printf("  auto-mount %s: %s\n", m.Tag, mountFailureMessage(result.ExitCode, strings.TrimSpace(string(result.Stderr))))
			continue
		}

		mode := "rw"
		if m.ReadOnly {
			mode = "ro"
		}
		fmt.Printf("  mounted %s at %s (%s)\n", m.Tag, mountPoint, mode)
	}
}

func setupRosettaInGuest(ctx context.Context, cs *ControlServer) {
	args := []string{"sh", "-lc", rosettaGuestSetupScript}

	a, ok := autoMountAgent(cs, "auto-mount Rosetta")
	if !ok {
		return
	}
	cs.mu.Lock()
	runCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	result, err := a.Exec(runCtx, args, nil, "")
	cancel()
	cs.mu.Unlock()

	if err != nil {
		fmt.Printf("auto-mount Rosetta: %v\n", err)
		return
	}
	if result.ExitCode != 0 {
		stderr := strings.TrimSpace(string(result.Stderr))
		if rosettaRegisterFailureIsBenign(stderr) {
			if verbose {
				fmt.Printf("auto-mount Rosetta register skipped: %s\n", stderr)
			}
			return
		}
		fmt.Printf("optional Rosetta setup failed (exit %d); x86_64 Linux binaries may not run: %s\n", result.ExitCode, stderr)
		return
	}
	if verbose {
		fmt.Println("Rosetta mounted and registered in guest")
	}
}

func mountFailureMessage(exitCode int32, stderr string) string {
	msg := fmt.Sprintf("mount failed (exit %d)", exitCode)
	if stderr != "" {
		msg += ": " + stderr
	}
	if strings.Contains(stderr, "Unknown parameter 'cache'") || strings.Contains(stderr, `Unknown parameter "cache"`) {
		msg += " (guest kernel rejected the VirtioFS cache option; disable auto-mount and mount the tag manually without cache=...)"
	}
	return msg
}

func rosettaRegisterFailureIsBenign(stderr string) bool {
	return strings.Contains(stderr, "failed to open elf at --register")
}

const rosettaGuestSetupScript = `set -eu
mkdir -p /run/rosetta
if ! mount | grep -q '^rosetta on /run/rosetta '; then
	mount -t virtiofs -o ro rosetta /run/rosetta
fi
if [ -x /run/rosetta/rosetta ]; then
	/run/rosetta/rosetta --register
fi`

func virtioFSMountArgs(m vmconfig.VolumeMount, mountPoint string, linuxGuest bool) []string {
	return virtioFSMountArgsWithOwner(m, mountPoint, linuxGuest, defaultLinuxVirtioFSOwner())
}

type virtioFSOwner struct {
	UID uint32
	GID uint32
}

func defaultLinuxVirtioFSOwner() virtioFSOwner {
	return virtioFSOwner{UID: 1000, GID: 1000}
}

func linuxVirtioFSOwner(dir string) virtioFSOwner {
	owner := defaultLinuxVirtioFSOwner()
	cfg, err := vmconfig.Load(dir)
	if err != nil {
		if verbose {
			fmt.Printf("warning: load guest user mapping: %v\n", err)
		}
		return owner
	}
	if cfg.GuestUserUID != 0 {
		owner.UID = cfg.GuestUserUID
	}
	if cfg.GuestUserGID != 0 {
		owner.GID = cfg.GuestUserGID
	}
	return owner
}

func virtioFSMountArgsWithOwner(m vmconfig.VolumeMount, mountPoint string, linuxGuest bool, owner virtioFSOwner) []string {
	if linuxGuest {
		opts := append([]string{}, m.MountOpts...)
		// Default Linux guests to cache=none. Apple's VirtioFS host has no
		// cache-coherency / sync mode, so guest-side caching produces stale
		// directory listings — host edits don't surface in `ls` until the
		// guest's dentry/page cache happens to expire. cache=none makes
		// every lookup hit the host, which is the only way `ls` after a
		// host-side write reflects the new state. Users who explicitly
		// pass cache=<other> (e.g. metadata, always) keep their setting.
		if !hasCacheOpt(opts) {
			opts = append([]string{"cache=none"}, opts...)
		}
		if !hasOptPrefix(opts, "uid=") {
			opts = append(opts, "uid="+strconv.FormatUint(uint64(owner.UID), 10))
		}
		if !hasOptPrefix(opts, "gid=") {
			opts = append(opts, "gid="+strconv.FormatUint(uint64(owner.GID), 10))
		}
		if m.ReadOnly {
			opts = append([]string{"ro"}, opts...)
		}
		args := []string{"mount", "-t", "virtiofs"}
		if len(opts) > 0 {
			args = append(args, "-o", strings.Join(opts, ","))
		}
		return append(args, m.Tag, mountPoint)
	}

	args := []string{"mount_virtiofs"}
	if m.ReadOnly {
		args = append(args, "-r")
	}
	// macOS mount_virtiofs only documents -r, -u, and -g. Generic MountOpts are
	// Linux-only today; passing -o here would be invalid.
	return append(args, m.Tag, mountPoint)
}

// hasCacheOpt reports whether opts already contains a cache=... entry.
// Used to decide whether to inject the cache=none default for Linux guests.
func hasCacheOpt(opts []string) bool {
	return hasOptPrefix(opts, "cache=")
}

func hasOptPrefix(opts []string, prefix string) bool {
	for _, o := range opts {
		if strings.HasPrefix(o, prefix) {
			return true
		}
	}
	return false
}
