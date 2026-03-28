package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/ebitengine/purego"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/dispatch"
	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/objectivec"
	vz "github.com/tmc/apple/virtualization"
	"github.com/tmc/apple/x/vzkit"
	"github.com/tmc/vz-macos/internal/assets"
)

// setAppIcon sets the Dock and app icon from the embedded .icns asset.
func setAppIcon(app *appkit.NSApplication) {
	iconData := assets.Icon
	nsData := foundation.NewDataWithBytesLength(iconData)
	img := appkit.NewImageWithData(&nsData)
	if img.ID != 0 {
		app.SetApplicationIconImage(&img)
	}
}

// suspendStatePath returns the path to the automatic suspend state file.
func suspendStatePath() string {
	return filepath.Join(vmDir, "suspend.vmstate")
}

// suspendConfigPath returns the path to the saved config fingerprint.
func suspendConfigPath() string {
	return filepath.Join(vmDir, "suspend.config.json")
}

// hasSuspendState checks if a suspend state file exists from a previous session.
func hasSuspendState() bool {
	_, err := os.Stat(suspendStatePath())
	return err == nil
}

// suspendConfigFingerprint captures the VM config params that must match between save and restore.
// If any of these change, restoreMachineStateFromURL will fail with "invalid argument".
type suspendConfigFingerprint struct {
	CPUs       int    `json:"cpus"`
	MemoryGB   int    `json:"memoryGB"`
	Network    string `json:"network"`
	Displays   int    `json:"displays"`
	Volumes    int    `json:"volumes"`
	USBDevices int    `json:"usbDevices"`
	Clipboard  bool   `json:"clipboard"`
	Serial     bool   `json:"serial"`
}

func currentConfigFingerprint() suspendConfigFingerprint {
	return suspendConfigFingerprint{
		CPUs:       int(cpuCount),
		MemoryGB:   int(memoryGB),
		Network:    networkMode,
		Displays:   max(len(displays), 1),
		Volumes:    len(getEffectiveVolumes()),
		USBDevices: len(usbDevices),
		Clipboard:  enableClipboard,
		Serial:     serialOutput != "none",
	}
}

// saveSuspendConfig writes the current config fingerprint alongside the suspend state.
func saveSuspendConfig() {
	fp := currentConfigFingerprint()
	data, _ := json.MarshalIndent(fp, "", "  ")
	if err := os.WriteFile(suspendConfigPath(), append(data, '\n'), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "warning: save suspend config: %v\n", err)
	}
}

// checkSuspendConfigMatch compares the saved config fingerprint with the current one.
// Returns nil if they match or no saved config exists. Returns a descriptive error if mismatched.
func checkSuspendConfigMatch() error {
	data, err := os.ReadFile(suspendConfigPath())
	if err != nil {
		return nil // No saved config, skip check
	}
	var saved suspendConfigFingerprint
	if err := json.Unmarshal(data, &saved); err != nil {
		return nil // Corrupt, skip check
	}
	current := currentConfigFingerprint()
	var diffs []string
	if saved.CPUs != current.CPUs {
		diffs = append(diffs, fmt.Sprintf("CPUs: %d -> %d", saved.CPUs, current.CPUs))
	}
	if saved.MemoryGB != current.MemoryGB {
		diffs = append(diffs, fmt.Sprintf("memory: %dGB -> %dGB", saved.MemoryGB, current.MemoryGB))
	}
	if saved.Network != current.Network {
		diffs = append(diffs, fmt.Sprintf("network: %s -> %s", saved.Network, current.Network))
	}
	if saved.Displays != current.Displays {
		diffs = append(diffs, fmt.Sprintf("displays: %d -> %d", saved.Displays, current.Displays))
	}
	if saved.Volumes != current.Volumes {
		diffs = append(diffs, fmt.Sprintf("volumes: %d -> %d", saved.Volumes, current.Volumes))
	}
	if saved.USBDevices != current.USBDevices {
		diffs = append(diffs, fmt.Sprintf("USB devices: %d -> %d", saved.USBDevices, current.USBDevices))
	}
	if saved.Clipboard != current.Clipboard {
		diffs = append(diffs, fmt.Sprintf("clipboard: %v -> %v", saved.Clipboard, current.Clipboard))
	}
	if saved.Serial != current.Serial {
		diffs = append(diffs, fmt.Sprintf("serial: %v -> %v", saved.Serial, current.Serial))
	}
	if len(diffs) > 0 {
		return fmt.Errorf("vm config changed since suspend (%s); delete %s to cold boot",
			strings.Join(diffs, ", "), suspendStatePath())
	}
	return nil
}

// canSaveRestore tracks whether the VM configuration supports save/restore.
var canSaveRestore bool

// utmAuxStoragePath overrides the default aux.img path when loading a UTM bundle.
// Set by runUTMBundle to point at the UTM bundle's AuxiliaryStorage file.
var utmAuxStoragePath string

// appFinishedLaunching guards against calling FinishLaunching more than once.
var appFinishedLaunching bool

// Default VM window dimensions.
const (
	defaultWindowWidth  = 1024
	defaultWindowHeight = 768
)

// runMacOSVM runs a macOS VM with the configured settings.
func runMacOSVM() error {
	fmt.Println("=== macOS VM Runner ===")
	preferPasswordDialog = guiMode && !headlessMode

	stopAppleLogStream := maybeStartAppleLogStream()
	defer stopAppleLogStream()

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

	// Resolve disk path
	resolvedDiskPath := diskPath
	if resolvedDiskPath == "" {
		resolvedDiskPath = filepath.Join(vmDir, "disk.img")
	}

	_, diskStatErr := os.Stat(resolvedDiskPath)
	diskExists := diskStatErr == nil

	// Create disk if it doesn't exist
	if os.IsNotExist(diskStatErr) {
		fmt.Printf("Creating disk image: %s (%d GB)\n", resolvedDiskPath, diskSizeGB)
		if err := createDiskImage(resolvedDiskPath, diskSizeGB); err != nil {
			return fmt.Errorf("create disk image: %w", err)
		}
	} else if diskStatErr != nil {
		return fmt.Errorf("stat disk image: %w", diskStatErr)
	}

	// Refuse to auto-repair identity metadata for an existing disk.
	// Recreating hw.model or machine.id for a pre-existing macOS disk can
	// produce an identity mismatch and undefined boot behavior (often black
	// screen or immediate boot failure).
	if diskExists {
		if err := validateExistingMacOSIdentityMetadata(); err != nil {
			return err
		}
	}

	// Pre-flight: check if another vz-macos process is already using this VM.
	// A stale control socket or running process can cause "storage device
	// attachment is invalid" when the VZ framework tries to open the disk.
	sock := GetControlSocketPath()
	if conn, err := net.DialTimeout("unix", sock, 500*time.Millisecond); err == nil {
		conn.Close()
		return fmt.Errorf("another vz-macos process is already running this VM (control socket active at %s)\nStop it first, or use a different -vm name", sock)
	}
	// Clean up stale socket file from a crashed process.
	os.Remove(sock)

	// Pre-flight: ensure disk is not still attached from a previous
	// inject/verify. The VZ framework cannot open a disk that is already
	// held by hdiutil.
	if err := ensureDiskDetached(resolvedDiskPath); err != nil {
		return fmt.Errorf("disk busy: %w", err)
	}

	// Build VM configuration
	fmt.Printf("Configuring VM: %d CPUs, %d GB RAM\n", cpuCount, memoryGB)
	config, err := buildVMConfiguration(resolvedDiskPath)
	if err != nil {
		return fmt.Errorf("build configuration: %w", err)
	}
	config.Retain()

	// Check if save/restore is supported for this configuration
	if ok, err := config.ValidateSaveRestoreSupportWithError(); ok {
		canSaveRestore = true
		if verbose {
			fmt.Println("  Save/restore support: enabled")
		}
	} else {
		canSaveRestore = false
		if verbose {
			reason := "unknown"
			if err != nil {
				reason = err.Error()
			}
			fmt.Printf("  Save/restore support: disabled (%s)\n", reason)
		}
	}

	// Create dispatch queue for VM operations
	vmQueue := dispatch.QueueCreate("com.appledocs.vz.vmqueue")

	// Create VM with dispatch queue
	vm := vz.NewVirtualMachineWithConfigurationQueue(&config, vmQueue)
	if vm.ID == 0 {
		return fmt.Errorf("failed to create virtual machine")
	}
	vm.Retain()

	// Start VM - delegate to startVMWithQueue for proper handling
	return startVMWithQueue(vm, vmQueue)
}

func validateExistingMacOSIdentityMetadata() error {
	missing, err := missingMacOSIdentityMetadata()
	if err != nil {
		return err
	}
	if len(missing) == 0 {
		return nil
	}

	if !recoverIdentity {
		return fmt.Errorf("existing VM disk found, but required identity metadata is missing (%s); refusing to launch. restore these files from backup, or retry with -recover-identity to attempt a metadata reset", strings.Join(missing, ", "))
	}

	backupDir, err := recoverIdentityMetadata(missing)
	if err != nil {
		return err
	}
	fmt.Printf("Recovery mode enabled: missing identity metadata (%s)\n", strings.Join(missing, ", "))
	fmt.Printf("  Backed up existing identity files to: %s\n", backupDir)
	fmt.Println("  Reset identity metadata (hw.model, machine.id, aux.img); regenerating on launch")
	return nil
}

func missingMacOSIdentityMetadata() ([]string, error) {
	required := []string{
		filepath.Join(vmDir, "aux.img"),
		filepath.Join(vmDir, "hw.model"),
		filepath.Join(vmDir, "machine.id"),
	}
	var missing []string
	for _, path := range required {
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				missing = append(missing, filepath.Base(path))
				continue
			}
			return nil, fmt.Errorf("stat %s: %w", path, err)
		}
		if info.Size() == 0 {
			missing = append(missing, filepath.Base(path))
		}
	}
	return missing, nil
}

func recoverIdentityMetadata(missing []string) (string, error) {
	backupDir := filepath.Join(vmDir, "recovery", "identity-reset-"+time.Now().Format("20060102-150405"))
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return "", fmt.Errorf("create recovery backup dir: %w", err)
	}

	backupCandidates := []string{
		"aux.img",
		"hw.model",
		"machine.id",
		"mac.address",
	}
	for _, name := range backupCandidates {
		src := filepath.Join(vmDir, name)
		if _, err := os.Stat(src); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return "", fmt.Errorf("stat %s: %w", src, err)
		}
		dst := filepath.Join(backupDir, name)
		if err := copyFile(src, dst); err != nil {
			return "", fmt.Errorf("backup %s: %w", name, err)
		}
	}

	resetFiles := []string{
		filepath.Join(vmDir, "aux.img"),
		filepath.Join(vmDir, "hw.model"),
		filepath.Join(vmDir, "machine.id"),
	}
	for _, path := range resetFiles {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return "", fmt.Errorf("reset %s: %w", path, err)
		}
	}

	if len(missing) > 0 {
		notePath := filepath.Join(backupDir, "RECOVERY_NOTE.txt")
		note := fmt.Sprintf("Recovery triggered at %s\nMissing metadata detected: %s\nReset files: aux.img, hw.model, machine.id\n",
			time.Now().Format(time.RFC3339), strings.Join(missing, ", "))
		_ = os.WriteFile(notePath, []byte(note), 0644)
	}

	return backupDir, nil
}

// validateVMSettings validates the VM configuration settings.
func validateVMSettings() error {
	configClass := vz.GetVZVirtualMachineConfigurationClass()
	minCPU := configClass.MinimumAllowedCPUCount()
	maxCPU := configClass.MaximumAllowedCPUCount()
	minMem := configClass.MinimumAllowedMemorySize() / (1024 * 1024 * 1024)
	maxMem := configClass.MaximumAllowedMemorySize() / (1024 * 1024 * 1024)

	if cpuCount < uint(minCPU) || cpuCount > uint(maxCPU) {
		return fmt.Errorf("cpu count must be between %d and %d", minCPU, maxCPU)
	}
	if memoryGB < minMem || memoryGB > maxMem {
		return fmt.Errorf("memory must be between %d GB and %d GB", minMem, maxMem)
	}
	return nil
}

// buildVMConfiguration builds a VZVirtualMachineConfiguration for macOS.
func buildVMConfiguration(diskImagePath string) (vz.VZVirtualMachineConfiguration, error) {
	// Resolve symlinks for all paths
	diskImagePath = resolvePath(diskImagePath)

	config := vz.NewVZVirtualMachineConfiguration()

	// CPU and memory
	config.SetCPUCount(cpuCount)
	config.SetMemorySize(memoryGB * 1024 * 1024 * 1024)

	// Platform configuration (macOS)
	platformConfig := vz.NewVZMacPlatformConfiguration()

	// Machine identifier (unique to this VM)
	machineID := loadOrCreateMachineIdentifier()
	platformConfig.SetMachineIdentifier(&machineID)

	// Hardware model from restore image's mostFeaturefulSupportedConfiguration
	hwModel, err := loadOrCreateHardwareModel()
	if err != nil {
		return config, fmt.Errorf("hardware model: %w", err)
	}
	platformConfig.SetHardwareModel(&hwModel)

	// Auxiliary storage (NVRAM, etc.)
	// Use UTM bundle's auxiliary storage path if set, otherwise default.
	auxStoragePath := filepath.Join(vmDir, "aux.img")
	if utmAuxStoragePath != "" {
		auxStoragePath = utmAuxStoragePath
	}
	auxURL := foundation.NewURLFileURLWithPath(auxStoragePath)
	auxURL.Retain() // Prevent premature deallocation
	var auxStorage vz.VZMacAuxiliaryStorage
	if _, statErr := os.Stat(auxStoragePath); os.IsNotExist(statErr) {
		if verbose {
			fmt.Println("  Creating auxiliary storage...")
		}
		var err error
		auxStorage, err = vz.NewMacAuxiliaryStorageCreatingStorageAtURLHardwareModelOptionsError(
			auxURL, hwModel, vz.VZMacAuxiliaryStorageInitializationOptionAllowOverwrite)
		if err != nil {
			return config, fmt.Errorf("failed to create auxiliary storage: %w", err)
		}
		auxStorage.Retain()
	} else {
		if verbose {
			fmt.Println("  Loading existing auxiliary storage...")
		}
		auxStorage = vz.NewMacAuxiliaryStorageWithContentsOfURL(auxURL)
		auxStorage.Retain() // Prevent premature deallocation
	}
	if auxStorage.ID != 0 {
		platformConfig.SetAuxiliaryStorage(&auxStorage)
	}
	config.SetPlatform(&platformConfig.VZPlatformConfiguration)

	// Boot loader
	bootloader := vz.NewVZMacOSBootLoader()
	config.SetBootLoader(&bootloader.VZBootLoader)

	// Storage
	diskURL := foundation.NewURLFileURLWithPath(diskImagePath)
	diskURL.Retain() // Prevent premature deallocation
	// Create disk attachment
	diskAttachment, err := vz.NewDiskImageStorageDeviceAttachmentWithURLReadOnlyError(diskURL, false)
	if err != nil {
		return config, fmt.Errorf("failed to create disk attachment: %w", err)
	}
	diskAttachment.Retain()

	// Create block device custom config
	storageConfig := vz.NewVirtioBlockDeviceConfigurationWithAttachment(&diskAttachment.VZStorageDeviceAttachment)
	storageConfig.Retain()
	setStorageDevices(config, storageConfig)

	// Graphics with multi-display support
	displayConfigs := []DisplayConfig(displays)
	if len(displayConfigs) == 0 {
		displayConfigs = []DisplayConfig{DefaultDisplayConfig()}
	}
	graphicsConfig, err := CreateMacGraphicsConfig(displayConfigs)
	if err != nil {
		return config, fmt.Errorf("create graphics config: %w", err)
	}
	setGraphicsDevices(config, graphicsConfig)

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

		macAddr := loadOrCreateMACAddress()
		if macAddr.ID != 0 {
			networkDeviceConfig.SetMACAddress(&macAddr)
		}

		setNetworkDevices(config, networkDeviceConfig)
	}

	// Keyboard
	// Try VZMacKeyboardConfiguration first when available
	var keyboardConfig vz.IVZKeyboardConfiguration
	if macKeyboard := vz.NewVZMacKeyboardConfiguration(); macKeyboard.GetID() != 0 {
		keyboardConfig = macKeyboard
	} else {
		keyboardConfig = vz.NewVZUSBKeyboardConfiguration()
	}
	setKeyboards(config, keyboardConfig)

	// Pointing device
	pointingDevices := []vz.IVZPointingDeviceConfiguration{
		vz.NewVZUSBScreenCoordinatePointingDeviceConfiguration(),
	}
	if trackpad := vz.NewVZMacTrackpadConfiguration(); trackpad.GetID() != 0 {
		pointingDevices = append(pointingDevices, trackpad)
	}
	setPointingDevices(config, pointingDevices)

	// Entropy device
	entropyConfig := vz.NewVZVirtioEntropyDeviceConfiguration()
	setEntropyDevices(config, entropyConfig)

	// Audio with host input/output streams
	audioConfig, err := createAudioDeviceConfiguration()
	if err != nil {
		fmt.Printf("warning: audio config: %v\n", err)
	} else {
		setAudioDevices(config, audioConfig)
	}

	minimalProfile := runtimeProfile == "minimal"

	if !minimalProfile {
		// Memory balloon device for runtime memory control
		addMemoryBalloonDevice(config)

		// Virtio socket device (vsock for host-guest communication)
		vsockConfig := vz.NewVZVirtioSocketDeviceConfiguration()
		if vsockConfig.ID != 0 {
			setSocketDevices(config, vsockConfig)
		}
	}

	// USB storage devices
	if len(usbDevices) > 0 {
		if minimalProfile {
			fmt.Println("warning: ignoring -usb devices with -runtime-profile minimal")
		} else {
			if err := AddUSBStorageToConfig(config, usbDevices); err != nil {
				return config, fmt.Errorf("add USB storage: %w", err)
			}
		}
	}

	// Serial console (for streaming output to stdout/stderr)
	if minimalProfile {
		if serialOutput != "none" {
			fmt.Println("warning: ignoring -serial with -runtime-profile minimal")
		}
	} else {
		serialConfig := createSerialConsoleConfig()
		if serialConfig.ID != 0 {
			setSerialPorts(config, serialConfig)
			if verbose {
				fmt.Println("  Serial console attached (output to stdout)")
			}
		}
	}

	// Clipboard sharing (SPICE agent over Virtio console)
	if minimalProfile {
		if enableClipboard {
			fmt.Println("warning: ignoring -clipboard with -runtime-profile minimal")
		}
	} else if enableClipboard {
		clipboardDevice := createClipboardConfig()
		if clipboardDevice.ID != 0 {
			config.SetConsoleDevices([]vz.VZConsoleDeviceConfiguration{
				vz.VZConsoleDeviceConfigurationFromID(clipboardDevice.ID),
			})
			if verbose {
				fmt.Println("  Clipboard sharing enabled (SPICE agent)")
			}
		}
	}

	if !minimalProfile {
		// Volume mounts (VirtioFS) - docker-style -v flag
		var allVirtioConfigs []vz.VZVirtioFileSystemDeviceConfiguration

		effectiveVolumes := getEffectiveVolumes()
		if len(effectiveVolumes) > 0 {
			volumeConfigs, err := createVolumeConfigs(effectiveVolumes)
			if err != nil {
				fmt.Printf("warning: volume config: %v\n", err)
			} else {
				allVirtioConfigs = append(allVirtioConfigs, volumeConfigs...)
			}
		}

		// Dedicated VirtioFS device for toolbar-managed shared folders.
		// Always created so the GUI can hotplug folders at runtime.
		sharedFolders := LoadSharedFolders(vmDir)
		sharedFoldersDevice := createSharedFoldersDevice(sharedFolders)
		if sharedFoldersDevice.ID != 0 {
			allVirtioConfigs = append(allVirtioConfigs, sharedFoldersDevice)
		}

		// Apply all VirtioFS configurations
		if len(allVirtioConfigs) > 0 {
			setDirectorySharingDevicesMulti(config, allVirtioConfigs)
		}
	} else {
		effectiveVolumes := getEffectiveVolumes()
		if len(effectiveVolumes) > 0 || len(LoadSharedFolders(vmDir)) > 0 {
			fmt.Println("warning: ignoring shared folders/volumes with -runtime-profile minimal")
		}
	}

	return config, nil
}

// loadOrCreateMachineIdentifier loads an existing machine identifier or creates a new one.
func loadOrCreateMachineIdentifier() vz.VZMacMachineIdentifier {
	machineIDPath := filepath.Join(vmDir, "machine.id")

	// Check if we have a saved machine identifier
	if data, err := os.ReadFile(machineIDPath); err == nil && len(data) > 0 {
		nsData := createNSDataFromBytes(data)
		if nsData != 0 {
			nsDataObj := foundation.NSDataFromID(nsData)
			machineID := vz.NewMacMachineIdentifierWithDataRepresentation(&nsDataObj)
			if machineID.ID != 0 {
				if verbose {
					fmt.Println("  Loaded existing machine identifier")
				}
				return machineID
			}
		}
	}

	// Create new machine identifier
	machineID := vz.NewVZMacMachineIdentifier()
	if verbose {
		fmt.Println("  Created new machine identifier")
	}

	// Save for future use
	if err := saveMachineIdentifier(machineID, machineIDPath); err != nil {
		fmt.Printf("  warning: could not save machine identifier: %v\n", err)
	}

	return machineID
}

// saveMachineIdentifier saves the machine identifier data representation to a file.
func saveMachineIdentifier(machineID vz.VZMacMachineIdentifier, path string) error {
	data := machineID.DataRepresentation()
	if data.GetID() == 0 {
		return fmt.Errorf("machine identifier has no data representation")
	}
	return saveNSDataToFile(data.GetID(), path)
}

// loadOrCreateMACAddress loads an existing MAC address or creates a new one.
func loadOrCreateMACAddress() vz.VZMACAddress {
	macPath := filepath.Join(vmDir, "mac.address")

	if data, err := os.ReadFile(macPath); err == nil && len(data) > 0 {
		macStr := strings.TrimSpace(string(data))
		macAddr := vz.NewMACAddressWithString(macStr)
		if macAddr.ID != 0 {
			if verbose {
				fmt.Printf("  Loaded existing MAC address: %s\n", macStr)
			}
			return macAddr
		}
	}

	macAddr := vz.GetVZMACAddressClass().RandomLocallyAdministeredAddress()
	if verbose {
		fmt.Printf("  Created new MAC address: %s\n", macAddr.String())
	}

	if err := os.WriteFile(macPath, []byte(macAddr.String()+"\n"), 0644); err != nil {
		fmt.Printf("  warning: could not save MAC address: %v\n", err)
	}

	return macAddr
}

// loadOrCreateHardwareModel loads an existing hardware model or creates one from a restore image.
func loadOrCreateHardwareModel() (vz.VZMacHardwareModel, error) {
	hwModelPath := filepath.Join(vmDir, "hw.model")

	// Check if we have a saved hardware model
	if data, err := os.ReadFile(hwModelPath); err == nil && len(data) > 0 {
		if verbose {
			fmt.Printf("  Found hw.model file: %s (%d bytes)\n", hwModelPath, len(data))
		}
		nsData := createNSDataFromBytes(data)
		if nsData != 0 {
			nsDataObj := foundation.NSDataFromID(nsData)
			model := vz.NewMacHardwareModelWithDataRepresentation(&nsDataObj)
			if verbose {
				fmt.Printf("  Hardware model ID: %#x, Supported: %v\n", model.ID, model.ID != 0 && model.Supported())
			}
			if model.ID != 0 && model.Supported() {
				return model, nil
			}
			if model.ID != 0 {
				fmt.Printf("  warning: hardware model loaded but not supported on this host\n")
			}
		} else {
			fmt.Println("  warning: failed to create NSData from hw.model bytes")
		}
	} else if err != nil {
		fmt.Printf("  No existing hw.model: %v\n", err)
	}

	// Try to get hardware model from IPSW or fetch latest
	var restoreImage vz.VZMacOSRestoreImage
	var err error

	if ipswPath != "" {
		fmt.Printf("  Loading restore image from: %s\n", ipswPath)
		restoreImage, err = loadMacOSRestoreImageFromPath(ipswPath)
		if err != nil {
			return vz.VZMacHardwareModel{}, fmt.Errorf("load IPSW: %w", err)
		}
	} else {
		fmt.Println("  Fetching latest restore image info...")
		restoreImage, err = fetchLatestRestoreImageObject()
		if err != nil {
			return vz.VZMacHardwareModel{}, fmt.Errorf("fetch restore image: %w", err)
		}
	}

	// Get mostFeaturefulSupportedConfiguration from restore image
	configReqs := getMostFeaturefulSupportedConfiguration(restoreImage)
	if configReqs.ID == 0 {
		return vz.VZMacHardwareModel{}, fmt.Errorf("restore image has no supported configuration for this host")
	}

	// Get hardware model from configuration requirements
	model := getHardwareModel(&configReqs)
	if model.ID == 0 {
		return vz.VZMacHardwareModel{}, fmt.Errorf("failed to get hardware model from restore image")
	}

	if model.ID == 0 {
		return vz.VZMacHardwareModel{}, fmt.Errorf("hardware model is nil")
	}

	if !model.Supported() {
		return vz.VZMacHardwareModel{}, fmt.Errorf("hardware model not supported on this host")
	}

	// Save hardware model for future use
	if err := saveHardwareModel(model, hwModelPath); err != nil {
		fmt.Printf("  warning: could not save hardware model: %v\n", err)
	}

	if verbose {
		fmt.Printf("  Using hardware model from restore image (build: %s)\n", restoreImage.BuildVersion())
	}
	return model, nil
}

// saveHardwareModel saves the hardware model data representation to a file.
func saveHardwareModel(model vz.VZMacHardwareModel, path string) error {
	data := model.DataRepresentation()
	if data == nil || data.GetID() == 0 {
		return fmt.Errorf("hardware model has no data representation")
	}
	return saveNSDataToFile(data.GetID(), path)
}

// Helper functions to set array properties using generated slice setters.
// These use the generated bindings' FromID pattern to convert concrete subtypes
// to base types required by the generated setters.
func setStorageDevices(config vz.VZVirtualMachineConfiguration, device vz.VZVirtioBlockDeviceConfiguration) {
	config.SetStorageDevices([]vz.VZStorageDeviceConfiguration{
		vz.VZStorageDeviceConfigurationFromID(device.ID),
	})
}

func setGraphicsDevices(config vz.VZVirtualMachineConfiguration, device vz.VZMacGraphicsDeviceConfiguration) {
	config.SetGraphicsDevices([]vz.VZGraphicsDeviceConfiguration{
		vz.VZGraphicsDeviceConfigurationFromID(device.ID),
	})
}

func setDisplays(config vz.VZMacGraphicsDeviceConfiguration, display vz.VZMacGraphicsDisplayConfiguration) {
	config.SetDisplays([]vz.VZMacGraphicsDisplayConfiguration{display})
}

func setNetworkDevices(config vz.VZVirtualMachineConfiguration, device vz.VZVirtioNetworkDeviceConfiguration) {
	config.SetNetworkDevices([]vz.VZNetworkDeviceConfiguration{
		vz.VZNetworkDeviceConfigurationFromID(device.ID),
	})
}

func setKeyboards(config vz.VZVirtualMachineConfiguration, device vz.IVZKeyboardConfiguration) {
	config.SetKeyboards([]vz.VZKeyboardConfiguration{
		vz.VZKeyboardConfigurationFromID(device.GetID()),
	})
}

func setPointingDevices(config vz.VZVirtualMachineConfiguration, devices []vz.IVZPointingDeviceConfiguration) {
	converted := make([]vz.VZPointingDeviceConfiguration, len(devices))
	for i, dev := range devices {
		converted[i] = vz.VZPointingDeviceConfigurationFromID(dev.GetID())
	}
	config.SetPointingDevices(converted)
}

func setEntropyDevices(config vz.VZVirtualMachineConfiguration, device vz.VZVirtioEntropyDeviceConfiguration) {
	config.SetEntropyDevices([]vz.VZEntropyDeviceConfiguration{
		vz.VZEntropyDeviceConfigurationFromID(device.ID),
	})
}

func setAudioDevices(config vz.VZVirtualMachineConfiguration, device vz.VZVirtioSoundDeviceConfiguration) {
	config.SetAudioDevices([]vz.VZAudioDeviceConfiguration{
		vz.VZAudioDeviceConfigurationFromID(device.ID),
	})
}

func setSerialPorts(config vz.VZVirtualMachineConfiguration, device vz.VZVirtioConsoleDeviceSerialPortConfiguration) {
	config.SetSerialPorts([]vz.VZSerialPortConfiguration{
		vz.VZSerialPortConfigurationFromID(device.ID),
	})
}

// createSharedFoldersDevice creates a VirtioFS device with a MultipleDirectoryShare
// for toolbar-managed shared folders. This device is always created so the GUI can
// hotplug folders at runtime without requiring -v flags at boot.
func createSharedFoldersDevice(folders []SharedFolderEntry) vz.VZVirtioFileSystemDeviceConfiguration {
	fsConfig := vz.NewVirtioFileSystemDeviceConfigurationWithTag(SharedFoldersVirtioFSTag)
	if fsConfig.ID == 0 {
		return fsConfig
	}
	fsConfig.Retain()

	// Build initial share from persisted toolbar folders.
	keys := make([]objectivec.IObject, 0, len(folders))
	values := make([]objectivec.IObject, 0, len(folders))
	for _, f := range folders {
		if _, err := os.Stat(f.Path); err != nil {
			fmt.Printf("warning: shared folder not found: %s\n", f.Path)
			continue
		}
		url := foundation.NewURLFileURLWithPath(f.Path)
		sharedDir := vz.NewSharedDirectoryWithURLReadOnly(url, f.ReadOnly)
		sharedDir.Retain()
		nsKey := objc.String(f.Tag)
		keys = append(keys, objectivec.ObjectFromID(nsKey))
		values = append(values, objectivec.ObjectFromID(sharedDir.ID))
		mode := "rw"
		if f.ReadOnly {
			mode = "ro"
		}
		fmt.Printf("Shared folder: %s -> %s/%s (%s)\n", f.Path, defaultSharedFoldersMountPoint, f.Tag, mode)
	}

	var dict foundation.NSDictionary
	if len(keys) > 0 {
		dict = newDictFromSlices(values, keys)
	} else {
		dict = foundation.NewNSDictionary()
	}
	share := vz.NewMultipleDirectoryShareWithDirectories(&dict)
	share.Retain()
	fsConfig.SetShare(&share.VZDirectoryShare)
	return fsConfig
}

// newDictFromSlices creates an NSDictionary from Go slices using the
// initWithObjects:forKeys:count: selector. The NSArray-based
// initWithObjects:forKeys: does not work with purego because objc.Send
// passes Go slices as raw pointers instead of converting them to NSArrays.
func newDictFromSlices(values, keys []objectivec.IObject) foundation.NSDictionary {
	count := len(values)
	if count == 0 {
		return foundation.NewNSDictionary()
	}
	// Extract raw objc IDs so we can pass a C array pointer.
	valIDs := make([]objc.ID, count)
	keyIDs := make([]objc.ID, count)
	for i := range values {
		valIDs[i] = values[i].GetID()
		keyIDs[i] = keys[i].GetID()
	}
	instance := objc.Send[objc.ID](objc.ID(objc.GetClass("NSDictionary")), objc.Sel("alloc"))
	rv := objc.Send[objc.ID](instance, objc.Sel("initWithObjects:forKeys:count:"),
		objc.CArray(valIDs), objc.CArray(keyIDs), uint(count))
	return foundation.NSDictionaryFromID(rv)
}

// setDirectorySharingDevicesMulti adds multiple VirtioFS configurations to the VM
func setDirectorySharingDevicesMulti(config vz.VZVirtualMachineConfiguration, devices []vz.VZVirtioFileSystemDeviceConfiguration) {
	var configs []vz.VZDirectorySharingDeviceConfiguration
	for _, device := range devices {
		configs = append(configs, vz.VZDirectorySharingDeviceConfigurationFromID(device.ID))
	}
	config.SetDirectorySharingDevices(configs)
}

// startVMWithQueue starts the virtual machine using a dispatch queue.
// If a suspend state file exists and recovery mode is not requested,
// it restores from the saved state for near-instant resume.
func startVMWithQueue(vm vz.VZVirtualMachine, queue dispatch.Queue) error {
	if guiMode {
		return runVMWithGUI(vm, queue)
	}

	if err := startConfiguredVM(vm, queue, true); err != nil {
		return err
	}

	return runVMHeadless(vm, queue)
}

func startConfiguredVM(vm vz.VZVirtualMachine, queue dispatch.Queue, pumpRunLoop bool) error {
	// Handle boot-args - save to file for manual application inside guest
	if bootArgs != "" {
		bootArgsPath := filepath.Join(vmDir, "boot-args.txt")
		if err := os.WriteFile(bootArgsPath, []byte(bootArgs+"\n"), 0644); err != nil {
			fmt.Printf("warning: could not save boot-args: %v\n", err)
		} else {
			fmt.Printf("Boot args saved to: %s\n", bootArgsPath)
			fmt.Printf("To apply inside guest: sudo nvram boot-args=\"%s\"\n", bootArgs)
		}
	}

	// Try to restore from suspend state (UTM-style fast resume)
	if skipResume && hasSuspendState() {
		fmt.Println("Discarding saved suspend state and performing cold boot...")
		os.Remove(suspendStatePath())
		os.Remove(suspendConfigPath())
	}
	if canSaveRestore && !recoveryMode && hasSuspendState() {
		stateFile := suspendStatePath()
		if err := checkSuspendConfigMatch(); err != nil {
			fmt.Printf("Cannot restore suspend state: %v\n", err)
			fmt.Println("Performing cold boot...")
			os.Remove(suspendStatePath())
			os.Remove(suspendConfigPath())
		} else {
			if info, err := os.Stat(stateFile); err == nil {
				fmt.Printf("Restoring VM from suspended state (%s)...\n", FormatSize(info.Size()))
			} else {
				fmt.Println("Restoring VM from suspended state...")
			}
			if err := restoreAndResumeVM(vm, queue); err == nil {
				fmt.Println("VM resumed from saved state")
				os.Remove(suspendConfigPath())
				return nil
			} else {
				fmt.Printf("Suspend restore failed: %v\n", err)
				fmt.Println("Performing cold boot...")
			}
			os.Remove(suspendStatePath())
			os.Remove(suspendConfigPath())
		}
	}

	fmt.Println("Starting virtual machine...")
	startErr := beginVMStart(vm, queue)
	if err := waitForVMStart(startErr, pumpRunLoop); err != nil {
		if !printNSErrorSummary("VM start error", err) {
			fmt.Fprintf(os.Stderr, "error: vm start: %v\n", err)
		}
		// Check if the disk is still attached — a common cause of
		// "storage device attachment is invalid".
		diskFile := diskPath
		if diskFile == "" {
			diskFile = filepath.Join(vmDir, "disk.img")
		}
		if _, found, _ := findAttachedDisk(diskFile); found {
			fmt.Println()
			fmt.Println("Hint: the disk image is still mounted from a previous inject/verify.")
			fmt.Println("  Run: ./vz-macos disk-detach")
		}
		return fmt.Errorf("vm start failed: %w", err)
	}
	fmt.Println("VM started successfully")
	return nil
}

func beginVMStart(vm vz.VZVirtualMachine, queue dispatch.Queue) <-chan error {
	startErr := make(chan error, 1)
	startHandlerFn := func(err error) {
		startErr <- snapshotNSError(err)
	}

	if verbose {
		fmt.Printf("beginVMStart: queue=%#x currentState=%s recovery=%v\n",
			queue.Handle(), vmStateName(vz.VZVirtualMachineState(vm.State())), recoveryMode)
	}

	DispatchAsyncQueue(queue, func() {
		if recoveryMode {
			fmt.Println("Starting VM in recovery mode...")
			startOptions := vz.NewVZMacOSVirtualMachineStartOptions()
			startOptions.SetStartUpFromMacOSRecovery(true)
			vm.StartWithOptionsCompletionHandler(&startOptions.VZVirtualMachineStartOptions, startHandlerFn)
			return
		}
		vm.StartWithCompletionHandler(startHandlerFn)
	})

	return startErr
}

func waitForVMStart(startErr <-chan error, pumpRunLoop bool) error {
	timeout := time.After(30 * time.Second)
	for {
		select {
		case err := <-startErr:
			return err
		case <-timeout:
			return fmt.Errorf("vm start timed out")
		default:
			if pumpRunLoop {
				vzkit.RunRunLoopOnce()
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}

// runVMHeadless runs the VM in headless mode with serial console and signal handling.
func runVMHeadless(vm vz.VZVirtualMachine, queue dispatch.Queue) error {
	// Put terminal in raw mode for serial console interaction
	var restoreTerminal func()
	if serialOutput == "stdout" {
		restoreTerminal = setRawMode()
	}

	app := ensureAppReady(appkit.NSApplicationActivationPolicyAccessory)

	sock := GetControlSocketPathForVM(vmDir)
	controlServer := NewControlServerWithVMDir(sock, vmDir)
	controlServer.SetVM(vm, queue)
	guiController, err := newHeadlessGUIController(app, vm, queue, controlServer, false)
	if err != nil {
		if restoreTerminal != nil {
			restoreTerminal()
		}
		return fmt.Errorf("headless presentation: %w", err)
	}
	controlServer.SetGUIController(guiController)
	if err := controlServer.Start(); err != nil {
		fmt.Printf("warning: control socket: %v\n", err)
	} else {
		fmt.Printf("Control socket: %s\n", sock)
		if verbose {
			fmt.Printf("  vz-macos ctl -socket %s agent-ping\n", sock)
			fmt.Printf("  vz-macos ctl -socket %s gui open\n", sock)
		}
	}

	// Check if vz-agent is available in the guest (background, non-blocking).
	go checkAgentAvailability(controlServer)

	// Auto-mount tagged volumes in guest if requested.
	if autoMountVolumes {
		ctx, cancelAutoMount := context.WithCancel(context.Background())
		defer cancelAutoMount()
		go autoMountTaggedVolumes(ctx, controlServer, getEffectiveVolumes())
	}

	type vmStateUpdate struct {
		mu            sync.Mutex
		newState      vz.VZVirtualMachineState
		changed       bool
		terminate     bool
		signalCleanup bool
	}
	var stateUpdate vmStateUpdate
	stateUpdate.newState = -1

	monitorDone := make(chan struct{})
	var stopMonitorOnce sync.Once
	stopMonitor := func() {
		stopMonitorOnce.Do(func() {
			close(monitorDone)
		})
	}
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		var lastState vz.VZVirtualMachineState = -1
		for {
			select {
			case <-monitorDone:
				return
			case <-ticker.C:
			}
			state := vz.VZVirtualMachineState(vm.State())
			if state != lastState {
				lastState = state
				stateUpdate.mu.Lock()
				stateUpdate.newState = state
				stateUpdate.changed = true
				stateUpdate.mu.Unlock()
			}
			if state == vz.VZVirtualMachineStateStopped || state == vz.VZVirtualMachineStateError {
				stateUpdate.mu.Lock()
				if stateUpdate.signalCleanup {
					stateUpdate.mu.Unlock()
					return
				}
				stateUpdate.terminate = true
				stateUpdate.changed = true
				stateUpdate.mu.Unlock()
				return
			}
		}
	}()

	// Setup cleanup on exit — save state if possible, otherwise hard stop.
	cleanup := func() {
		stopMonitor()
		controlServer.Stop()
		guiController.Shutdown()
		if restoreTerminal != nil {
			restoreTerminal()
		}
		if canSaveRestore {
			fmt.Println("\nSuspending VM...")
			if err := suspendVM(vm, queue); err != nil {
				fmt.Printf("Suspend failed: %v, stopping VM...\n", err)
				hardStopVM(vm, queue)
			} else {
				fmt.Println("VM suspended")
			}
		} else {
			fmt.Println("\nStopping VM...")
			hardStopVM(vm, queue)
		}
		closeSerialOutputFile()
		app.Stop(nil)
		postDummyEvent(app)
	}
	var cleanupOnce sync.Once
	setupSignalHandler(func() {
		stateUpdate.mu.Lock()
		stateUpdate.signalCleanup = true
		stateUpdate.mu.Unlock()
		cleanupOnce.Do(cleanup)
	})

	var scheduleTimer func()
	scheduleTimer = func() {
		foundation.GetTimerClass().ScheduledTimerWithTimeIntervalRepeatsBlock(
			0.033,
			false,
			func(_ *foundation.NSTimer) {
				stateUpdate.mu.Lock()
				if stateUpdate.changed {
					state := stateUpdate.newState
					terminate := stateUpdate.terminate
					stateUpdate.changed = false
					if state >= 0 {
						guiController.updateStateOnMain(state)
					}
					if terminate {
						os.Remove(suspendStatePath())
						os.Remove(suspendConfigPath())
						clearInjectSucceeded()
						app.Stop(nil)
						postDummyEvent(app)
						stateUpdate.mu.Unlock()
						return
					}
				}
				stateUpdate.mu.Unlock()
				scheduleTimer()
			},
		)
	}
	scheduleTimer()
	app.Run()

	stopMonitor()
	controlServer.Stop()
	guiController.shutdownOnMain()
	if restoreTerminal != nil {
		restoreTerminal()
	}
	os.Remove(suspendStatePath())
	os.Remove(suspendConfigPath())
	clearInjectSucceeded()
	closeSerialOutputFile()
	fmt.Println("VM stopped")
	return nil
}

// restoreAndResumeVM restores VM state from the suspend file and resumes execution.
// The VM must be in stopped state. After restore it enters paused state, then resume
// brings it back to running.
func restoreAndResumeVM(vm vz.VZVirtualMachine, queue dispatch.Queue) error {
	stateFile := suspendStatePath()

	// Verify the VM is in the right state for restore.
	var currentState vz.VZVirtualMachineState
	stateCh := make(chan struct{})
	DispatchAsyncQueue(queue, func() {
		currentState = vz.VZVirtualMachineState(vm.State())
		close(stateCh)
	})
	select {
	case <-stateCh:
	case <-time.After(5 * time.Second):
		return fmt.Errorf("timed out checking VM state")
	}
	if currentState != vz.VZVirtualMachineStateStopped {
		return fmt.Errorf("vm must be stopped to restore (current: %s)", vmStateName(currentState))
	}

	restoreURL := foundation.NewURLFileURLWithPath(stateFile)
	restoreURL.Retain()

	// Restore state (VM must be stopped → becomes paused)
	errCh := make(chan error, 1)
	DispatchAsyncQueue(queue, func() {
		vm.RestoreMachineStateFromURLCompletionHandler(restoreURL, func(err error) {
			errCh <- snapshotNSError(err)
		})
	})

	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("restore state: %w", err)
		}
	case <-time.After(60 * time.Second):
		return fmt.Errorf("restore state timed out")
	}

	// Resume (paused → running)
	resumeCh := make(chan error, 1)
	DispatchAsyncQueue(queue, func() {
		vm.ResumeWithCompletionHandler(func(err error) {
			resumeCh <- snapshotNSError(err)
		})
	})

	select {
	case err := <-resumeCh:
		if err != nil {
			return fmt.Errorf("resume after restore: %w", err)
		}
	case <-time.After(30 * time.Second):
		return fmt.Errorf("resume timed out")
	}

	// Delete suspend file after successful restore (one-shot, like UTM)
	os.Remove(stateFile)
	return nil
}

// suspendVM pauses the VM and saves its state to the suspend file.
// After a successful save the VM can be restored on next launch.
func suspendVM(vm vz.VZVirtualMachine, queue dispatch.Queue) error {
	// Pause the VM first
	pauseCh := make(chan error, 1)
	DispatchAsyncQueue(queue, func() {
		state := vz.VZVirtualMachineState(vm.State())
		if state == vz.VZVirtualMachineStatePaused {
			pauseCh <- nil
			return
		}
		if state != vz.VZVirtualMachineStateRunning {
			pauseCh <- fmt.Errorf("vm not running (state: %d)", state)
			return
		}
		vm.PauseWithCompletionHandler(func(err error) {
			pauseCh <- snapshotNSError(err)
		})
	})

	select {
	case err := <-pauseCh:
		if err != nil {
			return fmt.Errorf("pause: %w", err)
		}
	case <-time.After(30 * time.Second):
		return fmt.Errorf("pause timed out")
	}

	// Save state to file
	stateFile := suspendStatePath()
	saveURL := foundation.NewURLFileURLWithPath(stateFile)
	saveURL.Retain()

	saveCh := make(chan error, 1)
	DispatchAsyncQueue(queue, func() {
		vm.SaveMachineStateToURLCompletionHandler(saveURL, func(err error) {
			saveCh <- snapshotNSError(err)
		})
	})

	select {
	case err := <-saveCh:
		if err != nil {
			os.Remove(stateFile) // Clean up partial file
			return fmt.Errorf("save state: %w", err)
		}
	case <-time.After(120 * time.Second):
		os.Remove(stateFile)
		return fmt.Errorf("save state timed out")
	}

	if info, err := os.Stat(stateFile); err == nil {
		fmt.Printf("VM state saved (%s)\n", FormatSize(info.Size()))
	}

	// Save config fingerprint so we can detect mismatches on restore.
	saveSuspendConfig()

	return nil
}

// hardStopVM forcibly stops the VM. Used as fallback when suspend fails.
func hardStopVM(vm vz.VZVirtualMachine, queue dispatch.Queue) {
	DispatchAsyncQueue(queue, func() {
		vm.StopWithCompletionHandler(func(err error) {
			if err := snapshotNSError(err); err != nil {
				fmt.Fprintf(os.Stderr, "error: vm stop: %v\n", err)
			}
		})
	})
}

// runVMWithGUI shows a GUI window with the VM display and runs the NSApplication event loop.
func runVMWithGUI(vm vz.VZVirtualMachine, queue dispatch.Queue) error {
	// Transform the process into a foreground app so the window server
	// routes events to us. This is required for ForceDirectExecution
	// (bare binary) where SetActivationPolicy alone doesn't work.
	transformToForegroundApp()

	app := getSharedApp()
	app.SetActivationPolicy(appkit.NSApplicationActivationPolicyRegular)
	setAppIcon(&app)
	if !appFinishedLaunching {
		// Use "run-then-stop" to fully initialize the NSApplication event
		// machinery. Calling just FinishLaunching() doesn't set up the
		// window server event routing, so mouse/keyboard events are never
		// delivered. app.Run() does the full initialization internally.
		//
		// We schedule a zero-delay timer that calls app.stop: on the first
		// run loop iteration, so Run() returns almost immediately after
		// completing its setup. This avoids the purego GC crash caused by
		// a permanent reflect.Value.call frame (which only happens when
		// Run() blocks indefinitely).
		foundation.GetTimerClass().ScheduledTimerWithTimeIntervalRepeatsBlock(0, false, func(_ *foundation.NSTimer) {
			app.Stop(nil)
			postDummyEvent(app)
		})
		app.Run()
		appFinishedLaunching = true
	}

	if launchOrder == "start-first" {
		if verbose {
			fmt.Println("GUI launch order: start-first")
		}
		if err := startConfiguredVM(vm, queue, true); err != nil {
			return err
		}
	} else if verbose {
		fmt.Println("GUI launch order: window-first")
	}

	// Create VM view
	vmView := vz.NewVZVirtualMachineView()
	vmView.SetVirtualMachine(&vm)
	vmView.SetCapturesSystemKeys(false) // start with system keys going to macOS; toggle via toolbar (Cmd+K)
	vmView.SetAutomaticallyReconfiguresDisplay(true)
	if verbose {
		fmt.Printf("VM view created: id=%#x autoReconfiguresDisplay=%v\n",
			vmView.ID, vmView.AutomaticallyReconfiguresDisplay())
	}

	// Create window with proper frame
	contentRect := corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 100, Y: 100},
		Size:   corefoundation.CGSize{Width: defaultWindowWidth, Height: defaultWindowHeight},
	}
	window := appkit.NewWindowWithContentRectStyleMaskBackingDefer(
		contentRect,
		appkit.NSWindowStyleMaskTitled|
			appkit.NSWindowStyleMaskClosable|
			appkit.NSWindowStyleMaskMiniaturizable|
			appkit.NSWindowStyleMaskResizable,
		appkit.NSBackingStoreBuffered,
		false,
	)
	// Ensure standard chrome is visible even if style defaults were lost.
	window.SetStyleMask(
		appkit.NSWindowStyleMaskTitled |
			appkit.NSWindowStyleMaskClosable |
			appkit.NSWindowStyleMaskMiniaturizable |
			appkit.NSWindowStyleMaskResizable,
	)
	window.SetTitleVisibility(appkit.NSWindowTitleVisible)
	window.SetTitlebarAppearsTransparent(false)
	// Set window title based on OS type and VM name
	osLabel := "macOS VM"
	if linuxMode {
		osLabel = "Linux VM"
	}
	windowTitle := osLabel
	if vmName != "" && vmName != "default" {
		windowTitle = fmt.Sprintf("%s — %s", osLabel, vmName)
	}
	window.SetTitle(windowTitle)
	restoredFrame, frameAutosaveName := configureWindowFramePersistence(window)
	if verbose {
		if restoredFrame {
			fmt.Printf("Window frame restored from %q\n", frameAutosaveName)
		} else {
			fmt.Printf("No saved window frame for %q; using default layout\n", frameAutosaveName)
		}
	}

	// Set process name for Cmd-Tab display
	procName := "vz-macos"
	if vmName != "" && vmName != "default" {
		procName = fmt.Sprintf("vz-macos (%s)", vmName)
	}
	foundation.GetProcessInfoClass().ProcessInfo().SetProcessName(procName)

	// Show VM name on the dock icon badge.
	if vmName != "" && vmName != "default" {
		dockTile := app.DockTile()
		dockTile.SetBadgeLabel(vmName)
	}

	// Set the VM view frame to match the content rect
	vmViewAsNSView(vmView).SetFrame(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 0, Y: 0},
		Size:   contentRect.Size,
	})

	window.SetContentView(vmViewAsNSView(vmView))
	if !restoredFrame {
		window.Center()
	}

	// Add a boot overlay that fades out once the VM reaches Running state.
	// If the VM is already running (e.g. after install → restart), skip the overlay.
	var bootOverlay appkit.NSView
	currentState := vz.VZVirtualMachineState(vm.State())
	if currentState != vz.VZVirtualMachineStateRunning {
		bootOverlay = createBootOverlay(currentVMViewSize(vmView, contentRect.Size))
		vmViewAsNSView(vmView).AddSubview(&bootOverlay)
	}

	// Show window and make VM view first responder for keyboard input.
	window.MakeKeyAndOrderFront(nil)
	window.MakeFirstResponder(vmViewAsNSView(vmView).NSResponder)
	app.Activate()

	fmt.Println("VM display window opened.")

	// Start control socket for screenshots, keyboard, mouse control
	sock := GetControlSocketPathForVM(vmDir)
	controlServer := NewControlServerWithVMDir(sock, vmDir)
	controlServer.SetVMViewWithWindow(vmView, window)
	controlServer.SetVM(vm, queue)
	if err := controlServer.Start(); err != nil {
		fmt.Printf("warning: could not start control socket: %v\n", err)
	} else {
		fmt.Printf("Control socket: %s\n", sock)
		if verbose {
			fmt.Printf("  vz-macos ctl -socket %s screenshot -o screen.jpg\n", sock)
			fmt.Printf("  TOKEN=$(cat %s)\n", GetControlTokenPathForVM(vmDir))
			fmt.Printf("  echo '{\"type\":\"screenshot\",\"auth_token\":\"'$TOKEN'\"}' | nc -U %s\n", sock)
		}
	}

	// Check if vz-agent is available in the guest (background, non-blocking).
	go checkAgentAvailability(controlServer)

	// Create and attach toolbar
	vmToolbar := NewVMToolbar(window, vmView, vm, queue, controlServer, vmDir)

	// Setup main menu bar
	setupMainMenu(vmToolbar.delegateID)

	// Auto-mount tagged volumes in guest if requested.
	if autoMountVolumes {
		ctx, cancelAutoMount := context.WithCancel(context.Background())
		defer cancelAutoMount()
		go autoMountTaggedVolumes(ctx, controlServer, getEffectiveVolumes())
	}

	// Start unattended or provisioning automation if requested.
	// Unattended mode uses OCR for reliable detection; the older
	// provisioning path uses pixel heuristics.
	if unattended {
		go func() {
			if err := runUnattendedSetup(controlServer); err != nil {
				fmt.Fprintf(os.Stderr, "warning: unattended setup failed: %v\n", err)
			}
		}()
	} else if provisionUser != "" && shouldRunGUIAutomation() {
		go runProvisioningAutomation(controlServer)
	}

	// Shared state for background → main thread communication.
	// Background goroutine writes state changes; the timer reads them on
	// the main thread. This avoids DispatchAsync(GetMainDispatchQueue()) which
	// can cause purego callback GC issues in long-running scenarios.
	type vmStateUpdate struct {
		mu            sync.Mutex
		newState      vz.VZVirtualMachineState
		changed       bool
		terminate     bool
		signalCleanup bool // set by signal handler to prevent state monitor from triggering app.Stop
	}
	var stateUpdate vmStateUpdate
	stateUpdate.newState = -1
	var startResult <-chan error
	var runErr error
	if launchOrder == "window-first" {
		ch := make(chan error, 1)
		startResult = ch
		go func() {
			ch <- startConfiguredVM(vm, queue, false)
		}()
	}

	// Monitor VM state in background
	monitorDone := make(chan struct{})
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		var lastState vz.VZVirtualMachineState = -1
		for {
			select {
			case <-monitorDone:
				return
			case <-ticker.C:
			}
			state := vz.VZVirtualMachineState(vm.State())
			if state != lastState {
				if verbose {
					fmt.Printf("VM state transition: %s -> %s\n", vmStateName(lastState), vmStateName(state))
				}
				lastState = state
				stateUpdate.mu.Lock()
				stateUpdate.newState = state
				stateUpdate.changed = true
				stateUpdate.mu.Unlock()
			}
			if state == vz.VZVirtualMachineStateStopped || state == vz.VZVirtualMachineStateError {
				stateUpdate.mu.Lock()
				if stateUpdate.signalCleanup {
					// Signal handler is managing the exit; don't trigger app.Stop
					// which would cause runVMWithGUI to return and main() to
					// fall through to handleDefaultAction.
					stateUpdate.mu.Unlock()
					return
				}
				fmt.Println("VM stopped")
				stateUpdate.terminate = true
				stateUpdate.changed = true
				stateUpdate.mu.Unlock()
				return
			}
		}
	}()

	// Setup cleanup on window close / app quit — suspend if possible
	cleanup := func() {
		close(monitorDone)
		controlServer.Stop()
		if canSaveRestore {
			fmt.Println("\nSuspending VM...")
			if err := suspendVM(vm, queue); err != nil {
				fmt.Printf("Suspend failed: %v, stopping VM...\n", err)
				hardStopVM(vm, queue)
			} else {
				fmt.Println("VM suspended (will resume on next launch)")
			}
		} else {
			fmt.Println("\nStopping VM...")
			hardStopVM(vm, queue)
		}
		closeSerialOutputFile()
	}
	var cleanupOnce sync.Once
	doCleanup := func() { cleanupOnce.Do(cleanup) }

	// Close-window should behave like app quit: clean up VM lifecycle and stop the app.
	windowDelegate := appkit.NewNSWindowDelegate(appkit.NSWindowDelegateConfig{
		ShouldClose: func(_ appkit.NSWindow) bool {
			saveWindowDisplayPlacement(window, frameAutosaveName)
			window.SaveFrameUsingName(frameAutosaveName)
			stateUpdate.mu.Lock()
			stateUpdate.signalCleanup = true
			stateUpdate.mu.Unlock()
			doCleanup()
			app.Stop(nil)
			postDummyEvent(app)
			return true
		},
	})
	window.SetDelegate(windowDelegate)

	// Register an NSApplicationDelegate so that Cmd+Q and "Quit" in the
	// status item menu trigger a clean suspend instead of a hard kill.
	// ShouldTerminate runs cleanup, then cancels NSApp's terminate: flow
	// so we control the exit via app.Stop + postDummyEvent.
	delegate := appkit.NewNSApplicationDelegate(appkit.NSApplicationDelegateConfig{
		ShouldTerminate: func(_ appkit.NSApplication) appkit.NSApplicationTerminateReply {
			saveWindowDisplayPlacement(window, frameAutosaveName)
			window.SaveFrameUsingName(frameAutosaveName)
			doCleanup()
			app.Stop(nil)
			postDummyEvent(app)
			return appkit.NSTerminateCancel
		},
	})
	app.SetDelegate(delegate)
	setupSignalHandler(func() {
		stateUpdate.mu.Lock()
		stateUpdate.signalCleanup = true
		stateUpdate.mu.Unlock()
		doCleanup()
	})

	// Use app.Run() for proper event delivery. The window server only
	// routes events (mouse clicks, keyboard input, Cmd+Tab) to processes
	// whose NSApplication is in the "running" state via [NSApp run].
	// A manual NextEventMatchingMask/SendEvent loop does not work because
	// the window server connection requires the internal state that Run()
	// sets up.
	//
	// GC safety: app.Run() blocks on objc.Send which does NOT create
	// reflect.Value.call frames (it uses purego.SyscallN). The concern
	// about GC crashes only applies to purego callbacks that use
	// reflect.MakeFunc — our timer callback below is short-lived, so
	// its reflect frame is cleaned up before GC can scan stale values.
	var overlayFadeStep int = -1 // -1 means not fading
	var pauseOverlay appkit.NSView
	var pauseFadeStep int = -1 // -1 means not fading; >0 means fading in; used for fade-out too

	// Self-rescheduling one-shot timer handles state updates on the main
	// thread at ~30 Hz. Each invocation creates a fresh reflect frame via
	// purego's reflect.MakeFunc that is released when the callback returns.
	// This avoids the SIGTRAP crash caused by a long-lived repeating timer
	// whose persistent reflect.Value.call frame accumulates stale values
	// that GC mistakes for invalid pointers.
	var scheduleTimer func()
	scheduleTimer = func() {
		foundation.GetTimerClass().ScheduledTimerWithTimeIntervalRepeatsBlock(
			0.033, // ~30 Hz
			false, // one-shot: no persistent reflect frame
			func(_ *foundation.NSTimer) {
				if startResult != nil {
					select {
					case err := <-startResult:
						startResult = nil
						if err != nil {
							runErr = err
							stateUpdate.mu.Lock()
							stateUpdate.terminate = true
							stateUpdate.changed = true
							stateUpdate.mu.Unlock()
						}
					default:
					}
				}

				// Apply pending state updates from background goroutine.
				stateUpdate.mu.Lock()
				if stateUpdate.changed {
					stateUpdate.changed = false
					if stateUpdate.newState >= 0 {
						vmToolbar.UpdateState(stateUpdate.newState)
						window.SetTitle(fmt.Sprintf("%s — %s", windowTitle, vmStateName(stateUpdate.newState)))
						// Start fading the boot overlay once the VM is running.
						if stateUpdate.newState == vz.VZVirtualMachineStateRunning && overlayFadeStep == -1 && bootOverlay.ID != 0 {
							overlayFadeStep = 15 // ~0.5s fade at 30 Hz
						}

						// Show/hide pause overlay based on VM state.
						isPaused := stateUpdate.newState == vz.VZVirtualMachineStatePaused ||
							stateUpdate.newState == vz.VZVirtualMachineStateSaving ||
							stateUpdate.newState == vz.VZVirtualMachineStateRestoring
						if isPaused {
							// Always recreate the overlay if the state changes to ensure the label is correct.
							needsFadeIn := true
							if pauseOverlay.ID != 0 {
								objc.Send[objc.ID](pauseOverlay.ID, objc.Sel("removeFromSuperview"))
								needsFadeIn = false // already fading or faded in
							}
							pauseOverlay = createPauseOverlay(currentVMViewSize(vmView, contentRect.Size), stateUpdate.newState)
							if needsFadeIn {
								objc.Send[objc.ID](pauseOverlay.ID, objc.Sel("setAlphaValue:"), 0.0)
								pauseFadeStep = 0 // start fade-in
							} else {
								// Inherit alpha or just be fully visible if it was already showing
								alpha := 1.0
								if pauseFadeStep >= 0 && pauseFadeStep < 10 {
									alpha = float64(pauseFadeStep) / 10.0
								}
								objc.Send[objc.ID](pauseOverlay.ID, objc.Sel("setAlphaValue:"), alpha)
							}
							vmViewAsNSView(vmView).AddSubview(&pauseOverlay)
						} else if !isPaused && pauseOverlay.ID != 0 {
							objc.Send[objc.ID](pauseOverlay.ID, objc.Sel("removeFromSuperview"))
							pauseOverlay = appkit.NSView{}
							pauseFadeStep = -1
						}
					}
					if stateUpdate.terminate {
						os.Remove(suspendStatePath())
						clearInjectSucceeded()
						app.Stop(nil)
						postDummyEvent(app)
						stateUpdate.mu.Unlock()
						return // don't reschedule — app is stopping
					}
				}
				stateUpdate.mu.Unlock()

				// Animate boot overlay fade-out.
				if overlayFadeStep >= 0 && bootOverlay.ID != 0 {
					alpha := float64(overlayFadeStep) / 15.0
					objc.Send[objc.ID](bootOverlay.ID, objc.Sel("setAlphaValue:"), alpha)
					overlayFadeStep--
					if overlayFadeStep < 0 {
						objc.Send[objc.ID](bootOverlay.ID, objc.Sel("removeFromSuperview"))
					}
				}

				// Animate pause overlay fade-in.
				if pauseFadeStep >= 0 && pauseOverlay.ID != 0 {
					const pauseFadeFrames = 10 // ~0.33s at 30 Hz
					if pauseFadeStep < pauseFadeFrames {
						pauseFadeStep++
						alpha := float64(pauseFadeStep) / float64(pauseFadeFrames)
						objc.Send[objc.ID](pauseOverlay.ID, objc.Sel("setAlphaValue:"), alpha)
					}
				}

				// Reschedule for next frame.
				scheduleTimer()
			},
		)
	}
	scheduleTimer()

	// app.Run() blocks until app.Stop() is called (when VM terminates).
	app.Run()

	return runErr
}

// transformToForegroundApp tells the window server that this process is a
// foreground GUI application. Without this, bare binaries (ForceDirectExecution)
// don't receive window events from the window server.
//
// Uses the deprecated but still functional TransformProcessType Carbon API,
// which is the only reliable way to promote a CLI process to a GUI app
// that receives events and appears in Cmd+Tab.
func transformToForegroundApp() {
	appServices, err := purego.Dlopen("/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices", purego.RTLD_LAZY)
	if err != nil {
		return
	}
	transformSym, err := purego.Dlsym(appServices, "TransformProcessType")
	if err != nil {
		return
	}
	// ProcessSerialNumber{0, kCurrentProcess(2)} with kProcessTransformToForegroundApplication(1)
	type ProcessSerialNumber struct {
		HighLongOfPSN uint32
		LowLongOfPSN  uint32
	}
	var transformProcessType func(psn *ProcessSerialNumber, transformState uint32) int32
	purego.RegisterFunc(&transformProcessType, transformSym)
	psn := ProcessSerialNumber{0, 2} // kCurrentProcess
	transformProcessType(&psn, 1)    // kProcessTransformToForegroundApplication
}

// shouldRunGUIAutomation reports whether GUI provisioning automation should
// run based on the selected -provision-strategy.
//
// When running a VM (not installing), the "inject" strategy is not applicable
// because injection requires pre-boot disk manipulation. In this case, we
// auto-upgrade to GUI automation so that -provision-user works as expected
// without requiring -provision-strategy gui.
func shouldRunGUIAutomation() bool {
	switch provisionStrategy {
	case "gui":
		return true
	case "auto":
		return !didInjectSucceed()
	case "inject":
		// During "run", inject is not applicable — auto-upgrade to GUI.
		if !installVM {
			if verbose {
				fmt.Println("[provision] auto-upgrading strategy from inject to gui (inject only applies during install)")
			}
			return true
		}
		return false
	default:
		return false
	}
}

func currentVMViewSize(vmView vz.VZVirtualMachineView, fallback corefoundation.CGSize) corefoundation.CGSize {
	bounds := vmViewAsNSView(vmView).Bounds().Size
	if bounds.Width > 0 && bounds.Height > 0 {
		return bounds
	}
	return fallback
}

// createBootOverlay creates a dark overlay with a "Booting..." label,
// shown over the VM view while the VM starts up.
func createBootOverlay(size corefoundation.CGSize) appkit.NSView {
	frame := corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 0, Y: 0},
		Size:   size,
	}
	overlay := appkit.NewViewWithFrame(frame)
	objc.Send[objc.ID](overlay.ID, objc.Sel("setWantsLayer:"), true)
	// Keep overlay synced to VM view size on live window resizes.
	objc.Send[objc.ID](overlay.ID, objc.Sel("setAutoresizingMask:"), uint(2|16))

	// Dark background via CALayer.
	layer := objc.Send[objc.ID](overlay.ID, objc.Sel("layer"))
	if layer != 0 {
		bgColor := objc.Send[objc.ID](
			objc.ID(objc.GetClass("NSColor")),
			objc.Sel("colorWithWhite:alpha:"),
			0.08, 0.95,
		)
		cgColor := objc.Send[objc.ID](bgColor, objc.Sel("CGColor"))
		objc.Send[objc.ID](layer, objc.Sel("setBackgroundColor:"), cgColor)
	}

	// Centered label.
	label := appkit.NewTextFieldLabelWithString("Booting...")
	fontClass := appkit.GetNSFontClass()
	font := fontClass.SystemFontOfSizeWeight(22, -0.4) // Light weight
	label.SetFont(font)
	label.SetAlignment(appkit.NSTextAlignmentCenter)
	whiteColor := objc.Send[objc.ID](
		objc.ID(objc.GetClass("NSColor")),
		objc.Sel("colorWithWhite:alpha:"),
		1.0, 0.7,
	)
	objc.Send[objc.ID](label.ID, objc.Sel("setTextColor:"), whiteColor)
	objc.Send[objc.ID](label.ID, objc.Sel("setBezeled:"), false)
	objc.Send[objc.ID](label.ID, objc.Sel("setDrawsBackground:"), false)
	objc.Send[objc.ID](label.ID, objc.Sel("setEditable:"), false)
	label.SetFrame(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 0, Y: (size.Height - 36) / 2},
		Size:   corefoundation.CGSize{Width: size.Width, Height: 36},
	})
	// Stretch label width and keep it vertically centered as the overlay grows.
	objc.Send[objc.ID](label.ID, objc.Sel("setAutoresizingMask:"), uint(2|8|32))
	overlay.AddSubview(&label.NSView)
	return overlay
}

// createPauseOverlay creates a semi-transparent overlay shown when the VM is paused or saving.
func createPauseOverlay(size corefoundation.CGSize, state vz.VZVirtualMachineState) appkit.NSView {
	frame := corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 0, Y: 0},
		Size:   size,
	}
	overlay := appkit.NewViewWithFrame(frame)
	objc.Send[objc.ID](overlay.ID, objc.Sel("setWantsLayer:"), true)

	// Make the overlay resize with its superview (WidthSizable | HeightSizable)
	// NSViewWidthSizable = 2, NSViewHeightSizable = 16
	objc.Send[objc.ID](overlay.ID, objc.Sel("setAutoresizingMask:"), 2|16)

	// Semi-transparent dark background.
	layer := objc.Send[objc.ID](overlay.ID, objc.Sel("layer"))
	if layer != 0 {
		bgColor := objc.Send[objc.ID](
			objc.ID(objc.GetClass("NSColor")),
			objc.Sel("colorWithWhite:alpha:"),
			0.0, 0.45,
		)
		cgColor := objc.Send[objc.ID](bgColor, objc.Sel("CGColor"))
		objc.Send[objc.ID](layer, objc.Sel("setBackgroundColor:"), cgColor)
	}

	// Status label.
	text := "Paused"
	if state == vz.VZVirtualMachineStateSaving {
		text = "Saving..."
	} else if state == vz.VZVirtualMachineStateRestoring {
		text = "Restoring..."
	}
	label := appkit.NewTextFieldLabelWithString(text)
	fontClass := appkit.GetNSFontClass()
	font := fontClass.SystemFontOfSizeWeight(28, 0.3) // Medium weight
	label.SetFont(font)
	label.SetAlignment(appkit.NSTextAlignmentCenter)
	whiteColor := objc.Send[objc.ID](
		objc.ID(objc.GetClass("NSColor")),
		objc.Sel("colorWithWhite:alpha:"),
		1.0, 0.9,
	)
	objc.Send[objc.ID](label.ID, objc.Sel("setTextColor:"), whiteColor)
	objc.Send[objc.ID](label.ID, objc.Sel("setBezeled:"), false)
	objc.Send[objc.ID](label.ID, objc.Sel("setDrawsBackground:"), false)
	objc.Send[objc.ID](label.ID, objc.Sel("setEditable:"), false)
	label.SetFrame(corefoundation.CGRect{
		Origin: corefoundation.CGPoint{X: 0, Y: (size.Height - 40) / 2},
		Size:   corefoundation.CGSize{Width: size.Width, Height: 40},
	})
	// Stretch label width and keep it vertically centered as the overlay grows.
	objc.Send[objc.ID](label.ID, objc.Sel("setAutoresizingMask:"), uint(2|8|32))
	overlay.AddSubview(&label.NSView)
	return overlay
}
