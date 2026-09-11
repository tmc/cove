package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	vz "github.com/tmc/apple/virtualization"
	virtiofsx "github.com/tmc/apple/x/vzkit/virtiofs"
	agentstate "github.com/tmc/cove/internal/agent"
	"github.com/tmc/cove/internal/vmconfig"
)

// volumeSlice implements flag.Value for collecting multiple -v flags.
type volumeSlice []vmconfig.VolumeMount

var rosettaRuntimeSetup bool

var tccVolumeWarnings sync.Map
var loggedSavedVolumes sync.Map

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
	mounts, skipped := validateVolumes(mounts)
	for _, s := range skipped {
		fmt.Printf("warning: skipping volume %s: %s (invalid directory share would abort VM start)\n",
			volumeIssueLabel(s.Mount), s.Reason)
	}
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
		cfgPath := filepath.Join(vmDir, "config.json")
		if _, loaded := loggedSavedVolumes.LoadOrStore(cfgPath, true); !loaded {
			fmt.Printf("Using saved volume mounts from %s\n", cfgPath)
		}
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
// VirtioFS volumes and shared folders. It maintains active reconciliation
// to recover dead mounts and reconnect when backing volumes return.
func autoMountTaggedVolumes(ctx context.Context, cs *ControlServer, mounts []vmconfig.VolumeMount) {
	mounts, _ = validateVolumes(mounts)
	tagged := taggedVolumes(mounts)
	if len(tagged) == 0 && len(effectiveSharedFolders(vmDir)) == 0 && (!linuxMode || !rosettaRuntimeSetup) {
		return
	}

	// Keep running for the VM lifetime so guest reboot cycles get re-mounted
	// when the agent becomes available again.
	for {
		if err := waitForAgent(ctx, cs); err != nil {
			return
		}

		reconcileAllMounts(ctx, cs, tagged, vmDir)

		if linuxMode && rosettaRuntimeSetup {
			setupRosettaInGuest(ctx, cs)
		}

		// Monitor and reconcile mounts against host volume drops/reconnects.
		// Returns nil when agent is lost (triggering waitForAgent in outer loop).
		if err := monitorAndReconcileMounts(ctx, cs, tagged, vmDir); err != nil {
			return
		}
	}
}

func reconcileAllMounts(ctx context.Context, cs *ControlServer, tagged []vmconfig.VolumeMount, vmDir string) {
	if len(tagged) > 0 {
		mountTaggedVolumesOnce(ctx, cs, tagged, linuxVirtioFSOwner(vmDir))
	}

	sharedFolders := effectiveSharedFolders(vmDir)
	if len(sharedFolders) > 0 {
		mountRoot := defaultSharedFoldersMountRoot(vmDir)
		mounted, err := mountSharedFoldersInGuest(vmDir, mountRoot)
		if err != nil {
			if verbose {
				fmt.Printf("auto-mount shared folders: %v\n", err)
			}
		} else if mounted {
			if mountRoot == "" {
				fmt.Printf("Auto-mounted shared folders at %s\n", sharedFoldersMountSummary(vmDir))
			} else {
				fmt.Printf("Auto-mounted shared folders at %s\n", mountRoot)
				if !linuxMode {
					go warnWhenTCCFDABlocked(ctx, mountRoot)
				}
			}
		}
	}
}

func monitorAndReconcileMounts(ctx context.Context, cs *ControlServer, tagged []vmconfig.VolumeMount, vmDir string) error {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	lastSharedPresence := make(map[string]bool)
	for _, f := range effectiveSharedFolders(vmDir) {
		_, err := os.Stat(f.Path)
		lastSharedPresence[f.Path] = (err == nil)
	}
	lastTaggedPresence := make(map[string]bool)
	for _, m := range tagged {
		_, err := hostPathStat(m.HostPath)
		lastTaggedPresence[m.HostPath] = (err == nil)
	}

	tickCount := 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			tickCount++
			_, err := cs.getAgent()
			if err != nil {
				if verbose {
					fmt.Printf("auto-mount: agent unavailable; will re-mount after reconnect: %v\n", err)
				}
				return nil
			}

			// Check host shared folder paths
			sharedFolders := effectiveSharedFolders(vmDir)
			sharedChanged := false
			for _, f := range sharedFolders {
				_, statErr := os.Stat(f.Path)
				present := (statErr == nil)
				if present != lastSharedPresence[f.Path] {
					sharedChanged = true
					lastSharedPresence[f.Path] = present
					if present {
						fmt.Printf("Shared folder backing path reconnected: %s (%s)\n", f.Tag, f.Path)
					} else {
						fmt.Printf("Shared folder backing path disconnected: %s (%s)\n", f.Tag, f.Path)
					}
				}
			}

			if sharedChanged {
				_, _ = cs.applySharedFoldersToRunningVM(sharedFolders)
				mountRoot := defaultSharedFoldersMountRoot(vmDir)
				_, _ = mountSharedFoldersInGuest(vmDir, mountRoot)
			}

			// Check host tagged volume paths
			taggedChanged := false
			for _, m := range tagged {
				_, statErr := hostPathStat(m.HostPath)
				present := (statErr == nil)
				if present != lastTaggedPresence[m.HostPath] {
					taggedChanged = true
					lastTaggedPresence[m.HostPath] = present
					if present {
						fmt.Printf("Volume backing path reconnected: %s (%s)\n", m.Tag, m.HostPath)
					} else {
						fmt.Printf("Volume backing path disconnected: %s (%s)\n", m.Tag, m.HostPath)
					}
				}
			}

			if taggedChanged {
				mountTaggedVolumesOnce(ctx, cs, tagged, linuxVirtioFSOwner(vmDir))
			}

			// Periodically check for and heal any dead mounts
			if tickCount%3 == 0 {
				reconcileAllMounts(ctx, cs, tagged, vmDir)
			}
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
				// Probe health of mount point
				a, ok = autoMountAgent(cs, "  probe-mount "+m.Tag)
				if ok {
					cs.mu.Lock()
					probeCtx, probeCancel := context.WithTimeout(ctx, 5*time.Second)
					probeResult, probeErr := a.Exec(probeCtx, []string{"ls", "-1", mountPoint}, nil, "")
					probeCancel()
					cs.mu.Unlock()

					if probeErr == nil && probeResult != nil && probeResult.ExitCode == 0 {
						if verbose {
							fmt.Printf("  %s already mounted at %s\n", m.Tag, mountPoint)
						}
						continue
					}
					// Dead/stale mount point: force unmount
					if verbose {
						fmt.Printf("  %s at %s is dead/stale; recovering...\n", m.Tag, mountPoint)
					}
					cs.mu.Lock()
					umountCtx, umountCancel := context.WithTimeout(ctx, 10*time.Second)
					_, _ = a.Exec(umountCtx, []string{"umount", "-f", mountPoint}, nil, "")
					umountCancel()
					cs.mu.Unlock()
				}
			}
		}

		// Check if host path is currently present
		if _, err := hostPathStat(m.HostPath); err != nil {
			if verbose {
				fmt.Printf("  auto-mount %s: host backing path absent (%s); skipping until reconnected\n", m.Tag, m.HostPath)
			}
			continue
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
		if !linuxMode {
			go warnWhenTCCFDABlocked(ctx, mountPoint)
		}
	}
}

func warnWhenTCCFDABlocked(ctx context.Context, guestPath string) {
	if guestPath == "" {
		return
	}
	if _, loaded := tccVolumeWarnings.LoadOrStore(guestPath, struct{}{}); loaded {
		return
	}

	sock := GetControlSocketPathForVM(vmDir)
	for attempt := 0; attempt < 12; attempt++ {
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(min(attempt+1, 5)) * time.Second):
		}

		result, err := runTCCFDAProbe(sock, guestPath)
		if err != nil {
			if verbose {
				fmt.Printf("Full Disk Access probe for %s: waiting for user agent: %v\n", guestPath, err)
			}
			continue
		}
		if result.ExitCode == 0 {
			if verbose {
				fmt.Printf("Full Disk Access probe for %s: readable via user agent\n", guestPath)
			}
			return
		}
		stderr := strings.TrimSpace(result.Stderr)
		if stderr == "" {
			stderr = fmt.Sprintf("exit %d", result.ExitCode)
		}
		if isENOENTStderr(stderr) {
			if verbose {
				fmt.Printf("Full Disk Access probe for %s: path missing: %s\n", guestPath, stderr)
			}
			return
		}
		printMountedVolumeFDAWarning(guestPath, stderr)
		return
	}
	printMountedVolumeFDAWarning(guestPath, "user agent unavailable")
}

func printMountedVolumeFDAWarning(guestPath, detail string) {
	fmt.Printf("COVE_TCC_FDA_REQUIRED path=%s agent=/usr/local/bin/vz-agent detail=%s\n", shellQuote(guestPath), shellQuote(detail))
	fmt.Printf("Full Disk Access needed for %s: mounted but not readable via user agent (%s)\n", guestPath, detail)
	fmt.Printf("  guided fix: cove doctor tcc-fda -tcc-path %s -password <guest-admin-password>\n", shellQuote(guestPath))
	fmt.Printf("  verify with: cove doctor --tcc-path %s\n", shellQuote(guestPath))
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
