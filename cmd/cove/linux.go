// Linux VM support for cove.
//
// Supports two boot modes:
// - Direct kernel boot: uses VZLinuxBootLoader with kernel + initrd
// - EFI boot: uses VZEFIBootLoader with ISO image for installation

package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tmc/apple/dispatch"
	"github.com/tmc/apple/foundation"
	privvz "github.com/tmc/apple/private/virtualization"
	vz "github.com/tmc/apple/virtualization"
	audiox "github.com/tmc/apple/x/vzkit/audio"
	"github.com/tmc/apple/x/vzkit/clipboard"
	configx "github.com/tmc/apple/x/vzkit/config"
	displayx "github.com/tmc/apple/x/vzkit/display"
	linuxconfig "github.com/tmc/apple/x/vzkit/linuxconfig"
	platformx "github.com/tmc/apple/x/vzkit/platform"
	storagex "github.com/tmc/apple/x/vzkit/storage"
	"github.com/tmc/cove/internal/guestplan"
	"github.com/tmc/cove/internal/vmconfig"
	"github.com/tmc/cove/internal/vmrun"
)

// buildLinuxVMConfigurationWithConfig builds a VZVirtualMachineConfiguration for Linux.
// rc carries the per-run intent, including any host-resolved ISO path.
func buildLinuxVMConfigurationWithConfig(rc vmrun.RunConfig, hc vmrun.HostConfig, diskImagePath string) (vz.VZVirtualMachineConfiguration, error) {
	rc.DiskPath = diskImagePath
	plan, err := guestplan.Linux(rc, hc)
	if err != nil {
		return vz.VZVirtualMachineConfiguration{}, err
	}

	netConfig, err := ParseNetworkMode(plan.Network.Mode)
	if err != nil {
		return vz.VZVirtualMachineConfiguration{}, fmt.Errorf("parse network mode: %w", err)
	}
	macAddr := loadOrCreateMACAddressForVM(hc.VMDir)
	builderNetwork := linuxconfig.Network{Config: netConfig}
	if macAddr.ID != 0 {
		builderNetwork.MAC = &macAddr
	}
	if netConfig.Mode == NetworkModeFileHandle || netConfig.Mode == NetworkModeNone {
		builderNetwork = linuxconfig.Network{}
	}

	var audioConfig *audiox.Config
	if plan.Audio.Enabled {
		audioConfig = &audiox.Config{
			InputEnabled:  plan.Audio.HostInput,
			OutputEnabled: plan.Audio.HostOutput,
		}
	}

	displayConfigs := make([]displayx.Config, 0, len(plan.Display))
	for _, d := range plan.Display {
		displayConfigs = append(displayConfigs, displayx.Config{Width: d.Width, Height: d.Height, PPI: d.PPI})
	}

	config, err := linuxconfig.Build(linuxconfig.Config{
		CPUCount:      plan.CPUCount,
		MemoryGB:      plan.MemoryGB,
		Display:       displayConfigs,
		Network:       builderNetwork,
		Audio:         audioConfig,
		Keyboard:      true,
		Pointing:      true,
		Entropy:       true,
		USBController: true,
		MemoryBalloon: true,
		Socket:        true,
	})
	if err != nil {
		return config, fmt.Errorf("build linux device config: %w", err)
	}

	// Platform configuration (generic for Linux)
	fmt.Println("Setting up Linux platform configuration...")
	platformConfig := vz.NewVZGenericPlatformConfiguration()

	// Machine identifier (unique to this VM)
	machineID := loadOrCreateGenericMachineIdentifier()
	platformConfig.SetMachineIdentifier(&machineID)

	if rc.LinuxNested {
		if err := validateNestedVirtualizationSupported(); err != nil {
			return config, err
		}
		platformConfig.SetNestedVirtualizationEnabled(true)
		fmt.Println("  Nested virtualization enabled")
	} else if nestedVirtualizationSupported() {
		fmt.Println("  Nested virtualization disabled")
	}

	config.SetPlatform(&platformConfig.VZPlatformConfiguration)

	kernelToUse := rc.KernelPath
	initrdToUse := rc.InitrdPath
	cmdLineToUse := rc.CmdLine
	if kernelToUse == "" {
		if installed, ok := loadInstalledLinuxBootArtifacts(hc.VMDir); ok {
			fmt.Println("  Using staged installed kernel boot (VZLinuxBootLoader)")
			kernelToUse = installed.kernel
			initrdToUse = installed.initrd
			if cmdLineToUse == "" {
				cmdLineToUse = installed.commandLine()
			}
		}
	}

	// Boot loader - choose based on flags
	if kernelToUse != "" {
		// Direct kernel boot
		fmt.Println("  Using direct kernel boot (VZLinuxBootLoader)")
		bootloader, err := createLinuxBootLoaderWithPaths(kernelToUse, initrdToUse, cmdLineToUse)
		if err != nil {
			return config, err
		}
		config.SetBootLoader(&bootloader.VZBootLoader)
	} else {
		// EFI boot (for ISO installation or installed system)
		fmt.Println("  Using EFI boot (VZEFIBootLoader)")
		bootloader, err := createEFIBootLoader()
		if err != nil {
			return config, err
		}
		config.SetBootLoader(&bootloader.VZBootLoader)
	}

	// Storage - main disk
	storageConfig, err := createLinuxRootStorageDevice(diskImagePath, false)
	if err != nil {
		return vz.VZVirtualMachineConfiguration{}, fmt.Errorf("create root storage device: %w", err)
	}

	// If ISO is provided, add it as second storage device (read-only).
	// rc.ISOPath reflects any host-side resolution runLinuxVM did via
	// (*vmrun.RunConfig).ResolveISO before calling the builder.
	if rc.ISOPath != "" {
		isoURL := foundation.NewURLFileURLWithPath(rc.ISOPath)
		isoAttachment, err := newDiskAttachment(isoURL, true, storagex.CacheReadOnly)
		if err != nil {
			return config, fmt.Errorf("failed to create ISO attachment: %w", err)
		}
		isoAttachment.Retain()

		isoStorage := vz.NewVirtioBlockDeviceConfigurationWithAttachment(&isoAttachment.VZStorageDeviceAttachment)
		isoStorage.Retain()

		// Add both storage devices
		config.SetStorageDevices([]vz.VZStorageDeviceConfiguration{
			storageConfig,
			vz.VZStorageDeviceConfigurationFromID(isoStorage.ID),
		})
	} else {
		config.SetStorageDevices([]vz.VZStorageDeviceConfiguration{storageConfig})
	}

	// Network configuration not covered by vzkit's mechanical builder.
	if netConfig.Mode == NetworkModeFileHandle {
		networkDeviceConfig, err := CreateNetworkDeviceConfiguration(netConfig)
		if err != nil {
			return config, fmt.Errorf("create network device: %w", err)
		}
		if macAddr.ID != 0 {
			networkDeviceConfig.SetMACAddress(&macAddr)
		}
		configx.SetNetworkDevices(config, networkDeviceConfig)
	}

	// Serial console
	serialConfig := createSerialConsoleConfig()
	if serialConfig.ID != 0 {
		configx.SetSerialPorts(config, serialConfig)
		fmt.Println("  Serial console attached (output to stdout)")
	}

	// Clipboard sharing (SPICE agent over Virtio console)
	if rc.EnableClipboard {
		clipboardDevice, err := clipboard.NewConfig()
		if err != nil {
			fmt.Printf("  warning: clipboard: %v\n", err)
		} else if clipboardDevice.ID != 0 {
			config.SetConsoleDevices([]vz.VZConsoleDeviceConfiguration{
				vz.VZConsoleDeviceConfigurationFromID(clipboardDevice.ID),
			})
			fmt.Println("  Clipboard sharing enabled (SPICE agent)")
		}
	}

	// Volume mounts (VirtioFS) - docker-style -v flag, plus the dedicated
	// shared-folders device that runtime live-apply mutates.
	virtioFSConfigs := linuxVirtioFSDeviceConfigs(nil, effectiveSharedFolders(hc.VMDir))
	if volumeConfigs, err := createVolumeConfigs(getEffectiveVolumes()); err != nil {
		fmt.Printf("warning: volume config: %v\n", err)
	} else {
		virtioFSConfigs = append(volumeConfigs, virtioFSConfigs...)
	}
	if len(virtioFSConfigs) > 0 {
		setDirectorySharingDevicesMulti(config, virtioFSConfigs)
	}

	// Rosetta support for x86-64 binary translation
	rosettaRuntimeSetup = false
	if rc.EnableRosetta && !sandboxStrict() {
		if err := AddRosettaToLinuxVM(config, hc.VMDir); err != nil {
			fmt.Printf("optional Rosetta setup skipped; x86_64 Linux binaries may not run: %v\n", err)
		} else {
			rosettaRuntimeSetup = true
		}
	}

	// USB storage devices
	if len(usbDevices) > 0 {
		if err := AddUSBStorageToConfig(config, usbDevices); err != nil {
			return config, fmt.Errorf("add USB storage: %w", err)
		}
	}

	if err := applyPrivateVMConfigurationWithRunConfig(config, rc); err != nil {
		return config, err
	}

	return config, nil
}

func linuxVirtioFSDeviceConfigs(volumeConfigs []vz.VZVirtioFileSystemDeviceConfiguration, sharedFolders []SharedFolderEntry) []vz.VZVirtioFileSystemDeviceConfiguration {
	configs := append([]vz.VZVirtioFileSystemDeviceConfiguration(nil), volumeConfigs...)
	sharedFoldersDevice := createSharedFoldersDevice(sharedFolders)
	if sharedFoldersDevice.ID != 0 {
		configs = append(configs, sharedFoldersDevice)
	}
	return configs
}

func createLinuxBootLoaderWithPaths(kernelPath, initrdPath, cmdLine string) (vz.VZLinuxBootLoader, error) {
	// Resolve to absolute paths (NSURL requires absolute paths)
	absKernelPath, err := filepath.Abs(kernelPath)
	if err != nil {
		return vz.VZLinuxBootLoader{}, fmt.Errorf("resolve kernel path: %w", err)
	}

	// Verify kernel exists
	if _, statErr := os.Stat(absKernelPath); statErr != nil {
		return vz.VZLinuxBootLoader{}, fmt.Errorf("kernel not found: %s", absKernelPath)
	}

	kernelURL := foundation.NewURLFileURLWithPath(absKernelPath)
	if kernelURL.ID == 0 {
		return vz.VZLinuxBootLoader{}, fmt.Errorf("failed to create kernel URL")
	}
	fmt.Printf("  Kernel URL: %s\n", kernelURL.AbsoluteString())

	bootloader := vz.NewLinuxBootLoaderWithKernelURL(kernelURL)
	if bootloader.ID == 0 {
		return vz.VZLinuxBootLoader{}, fmt.Errorf("failed to create Linux boot loader")
	}

	// Set initrd if provided
	if initrdPath != "" {
		absInitrdPath, initErr := filepath.Abs(initrdPath)
		if initErr != nil {
			return vz.VZLinuxBootLoader{}, fmt.Errorf("resolve initrd path: %w", initErr)
		}
		if _, statErr := os.Stat(absInitrdPath); statErr != nil {
			return vz.VZLinuxBootLoader{}, fmt.Errorf("initrd not found: %s", absInitrdPath)
		}

		initrdURL := foundation.NewURLFileURLWithPath(absInitrdPath)
		if initrdURL.ID == 0 {
			return vz.VZLinuxBootLoader{}, fmt.Errorf("failed to create initrd URL")
		}
		bootloader.SetInitialRamdiskURL(initrdURL)
		fmt.Printf("  Initrd URL: %s\n", initrdURL.AbsoluteString())
	}

	// Set command line if provided
	if cmdLine != "" {
		bootloader.SetCommandLine(cmdLine)
		fmt.Printf("  Cmdline: %s\n", cmdLine)
	} else {
		// Default command line:
		// - console=tty0: graphical framebuffer output
		// - console=hvc0: virtio serial console (for headless mode)
		// - root=/dev/vda: root filesystem on first virtio block device
		// Both consoles are always enabled so output works in GUI and headless modes
		defaultCmdLine := "console=tty0 console=hvc0 root=/dev/vda"
		bootloader.SetCommandLine(defaultCmdLine)
		fmt.Printf("  Cmdline: %s (default)\n", defaultCmdLine)
	}

	return bootloader, nil
}

type installedLinuxBootArtifacts struct {
	kernel     string
	initrd     string
	rootUUID   string
	rootDevice string
}

func loadInstalledLinuxBootArtifacts(vmDir string) (installedLinuxBootArtifacts, bool) {
	kernel := filepath.Join(vmDir, "vmlinuz")
	initrd := filepath.Join(vmDir, "initrd")
	rootUUIDPath := filepath.Join(vmDir, linuxRootUUIDFileName)
	rootDevicePath := filepath.Join(vmDir, linuxRootDeviceFileName)

	for _, path := range []string{kernel, rootUUIDPath} {
		info, err := os.Stat(path)
		if err != nil || info.Size() == 0 {
			return installedLinuxBootArtifacts{}, false
		}
	}
	if info, err := os.Stat(initrd); err != nil || info.Size() == 0 {
		initrd = ""
	}

	rootUUID, err := os.ReadFile(rootUUIDPath)
	if err != nil {
		return installedLinuxBootArtifacts{}, false
	}
	rootUUIDValue := strings.TrimSpace(string(rootUUID))
	if rootUUIDValue == "" {
		return installedLinuxBootArtifacts{}, false
	}

	rootDevice := ""
	if data, err := os.ReadFile(rootDevicePath); err == nil {
		rootDevice = strings.TrimSpace(string(data))
	}

	return installedLinuxBootArtifacts{
		kernel:     kernel,
		initrd:     initrd,
		rootUUID:   rootUUIDValue,
		rootDevice: rootDevice,
	}, true
}

func hasInstalledLinuxBootArtifacts(vmDir string) bool {
	_, ok := loadInstalledLinuxBootArtifacts(vmDir)
	return ok
}

func (a installedLinuxBootArtifacts) commandLine() string {
	if a.rootDevice != "" {
		return fmt.Sprintf("console=tty0 console=hvc0 root=%s rootfstype=ext4 rw", a.rootDevice)
	}
	return fmt.Sprintf("console=tty0 console=hvc0 root=UUID=%s", a.rootUUID)
}

// createEFIBootLoader creates a VZEFIBootLoader with variable store.
func createEFIBootLoader() (vz.VZEFIBootLoader, error) {
	efiStorePath := filepath.Join(vmDir, "efi.nvram")
	bootloader, created, err := platformx.CreateEFIBootLoader(efiStorePath)
	if err != nil {
		return bootloader, err
	}
	if created {
		fmt.Println("  Creating EFI variable store...")
	} else {
		fmt.Println("  Loading existing EFI variable store...")
	}
	if windowsMode && strings.TrimSpace(windowsEFIRomPath) != "" {
		romPath := resolvePath(windowsEFIRomPath)
		if _, err := os.Stat(romPath); err != nil {
			return bootloader, fmt.Errorf("windows EFI ROM not found: %w", err)
		}
		romURL := foundation.NewURLFileURLWithPath(romPath)
		if romURL.ID == 0 {
			return bootloader, fmt.Errorf("create windows EFI ROM url")
		}
		romURL.Retain()
		privvz.VZEFIBootLoaderFromID(bootloader.ID).SetROMImageURL(romURL)
		fmt.Printf("  Windows EFI ROM: %s\n", romPath)
	}

	return bootloader, nil
}

// loadOrCreateGenericMachineIdentifier loads an existing generic machine identifier or creates a new one.
func loadOrCreateGenericMachineIdentifier() vz.VZGenericMachineIdentifier {
	machineIDPath := filepath.Join(vmDir, "linux-machine.id")
	machineID, created, err := platformx.LoadOrCreateGenericMachineIdentifier(machineIDPath)
	if err != nil {
		fmt.Printf("  warning: could not save machine identifier: %v\n", err)
	}
	if created {
		fmt.Println("  Created new machine identifier")
	} else {
		fmt.Println("  Loaded existing machine identifier")
	}
	return machineID
}

// runLinuxVM runs a Linux VM with the configured settings.
func runLinuxVMWithConfig(rc vmrun.RunConfig, hc vmrun.HostConfig, bundle *RunBundle, metrics runMetricRecorder) error {
	fmt.Println("=== Linux VM Runner ===")

	// Validate settings
	if err := validateVMSettings(); err != nil {
		return err
	}

	// Persist CPU/memory config for subsequent boots
	saveHardwareConfig(hc.VMDir)

	// Ensure VM directory exists
	if err := os.MkdirAll(hc.VMDir, 0755); err != nil {
		return fmt.Errorf("create VM directory: %w", err)
	}

	// For EFI boot: only attach ISO if the VM hasn't been installed yet
	// (no efi.nvram) or if the user explicitly provided an ISO.
	if rc.KernelPath == "" {
		if _, ok := loadInstalledLinuxBootArtifacts(hc.VMDir); ok {
			rc.ResolveISO("")
		} else {
			efiStorePath := filepath.Join(hc.VMDir, "efi.nvram")
			isoExplicit := false
			flag.Visit(func(f *flag.Flag) {
				if f.Name == "iso" {
					isoExplicit = true
				}
			})
			if isoExplicit || rc.ISOPath != "" {
				// User explicitly wants an ISO — resolve it
				resolvedISO, err := ensureLinuxISO()
				if err != nil {
					return fmt.Errorf("ensure ISO: %w", err)
				}
				rc.ResolveISO(resolvedISO)
			} else if _, err := os.Stat(efiStorePath); os.IsNotExist(err) {
				if _, markerErr := os.Stat(linuxInstalledMarkerPath(hc.VMDir)); markerErr == nil {
					// The unattended installer completed previously. Create an EFI
					// variable store on first real boot, but do not reattach
					// installation media.
				} else {
					// No EFI store yet — first boot, need the ISO
					resolvedISO, err := ensureLinuxISO()
					if err != nil {
						return fmt.Errorf("ensure ISO: %w", err)
					}
					rc.ResolveISO(resolvedISO)
				}
			} else if _, markerErr := os.Stat(linuxInstalledMarkerPath(hc.VMDir)); os.IsNotExist(markerErr) {
				fmt.Println("warning: Linux VM has EFI state but no installed-Linux boot marker")
				fmt.Println("  missing direct-boot artifacts may cause the guest to stop immediately")
				fmt.Println("  reinstall with 'cove install -linux' or use a VM with linux-installed, vmlinuz, and linux-root-uuid.txt")
				fmt.Println("  initrd is optional; linux-root-device.txt may override the root= kernel argument")
			}
		}
		// else: efi.nvram exists, boot from disk — no ISO needed
	}

	// Resolve disk path
	resolvedDiskPath := rc.DiskPath
	if resolvedDiskPath == "" {
		resolvedDiskPath = filepath.Join(hc.VMDir, "linux-disk.img")
	}
	if err := checkIncompletePullDisk(hc.VMDir, resolvedDiskPath); err != nil {
		return err
	}

	// Create disk if it doesn't exist
	if _, err := os.Stat(resolvedDiskPath); os.IsNotExist(err) {
		fmt.Printf("Creating disk image: %s (%d GB)\n", resolvedDiskPath, rc.DiskSizeGB)
		if err := createDiskImage(resolvedDiskPath, rc.DiskSizeGB); err != nil {
			return fmt.Errorf("create disk image: %w", err)
		}
	}

	// Build VM configuration
	fmt.Printf("Configuring VM: %d CPUs, %d GB RAM\n", rc.CPUCount, rc.MemoryGB)
	config, err := buildLinuxVMConfigurationWithConfig(rc, hc, resolvedDiskPath)
	if err != nil {
		return fmt.Errorf("build configuration: %w", err)
	}
	config.Retain()

	// Validate configuration
	fmt.Println("Validating configuration...")
	// Validate
	if _, err := config.ValidateWithError(); err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}
	fmt.Println("  ✓ Configuration valid")
	updateSaveRestoreSupport(config)

	// Note: Avoid calling getter methods on config as they may crash due to selector issues
	fmt.Printf("  Configured: %d CPUs, %d GB RAM\n", rc.CPUCount, rc.MemoryGB)

	// Create dispatch queue for VM operations
	vmQueue := dispatch.QueueCreate("com.appledocs.vz.linux.vmqueue")

	// Create VM with dispatch queue
	fmt.Println("Creating virtual machine...")
	vm := vz.NewVirtualMachineWithConfigurationQueue(&config, vmQueue)
	if vm.ID == 0 {
		return fmt.Errorf("failed to create virtual machine")
	}
	vm.Retain()

	// Start VM
	fmt.Println("Starting virtual machine...")
	return startVMWithQueueForRun(vm, vmQueue, bundle, metrics, rc, hc)
}

func createLinuxRootStorageDevice(path string, readOnly bool) (vz.VZStorageDeviceConfiguration, error) {
	attachment, err := createSystemDiskAttachment(path, readOnly)
	if err != nil {
		return vz.VZStorageDeviceConfiguration{}, fmt.Errorf("create disk attachment: %w", err)
	}
	return createLinuxStorageDeviceWithAttachment(attachment)
}

func createLinuxStorageDeviceWithAttachment(attachment vz.VZStorageDeviceAttachment) (vz.VZStorageDeviceConfiguration, error) {
	if linuxNVMe {
		device, err := storagex.CreateNVMeDeviceWithAttachment(attachment)
		if err != nil {
			return vz.VZStorageDeviceConfiguration{}, err
		}
		return vz.VZStorageDeviceConfigurationFromID(device.ID), nil
	}

	device, err := storagex.CreateBlockDeviceWithAttachment(attachment)
	if err != nil {
		return vz.VZStorageDeviceConfiguration{}, err
	}
	return vz.VZStorageDeviceConfigurationFromID(device.ID), nil
}

// Common Linux ISO URLs for ARM64
const (
	UbuntuServerARM64URL  = "https://cdimage.ubuntu.com/releases/24.04.3/release/ubuntu-24.04.3-live-server-arm64.iso"
	UbuntuDesktopARM64URL = "https://cdimage.ubuntu.com/releases/24.04.3/release/ubuntu-24.04.3-desktop-arm64.iso"
	DebianARM64URL        = "https://cdimage.debian.org/debian-cd/current/arm64/iso-cd/debian-13.5.0-arm64-netinst.iso"
	FedoraARM64URL        = "https://download.fedoraproject.org/pub/fedora/linux/releases/43/Server/aarch64/iso/Fedora-Server-netinst-aarch64-43-1.6.iso"
	AlpineARM64URL        = "https://dl-cdn.alpinelinux.org/alpine/latest-stable/releases/aarch64/alpine-virt-3.23.4-aarch64.iso"
)

type linuxISODescriptor struct {
	cacheName string
	url       string
	minSize   int64
	label     string
}

// downloadLinuxISO downloads a Linux ISO for installation with progress display.
func downloadLinuxISO(urlStr, path string, minSize int64) error {
	if info, err := os.Stat(path); err == nil {
		if info.Size() > minSize {
			fmt.Printf("Using existing ISO: %s (%.1f GB)\n", path, float64(info.Size())/(1024*1024*1024))
			return nil
		}
		fmt.Printf("Found partial download: %s (%.1f MB), resuming...\n", path, float64(info.Size())/(1024*1024))
	}

	fmt.Printf("Downloading Linux ISO to: %s\n", path)
	fmt.Printf("URL: %s\n", urlStr)
	fmt.Println("Download is resumable - Ctrl+C to pause, run again to continue.")
	fmt.Println()

	// Use curl with resume support and progress
	cmd := exec.Command("curl", "--fail", "-L", "-C", "-", "-#", "-o", path, urlStr)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("download failed: %w", err)
	}

	// Verify download
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("downloaded file not found: %w", err)
	}
	if info.Size() < minSize {
		return fmt.Errorf("downloaded file too small (%.1f MB), may be incomplete or error page", float64(info.Size())/(1024*1024))
	}

	fmt.Printf("✓ Download complete: %.1f GB\n", float64(info.Size())/(1024*1024*1024))
	return nil
}

// ensureLinuxISO ensures we have a Linux ISO, downloading if necessary.
// ISOs are cached in ~/.vz/cache/ so they survive VM deletion and can be
// shared across multiple Linux VMs. Desktop and Server variants use separate
// cache files (linux-desktop.iso and linux-server.iso).
func ensureLinuxISO() (string, error) {
	return ensureLinuxISOForVariant(currentLinuxVariant())
}

func ensureLinuxISOForVariant(variant LinuxVariant) (string, error) {
	desc, err := linuxISODescriptorForVariant(variant)
	if err != nil {
		return "", err
	}
	// If user specified an ISO path, use that directly
	if isoPath != "" {
		if isURL(isoPath) {
			cacheDir := vmconfig.CacheDir()
			if err := os.MkdirAll(cacheDir, 0755); err != nil {
				return "", fmt.Errorf("create cache dir: %w", err)
			}
			cacheFile := filepath.Join(cacheDir, desc.cacheName)
			if err := downloadLinuxISO(isoPath, cacheFile, desc.minSize); err != nil {
				return "", err
			}
			return cacheFile, nil
		}
		if _, err := os.Stat(isoPath); err != nil {
			return "", fmt.Errorf("iso file not found: %s", isoPath)
		}
		return isoPath, nil
	}

	cacheDir := vmconfig.CacheDir()
	cacheFile := filepath.Join(cacheDir, desc.cacheName)

	// Check variant-specific cache.
	if info, err := os.Stat(cacheFile); err == nil && info.Size() > desc.minSize && !linuxISODownloadInProgress(cacheFile) {
		fmt.Printf("Using cached ISO: %s (%.1f GB)\n", cacheFile, float64(info.Size())/(1024*1024*1024))
		return cacheFile, nil
	}

	// Check legacy cache file (backward compat) only if it matches the
	// requested variant. The historical linux.iso name did not encode
	// Server vs Desktop, so blindly reusing it can boot the wrong installer.
	legacyCache := filepath.Join(cacheDir, "linux.iso")
	if info, err := os.Stat(legacyCache); err == nil && info.Size() > desc.minSize && !linuxISODownloadInProgress(legacyCache) {
		if linuxISOMatchesVariant(legacyCache, variant) {
			fmt.Printf("Using cached ISO: %s (%.1f GB)\n", legacyCache, float64(info.Size())/(1024*1024*1024))
			return legacyCache, nil
		}
		fmt.Printf("Ignoring cached ISO: %s (does not match %s)\n", legacyCache, desc.label)
	}

	// Fall back to per-VM directory for existing installs
	legacyFile := filepath.Join(vmDir, "linux.iso")
	if info, err := os.Stat(legacyFile); err == nil && info.Size() > desc.minSize && !linuxISODownloadInProgress(legacyFile) {
		if linuxISOMatchesVariant(legacyFile, variant) {
			fmt.Printf("Using existing ISO: %s (%.1f GB)\n", legacyFile, float64(info.Size())/(1024*1024*1024))
			return legacyFile, nil
		}
		fmt.Printf("Ignoring existing ISO: %s (does not match %s)\n", legacyFile, desc.label)
	}

	// Download to variant-specific cache
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return "", fmt.Errorf("create cache dir: %w", err)
	}
	fmt.Printf("No ISO specified, downloading %s ARM64...\n", desc.label)
	if err := downloadLinuxISO(desc.url, cacheFile, desc.minSize); err != nil {
		return "", err
	}
	return cacheFile, nil
}

func linuxISODownloadInProgress(path string) bool {
	_, err := os.Stat(path + ".aria2")
	return err == nil
}

func linuxISODescriptorForVariant(variant LinuxVariant) (linuxISODescriptor, error) {
	switch variant {
	case LinuxVariantServer:
		return linuxISODescriptor{cacheName: "linux-ubuntu.iso", url: UbuntuServerARM64URL, minSize: 500 * 1024 * 1024, label: "Ubuntu Server 24.04"}, nil
	case LinuxVariantDesktop:
		return linuxISODescriptor{cacheName: "linux-ubuntu-desktop.iso", url: UbuntuDesktopARM64URL, minSize: 2 * 1024 * 1024 * 1024, label: "Ubuntu Desktop 24.04"}, nil
	case LinuxVariantDebian:
		return linuxISODescriptor{cacheName: "linux-debian.iso", url: DebianARM64URL, minSize: 300 * 1024 * 1024, label: "Debian 13"}, nil
	case LinuxVariantFedora:
		return linuxISODescriptor{cacheName: "linux-fedora.iso", url: FedoraARM64URL, minSize: 500 * 1024 * 1024, label: "Fedora Server 43"}, nil
	case LinuxVariantAlpine:
		return linuxISODescriptor{cacheName: "linux-alpine.iso", url: AlpineARM64URL, minSize: 30 * 1024 * 1024, label: "Alpine 3.23 virt"}, nil
	case LinuxVariantNixOS:
		return linuxISODescriptor{cacheName: "nixos-25.11-aarch64-linux.iso", url: "https://channels.nixos.org/nixos-25.11/latest-nixos-minimal-aarch64-linux.iso", minSize: 1200 * 1024 * 1024, label: "NixOS 25.11 minimal"}, nil
	default:
		return linuxISODescriptor{}, fmt.Errorf("unsupported linux distro %q", variant)
	}
}

func linuxISOMatchesVariant(path string, want LinuxVariant) bool {
	out, err := exec.Command("bsdtar", "-xOf", path, ".disk/info").Output()
	if err != nil {
		return false
	}
	info := strings.ToLower(string(out))
	switch want {
	case LinuxVariantDesktop:
		return strings.Contains(info, "ubuntu") && strings.Contains(info, "desktop")
	case LinuxVariantServer:
		return strings.Contains(info, "ubuntu") && strings.Contains(info, "server")
	case LinuxVariantDebian:
		return strings.Contains(info, "debian")
	case LinuxVariantFedora:
		return strings.Contains(info, "fedora")
	case LinuxVariantAlpine:
		return strings.Contains(info, "alpine")
	case LinuxVariantNixOS:
		return strings.Contains(info, "nixos")
	default:
		return false
	}
}

// isURL checks if a string looks like a URL.
func isURL(s string) bool {
	return len(s) > 8 && (s[:7] == "http://" || s[:8] == "https://")
}

func addVirtioSocketDevice(config vz.VZVirtualMachineConfiguration) {
	if !sandboxAllowsVsock() {
		return
	}
	vsockConfig := vz.NewVZVirtioSocketDeviceConfiguration()
	if vsockConfig.ID != 0 {
		configx.SetSocketDevices(config, vsockConfig)
	}
}
