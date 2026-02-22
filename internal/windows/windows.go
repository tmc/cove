//go:build ignore

// Windows ARM64 VM support for vz-macos.
//
// STATUS: Blocked — Apple's Virtualization.framework cannot boot Windows (2026-02-08).
//
// The Windows Boot Manager (bootmgfw.efi) requires a linear framebuffer via EFI
// Graphics Output Protocol (GOP) with PixelBlueGreenRedReserved8BitPerColor format.
// Apple's built-in EFI firmware (VZEFIBootLoader) only provides PixelBltOnly GOP
// through VirtioGpuDxe, which Windows refuses. The firmware is baked into
// Virtualization.framework and cannot be replaced with OVMF.
//
// What works:
//   - UEFI Shell boots and displays text (Simple Text Output Protocol)
//   - All Windows installer files are accessible on FAT32 via USB mass storage
//   - UEFI Shell can chainload bootmgfw.efi — it runs but produces no output
//
// What doesn't:
//   - bootmgfw.efi hangs with black screen, 0 bytes written to disk
//   - No project (UTM, Tart, Code-Hex/vz) has booted Windows on Virtualization.framework
//   - UTM always uses QEMU (with ramfb device that provides linear framebuffer) for Windows
//
// TODO: Re-enable when Apple adds linear framebuffer GOP to VZEFIBootLoader,
// or when a custom GOP shim driver becomes available for ARM64 VirtIO GPU.
// Monitor: https://developer.apple.com/documentation/virtualization for updates.
//
// Uses VZGenericPlatformConfiguration + VZEFIBootLoader for non-macOS EFI guests.
package windows

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/tmc/appledocs/generated/dispatch"
	"github.com/tmc/appledocs/generated/foundation"
	vz "github.com/tmc/appledocs/generated/virtualization"
)

// buildWindowsVMConfiguration builds a VZVirtualMachineConfiguration for Windows.
func buildWindowsVMConfiguration(diskImagePath string) (vz.VZVirtualMachineConfiguration, error) {
	config := vz.NewVZVirtualMachineConfiguration()

	// CPU and memory
	config.SetCPUCount(cpuCount)
	config.SetMemorySize(memoryGB * 1024 * 1024 * 1024)

	// Platform configuration (generic, same as Linux)
	fmt.Println("Setting up Windows platform configuration...")
	platformConfig := vz.NewVZGenericPlatformConfiguration()

	machineID := loadOrCreateWindowsMachineIdentifier()
	platformConfig.SetMachineIdentifier(&machineID)
	config.SetPlatform(&platformConfig.VZPlatformConfiguration)

	// EFI boot loader (always EFI for Windows)
	fmt.Println("  Using EFI boot (VZEFIBootLoader)")
	bootloader, err := createEFIBootLoader()
	if err != nil {
		return config, err
	}
	config.SetBootLoader(&bootloader.VZBootLoader)

	// Storage - main disk (NVMe — Windows ARM64 EFI has built-in NVMe drivers)
	diskURL := foundation.FileURL(diskImagePath)
	diskAttachment, err := vz.NewDiskImageStorageDeviceAttachmentWithURLReadOnlyError(&diskURL, false)
	if err != nil {
		return config, fmt.Errorf("create disk attachment: %w", err)
	}
	diskAttachment.Retain()

	storageConfig := vz.NewNVMExpressControllerDeviceConfigurationWithAttachment(&diskAttachment.VZStorageDeviceAttachment)
	storageConfig.Retain()

	config.SetStorageDevices([]vz.VZStorageDeviceConfiguration{
		storageConfig.VZStorageDeviceConfiguration,
	})

	// Graphics - Virtio with multi-display support
	displayConfigs := []DisplayConfig(displays)
	if len(displayConfigs) == 0 {
		displayConfigs = []DisplayConfig{GetDefaultDisplayForVM(true)}
	}
	graphicsConfig, err := CreateVirtioGraphicsConfig(displayConfigs)
	if err != nil {
		return config, fmt.Errorf("create graphics config: %w", err)
	}
	setVirtioGraphicsDevices(config, graphicsConfig)

	// Network
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

	// Memory balloon device
	balloonConfig := vz.NewVZVirtioTraditionalMemoryBalloonDeviceConfiguration()
	if balloonConfig.ID != 0 {
		setMemoryBalloonDevices(config, balloonConfig)
	}

	// Virtio socket device (vsock)
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

	// Clipboard sharing (SPICE agent)
	if enableClipboard {
		clipboardDevice := createClipboardConfig()
		if clipboardDevice.ID != 0 {
			config.SetConsoleDevices([]vz.VZConsoleDeviceConfiguration{
				vz.VZConsoleDeviceConfigurationFromID(clipboardDevice.ID),
			})
			fmt.Println("  Clipboard sharing enabled (SPICE agent)")
		}
	}

	// Volume mounts (VirtioFS)
	effectiveVolumes := getEffectiveVolumes()
	if len(effectiveVolumes) > 0 {
		volumeConfigs, err := createVolumeConfigs(effectiveVolumes)
		if err != nil {
			fmt.Printf("Warning: volume config: %v\n", err)
		} else if len(volumeConfigs) > 0 {
			setDirectorySharingDevicesMulti(config, volumeConfigs)
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

// loadOrCreateWindowsMachineIdentifier loads or creates a machine identifier
// for a Windows VM, stored separately from the Linux machine ID.
func loadOrCreateWindowsMachineIdentifier() vz.VZGenericMachineIdentifier {
	machineIDPath := filepath.Join(vmDir, "windows-machine.id")

	if data, err := os.ReadFile(machineIDPath); err == nil && len(data) > 0 {
		nsData := createNSDataFromBytes(data)
		if nsData != 0 {
			nsDataObj := foundation.NSDataFrom(nsData)
			machineID := vz.NewGenericMachineIdentifierWithDataRepresentation(&nsDataObj)
			if machineID.ID != 0 {
				fmt.Println("  Loaded existing Windows machine identifier")
				return machineID
			}
		}
	}

	machineID := vz.NewVZGenericMachineIdentifier()
	fmt.Println("  Created new Windows machine identifier")

	if err := saveGenericMachineIdentifier(machineID, machineIDPath); err != nil {
		fmt.Printf("  Warning: could not save machine identifier: %v\n", err)
	}

	return machineID
}

// runWindowsVM runs a Windows VM with the configured settings.
func runWindowsVM() error {
	fmt.Println("=== Windows VM Runner ===")

	if err := validateVMSettings(); err != nil {
		return err
	}

	if err := os.MkdirAll(vmDir, 0755); err != nil {
		return fmt.Errorf("create VM directory: %w", err)
	}

	// Resolve disk path
	resolvedDiskPath := diskPath
	if resolvedDiskPath == "" {
		resolvedDiskPath = filepath.Join(vmDir, "windows-disk.img")
	}

	// Verify the disk exists
	if _, err := os.Stat(resolvedDiskPath); os.IsNotExist(err) {
		return fmt.Errorf("Windows disk image not found: %s\nRun 'vz-macos install -windows -iso <path>' first", resolvedDiskPath)
	}

	// Build VM configuration
	fmt.Printf("Configuring VM: %d CPUs, %d GB RAM\n", cpuCount, memoryGB)
	config, err := buildWindowsVMConfiguration(resolvedDiskPath)
	if err != nil {
		return fmt.Errorf("build configuration: %w", err)
	}
	config.Retain()

	// Validate
	fmt.Println("Validating configuration...")
	if _, err := config.ValidateWithError(); err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}
	fmt.Println("  Configuration valid")
	fmt.Printf("  Configured: %d CPUs, %d GB RAM\n", cpuCount, memoryGB)

	// Create dispatch queue
	vmQueue := dispatch.QueueCreate("com.appledocs.vz.windows.vmqueue")

	// Create VM
	fmt.Println("Creating virtual machine...")
	vm := vz.NewVirtualMachineWithConfigurationQueue(&config, vmQueue.Handle())
	if vm.ID == 0 {
		return fmt.Errorf("failed to create virtual machine")
	}
	vm.Retain()

	// Start VM
	fmt.Println("Starting virtual machine...")
	return startVMWithQueue(vm, vmQueue)
}
