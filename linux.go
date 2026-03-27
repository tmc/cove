// Linux VM support for vz-macos.
//
// Supports two boot modes:
// - Direct kernel boot: uses VZLinuxBootLoader with kernel + initrd
// - EFI boot: uses VZEFIBootLoader with ISO image for installation
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/tmc/apple/dispatch"
	"github.com/tmc/apple/foundation"
	vz "github.com/tmc/apple/virtualization"
)

// buildLinuxVMConfiguration builds a VZVirtualMachineConfiguration for Linux.
func buildLinuxVMConfiguration(diskImagePath string) (vz.VZVirtualMachineConfiguration, error) {
	config := vz.NewVZVirtualMachineConfiguration()

	// CPU and memory
	config.SetCPUCount(cpuCount)
	config.SetMemorySize(memoryGB * 1024 * 1024 * 1024)

	// Platform configuration (generic for Linux)
	fmt.Println("Setting up Linux platform configuration...")
	platformConfig := vz.NewVZGenericPlatformConfiguration()

	// Machine identifier (unique to this VM)
	machineID := loadOrCreateGenericMachineIdentifier()
	platformConfig.SetMachineIdentifier(&machineID)

	// Enable nested virtualization (KVM in guest) if supported (macOS 15+, M3+)
	if vz.GetVZGenericPlatformConfigurationClass().NestedVirtualizationSupported() {
		platformConfig.SetNestedVirtualizationEnabled(true)
		fmt.Println("  Nested virtualization enabled (KVM will be available in guest)")
	}

	config.SetPlatform(&platformConfig.VZPlatformConfiguration)

	// Boot loader - choose based on flags
	if kernelPath != "" {
		// Direct kernel boot
		fmt.Println("  Using direct kernel boot (VZLinuxBootLoader)")
		bootloader, err := createLinuxBootLoader()
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
	diskURL := foundation.NewURLFileURLWithPath(diskImagePath)
	// Create disk attachment
	diskAttachment, err := vz.NewDiskImageStorageDeviceAttachmentWithURLReadOnlyError(diskURL, false)
	if err != nil {
		return vz.VZVirtualMachineConfiguration{}, fmt.Errorf("failed to create disk attachment: %w", err)
	}
	diskAttachment.Retain()

	// Create block device custom config
	storageConfig := vz.NewVirtioBlockDeviceConfigurationWithAttachment(&diskAttachment.VZStorageDeviceAttachment)
	storageConfig.Retain()

	// If ISO is provided, add it as second storage device (read-only)
	if isoPath != "" {
		isoURL := foundation.NewURLFileURLWithPath(isoPath)
		isoAttachment, err := vz.NewDiskImageStorageDeviceAttachmentWithURLReadOnlyError(isoURL, true)
		if err != nil {
			return config, fmt.Errorf("failed to create ISO attachment: %w", err)
		}
		isoAttachment.Retain()

		isoStorage := vz.NewVirtioBlockDeviceConfigurationWithAttachment(&isoAttachment.VZStorageDeviceAttachment)

		// Add both storage devices
		setStorageDevicesMultiple(config, storageConfig, isoStorage)
	} else {
		setStorageDevices(config, storageConfig)
	}

	// Graphics - use Virtio for Linux with multi-display support
	displayConfigs := []DisplayConfig(displays)
	if len(displayConfigs) == 0 {
		displayConfigs = []DisplayConfig{GetDefaultDisplayForVM(true)}
	}
	graphicsConfig, err := CreateVirtioGraphicsConfig(displayConfigs)
	if err != nil {
		return config, fmt.Errorf("create graphics config: %w", err)
	}
	setVirtioGraphicsDevices(config, graphicsConfig)

	// Network configuration
	netConfig, err := ParseNetworkMode(networkMode)
	if err != nil {
		return config, fmt.Errorf("parse network mode: %w", err)
	}
	if netConfig.Mode != NetworkModeNone {
		networkDeviceConfig, err := CreateNetworkDeviceConfiguration(netConfig)
		if err != nil {
			return config, fmt.Errorf("create network device: %w", err)
		}
		setNetworkDevices(config, networkDeviceConfig)
	}

	// Keyboard
	keyboardConfig := vz.NewVZUSBKeyboardConfiguration()
	setKeyboards(config, keyboardConfig)

	// Pointing device
	pointingConfig := vz.NewVZUSBScreenCoordinatePointingDeviceConfiguration()
	setPointingDevices(config, []vz.IVZPointingDeviceConfiguration{pointingConfig})

	// Entropy device
	entropyConfig := vz.NewVZVirtioEntropyDeviceConfiguration()
	setEntropyDevices(config, entropyConfig)

	// Audio
	audioConfig := vz.NewVZVirtioSoundDeviceConfiguration()
	setAudioDevices(config, audioConfig)

	// Memory balloon device (allows guest memory management)
	balloonConfig := vz.NewVZVirtioTraditionalMemoryBalloonDeviceConfiguration()
	if balloonConfig.ID != 0 {
		setMemoryBalloonDevices(config, balloonConfig)
	}

	// Virtio socket device (vsock for host-guest communication)
	vsockConfig := vz.NewVZVirtioSocketDeviceConfiguration()
	if vsockConfig.ID != 0 {
		setSocketDevices(config, vsockConfig)
	}

	// Serial console
	serialConfig := createSerialConsoleConfig()
	if serialConfig.ID != 0 {
		setSerialPorts(config, serialConfig)
		fmt.Println("  Serial console attached (output to stdout)")
	}

	// Clipboard sharing (SPICE agent over Virtio console)
	if enableClipboard {
		clipboardDevice := createClipboardConfig()
		if clipboardDevice.ID != 0 {
			config.SetConsoleDevices([]vz.VZConsoleDeviceConfiguration{
				vz.VZConsoleDeviceConfigurationFromID(clipboardDevice.ID),
			})
			fmt.Println("  Clipboard sharing enabled (SPICE agent)")
		}
	}

	// Volume mounts (VirtioFS) - docker-style -v flag
	effectiveVolumes := getEffectiveVolumes()
	if len(effectiveVolumes) > 0 {
		volumeConfigs, err := createVolumeConfigs(effectiveVolumes)
		if err != nil {
			fmt.Printf("warning: volume config: %v\n", err)
		} else if len(volumeConfigs) > 0 {
			setDirectorySharingDevicesMulti(config, volumeConfigs)
		}
	}

	// Rosetta support for x86-64 binary translation
	if enableRosetta {
		if err := AddRosettaToLinuxVM(config, vmDir); err != nil {
			fmt.Printf("warning: Rosetta setup failed: %v\n", err)
		}
	}

	// USB storage devices
	if len(usbDevices) > 0 {
		if err := AddUSBStorageToConfig(config, usbDevices); err != nil {
			return config, fmt.Errorf("add USB storage: %w", err)
		}
	}

	return config, nil
}

// createLinuxBootLoader creates a VZLinuxBootLoader with kernel, initrd, and cmdline.
func createLinuxBootLoader() (vz.VZLinuxBootLoader, error) {
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

// createEFIBootLoader creates a VZEFIBootLoader with variable store.
func createEFIBootLoader() (vz.VZEFIBootLoader, error) {
	bootloader := vz.NewVZEFIBootLoader()
	if bootloader.ID == 0 {
		return bootloader, fmt.Errorf("failed to create EFI boot loader")
	}

	// Create or load EFI variable store
	efiStorePath := filepath.Join(vmDir, "efi.nvram")
	efiStoreURL := foundation.NewURLFileURLWithPath(efiStorePath)

	var efiStore vz.VZEFIVariableStore
	if _, err := os.Stat(efiStorePath); os.IsNotExist(err) {
		fmt.Println("  Creating EFI variable store...")
		var err error
		efiStore, err = vz.NewEFIVariableStoreCreatingVariableStoreAtURLOptionsError(
			efiStoreURL, vz.VZEFIVariableStoreInitializationOptionAllowOverwrite)
		if err != nil {
			return bootloader, fmt.Errorf("failed to create EFI variable store: %w", err)
		}
	} else {
		fmt.Println("  Loading existing EFI variable store...")
		efiStore = vz.NewEFIVariableStoreWithURL(efiStoreURL)
	}
	// efiStore must be retained as it's assigned to bootloader and might be autoreleased
	if efiStore.ID != 0 {
		efiStore.Retain()
		bootloader.SetVariableStore(&efiStore)
	}

	return bootloader, nil
}

// loadOrCreateGenericMachineIdentifier loads an existing generic machine identifier or creates a new one.
func loadOrCreateGenericMachineIdentifier() vz.VZGenericMachineIdentifier {
	machineIDPath := filepath.Join(vmDir, "linux-machine.id")

	// Check if we have a saved machine identifier
	if data, err := os.ReadFile(machineIDPath); err == nil && len(data) > 0 {
		nsData := createNSDataFromBytes(data)
		if nsData != 0 {
			nsDataObj := foundation.NSDataFromID(nsData)
			machineID := vz.NewGenericMachineIdentifierWithDataRepresentation(&nsDataObj)
			if machineID.ID != 0 {
				fmt.Println("  Loaded existing machine identifier")
				return machineID
			}
		}
	}

	// Create new machine identifier
	machineID := vz.NewVZGenericMachineIdentifier()
	fmt.Println("  Created new machine identifier")

	// Save for future use
	if err := saveGenericMachineIdentifier(machineID, machineIDPath); err != nil {
		fmt.Printf("  warning: could not save machine identifier: %v\n", err)
	}

	return machineID
}

// saveGenericMachineIdentifier saves the machine identifier data representation to a file.
func saveGenericMachineIdentifier(machineID vz.VZGenericMachineIdentifier, path string) error {
	data := machineID.DataRepresentation()
	if data.GetID() == 0 {
		return fmt.Errorf("machine identifier has no data representation")
	}
	return saveNSDataToFile(data.GetID(), path)
}

// runLinuxVM runs a Linux VM with the configured settings.
func runLinuxVM() error {
	fmt.Println("=== Linux VM Runner ===")

	// Validate settings
	if err := validateVMSettings(); err != nil {
		return err
	}

	// Persist CPU/memory config for subsequent boots
	saveHardwareConfig(vmDir)

	// Ensure VM directory exists
	if err := os.MkdirAll(vmDir, 0755); err != nil {
		return fmt.Errorf("create VM directory: %w", err)
	}

	// For EFI boot (no kernel specified), ensure we have an ISO
	if kernelPath == "" {
		resolvedISO, err := ensureLinuxISO()
		if err != nil {
			return fmt.Errorf("ensure ISO: %w", err)
		}
		isoPath = resolvedISO
	}

	// Resolve disk path
	resolvedDiskPath := diskPath
	if resolvedDiskPath == "" {
		resolvedDiskPath = filepath.Join(vmDir, "linux-disk.img")
	}

	// Create disk if it doesn't exist
	if _, err := os.Stat(resolvedDiskPath); os.IsNotExist(err) {
		fmt.Printf("Creating disk image: %s (%d GB)\n", resolvedDiskPath, diskSizeGB)
		if err := createDiskImage(resolvedDiskPath, diskSizeGB); err != nil {
			return fmt.Errorf("create disk image: %w", err)
		}
	}

	// Build VM configuration
	fmt.Printf("Configuring VM: %d CPUs, %d GB RAM\n", cpuCount, memoryGB)
	config, err := buildLinuxVMConfiguration(resolvedDiskPath)
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

	// Note: Avoid calling getter methods on config as they may crash due to selector issues
	fmt.Printf("  Configured: %d CPUs, %d GB RAM\n", cpuCount, memoryGB)

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
	return startVMWithQueue(vm, vmQueue)
}

// Helper functions for Linux-specific array properties using generated slice setters.
func setStorageDevicesMultiple(config vz.VZVirtualMachineConfiguration, devices ...vz.VZVirtioBlockDeviceConfiguration) {
	storageDevices := make([]vz.VZStorageDeviceConfiguration, len(devices))
	for i, device := range devices {
		storageDevices[i] = vz.VZStorageDeviceConfigurationFromID(device.ID)
	}
	config.SetStorageDevices(storageDevices)
}

func setVirtioGraphicsDevices(config vz.VZVirtualMachineConfiguration, device vz.VZVirtioGraphicsDeviceConfiguration) {
	config.SetGraphicsDevices([]vz.VZGraphicsDeviceConfiguration{
		vz.VZGraphicsDeviceConfigurationFromID(device.ID),
	})
}

func setVirtioScanouts(config vz.VZVirtioGraphicsDeviceConfiguration, scanout vz.VZVirtioGraphicsScanoutConfiguration) {
	config.SetScanouts([]vz.VZVirtioGraphicsScanoutConfiguration{scanout})
}

// Common Linux ISO URLs for ARM64
const (
	UbuntuServerARM64URL  = "https://cdimage.ubuntu.com/releases/24.04.3/release/ubuntu-24.04.3-live-server-arm64.iso"
	UbuntuDesktopARM64URL = "https://cdimage.ubuntu.com/releases/24.04.3/release/ubuntu-24.04.3-desktop-arm64.iso"
)

// downloadLinuxISO downloads a Linux ISO for installation with progress display.
func downloadLinuxISO(urlStr, path string) error {
	// Check if file already exists and has reasonable size (> 500MB)
	if info, err := os.Stat(path); err == nil {
		if info.Size() > 500*1024*1024 {
			fmt.Printf("Using existing ISO: %s (%.1f GB)\n", path, float64(info.Size())/(1024*1024*1024))
			return nil
		}
		// Partial download exists - curl will resume
		fmt.Printf("Found partial download: %s (%.1f MB), resuming...\n", path, float64(info.Size())/(1024*1024))
	}

	fmt.Printf("Downloading Linux ISO to: %s\n", path)
	fmt.Printf("URL: %s\n", urlStr)
	fmt.Println("Download is resumable - Ctrl+C to pause, run again to continue.")
	fmt.Println()

	// Use curl with resume support and progress
	cmd := exec.Command("curl", "-L", "-C", "-", "-#", "-o", path, urlStr)
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
	if info.Size() < 500*1024*1024 {
		return fmt.Errorf("downloaded file too small (%.1f MB), may be incomplete or error page", float64(info.Size())/(1024*1024))
	}

	fmt.Printf("✓ Download complete: %.1f GB\n", float64(info.Size())/(1024*1024*1024))
	return nil
}

// ensureLinuxISO ensures we have a Linux ISO, downloading if necessary.
// ISOs are cached in ~/.vz/cache/ so they survive VM deletion and can be
// shared across multiple Linux VMs.
func ensureLinuxISO() (string, error) {
	// If user specified an ISO path, use that directly
	if isoPath != "" {
		if isURL(isoPath) {
			cacheDir := GetCacheDir()
			if err := os.MkdirAll(cacheDir, 0755); err != nil {
				return "", fmt.Errorf("create cache dir: %w", err)
			}
			cacheFile := filepath.Join(cacheDir, "linux.iso")
			if err := downloadLinuxISO(isoPath, cacheFile); err != nil {
				return "", err
			}
			return cacheFile, nil
		}
		if _, err := os.Stat(isoPath); err != nil {
			return "", fmt.Errorf("iso file not found: %s", isoPath)
		}
		return isoPath, nil
	}

	// Check shared cache first (~/.vz/cache/linux.iso)
	cacheDir := GetCacheDir()
	cacheFile := filepath.Join(cacheDir, "linux.iso")
	if info, err := os.Stat(cacheFile); err == nil && info.Size() > 500*1024*1024 {
		fmt.Printf("Using cached ISO: %s (%.1f GB)\n", cacheFile, float64(info.Size())/(1024*1024*1024))
		return cacheFile, nil
	}

	// Fall back to per-VM directory for existing installs
	legacyFile := filepath.Join(vmDir, "linux.iso")
	if info, err := os.Stat(legacyFile); err == nil && info.Size() > 500*1024*1024 {
		fmt.Printf("Using existing ISO: %s (%.1f GB)\n", legacyFile, float64(info.Size())/(1024*1024*1024))
		return legacyFile, nil
	}

	// Download to shared cache
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return "", fmt.Errorf("create cache dir: %w", err)
	}
	fmt.Println("No ISO specified, downloading Ubuntu Server 24.04 ARM64...")
	if err := downloadLinuxISO(UbuntuServerARM64URL, cacheFile); err != nil {
		return "", err
	}
	return cacheFile, nil
}

// isURL checks if a string looks like a URL.
func isURL(s string) bool {
	return len(s) > 8 && (s[:7] == "http://" || s[:8] == "https://")
}

// Helper functions for Linux-specific device configuration

func setMemoryBalloonDevices(config vz.VZVirtualMachineConfiguration, device vz.VZVirtioTraditionalMemoryBalloonDeviceConfiguration) {
	config.SetMemoryBalloonDevices([]vz.VZMemoryBalloonDeviceConfiguration{
		vz.VZMemoryBalloonDeviceConfigurationFromID(device.ID),
	})
}

func setSocketDevices(config vz.VZVirtualMachineConfiguration, device vz.VZVirtioSocketDeviceConfiguration) {
	config.SetSocketDevices([]vz.VZSocketDeviceConfiguration{
		vz.VZSocketDeviceConfigurationFromID(device.ID),
	})
}
