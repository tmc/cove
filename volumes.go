package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	vz "github.com/tmc/apple/virtualization"
	"github.com/tmc/apple/x/vzkit"
)

// VolumeMount represents a host-to-guest volume mount configuration.
type VolumeMount = vzkit.VolumeMount

// volumeSlice implements flag.Value for collecting multiple -v flags.
type volumeSlice []VolumeMount

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
		parts = append(parts, s)
	}
	return strings.Join(parts, ", ")
}

func (v *volumeSlice) Set(value string) error {
	mount, err := vzkit.ParseVolumeSpec(value)
	if err != nil {
		return err
	}
	*v = append(*v, mount)
	return nil
}

// createVolumeConfigs creates VirtioFS configurations for all volume mounts
// and prints mount instructions for each volume.
func createVolumeConfigs(mounts []VolumeMount) ([]vz.VZVirtioFileSystemDeviceConfiguration, error) {
	if len(mounts) == 0 {
		return nil, nil
	}

	configs, err := vzkit.CreateVirtioFSDevices(mounts)
	if err != nil {
		return nil, err
	}

	printVolumeMountInfo(mounts)
	return configs, nil
}

// printVolumeMountInfo prints mount instructions for each volume.
func printVolumeMountInfo(mounts []VolumeMount) {
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
			fmt.Printf("  %s -> tag %q (%s)\n", mount.HostPath, mount.Tag, mode)
			fmt.Printf("    guest: mount_virtiofs %s /mnt/%s\n", mount.Tag, mount.Tag)
		} else {
			guestPath := "/Volumes/My Shared Files"
			if untaggedCount > 1 {
				baseName := filepath.Base(mount.HostPath)
				key := baseName
				for i := 2; usedKeys[key]; i++ {
					key = fmt.Sprintf("%s-%d", baseName, i)
				}
				usedKeys[key] = true
				guestPath = "/Volumes/My Shared Files/" + key
			}
			fmt.Printf("  %s -> %s (%s)\n", mount.HostPath, guestPath, mode)
		}
	}
}

// getEffectiveVolumes returns the combined list of volumes from -vol flags,
// legacy -share-dir, and saved VM configuration.
//
// When -vol flags are provided on the command line, they are used and saved
// to the VM config for future runs. When no -vol flags are given, saved
// volumes from the VM config are loaded instead.
func getEffectiveVolumes() []VolumeMount {
	cliVolumes := make([]VolumeMount, len(volumes))
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
			cliVolumes = append(cliVolumes, VolumeMount{
				HostPath: absShareDir,
				Tag:      "",
				ReadOnly: false,
			})
		}
	}

	// If volumes were specified on the command line, save them to config.
	if len(cliVolumes) > 0 {
		if err := saveVolumesToConfig(vmDir, cliVolumes); err != nil {
			fmt.Printf("warning: save volume config: %v\n", err)
		}
		return cliVolumes
	}

	// No CLI volumes: load saved volumes from config.
	cfg, err := LoadVMConfig(vmDir)
	if err != nil {
		fmt.Printf("warning: load volume config: %v\n", err)
		return nil
	}
	if len(cfg.Volumes) > 0 {
		fmt.Printf("Using saved volume mounts from %s\n", filepath.Join(vmDir, "config.json"))
	}
	return cfg.Volumes
}

// saveVolumesToConfig persists volume mounts to the VM config file.
func saveVolumesToConfig(dir string, mounts []VolumeMount) error {
	cfg, err := LoadVMConfig(dir)
	if err != nil {
		return err
	}
	cfg.Volumes = mounts
	return SaveVMConfig(dir, cfg)
}

// taggedVolumes returns only the volumes that have custom tags (not auto-mount).
func taggedVolumes(mounts []VolumeMount) []VolumeMount {
	var tagged []VolumeMount
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
func autoMountTaggedVolumes(ctx context.Context, cs *ControlServer, mounts []VolumeMount) {
	tagged := taggedVolumes(mounts)
	sharedConfigured := len(LoadSharedFolders(vmDir)) > 0
	if len(tagged) == 0 && !sharedConfigured {
		return
	}

	// Wait for agent to become available (VM needs to finish booting).
	var connected bool
	for attempt := 0; !connected; attempt++ {
		delay := time.Duration(min(attempt+1, 5)) * time.Second
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}

		cs.mu.Lock()
		err := cs.ensureAgent()
		cs.mu.Unlock()
		if err != nil {
			if verbose {
				fmt.Printf("auto-mount: waiting for agent (attempt %d): %v\n", attempt+1, err)
			}
			continue
		}
		connected = true
	}

	if len(tagged) > 0 {
		fmt.Println("Auto-mounting tagged volumes in guest...")
		for _, m := range tagged {
			mountPoint := "/mnt/" + m.Tag
			if linuxMode {
				mountPoint = "/mnt/" + m.Tag
			} else {
				mountPoint = "/Volumes/" + m.Tag
			}

			// Create mount point
			cs.mu.Lock()
			mkdirCtx, mkdirCancel := context.WithTimeout(ctx, 10*time.Second)
			_, mkdirErr := cs.agent.Exec(mkdirCtx, []string{"mkdir", "-p", mountPoint}, nil, "")
			mkdirCancel()
			cs.mu.Unlock()

			if mkdirErr != nil {
				fmt.Printf("  auto-mount %s: mkdir failed: %v\n", m.Tag, mkdirErr)
				continue
			}

			// Check if already mounted (common after VM resume).
			cs.mu.Lock()
			checkCtx, checkCancel := context.WithTimeout(ctx, 5*time.Second)
			checkResult, checkErr := cs.agent.Exec(checkCtx, []string{"mount"}, nil, "")
			checkCancel()
			cs.mu.Unlock()

			if checkErr == nil && checkResult.ExitCode == 0 {
				if strings.Contains(string(checkResult.Stdout), mountPoint) {
					fmt.Printf("  %s already mounted at %s\n", m.Tag, mountPoint)
					continue
				}
			}

			// Mount the VirtioFS tag
			cs.mu.Lock()
			mountCtx, mountCancel := context.WithTimeout(ctx, 10*time.Second)
			result, mountErr := cs.agent.Exec(mountCtx, []string{"mount_virtiofs", m.Tag, mountPoint}, nil, "")
			mountCancel()
			cs.mu.Unlock()

			if mountErr != nil {
				fmt.Printf("  auto-mount %s: mount failed: %v\n", m.Tag, mountErr)
				continue
			}
			if result.ExitCode != 0 {
				fmt.Printf("  auto-mount %s: mount failed (exit %d): %s\n", m.Tag, result.ExitCode, strings.TrimSpace(string(result.Stderr)))
				continue
			}

			mode := "rw"
			if m.ReadOnly {
				mode = "ro"
			}
			fmt.Printf("  mounted %s at %s (%s)\n", m.Tag, mountPoint, mode)
		}
	}

	if sharedConfigured && !linuxMode {
		mounted, err := mountSharedFoldersInGuest(vmDir, defaultSharedFoldersMountPoint)
		if err != nil {
			fmt.Printf("auto-mount shared folders: %v\n", err)
			return
		}
		if mounted {
			fmt.Printf("Auto-mounted shared folders at %s\n", defaultSharedFoldersMountPoint)
		} else if verbose {
			fmt.Printf("Shared folders already mounted at %s\n", defaultSharedFoldersMountPoint)
		}
	}
}
