package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/dispatch"
	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/objectivec"
	privvz "github.com/tmc/apple/private/virtualization"
	vz "github.com/tmc/apple/virtualization"
	displayx "github.com/tmc/apple/x/vzkit/display"
	"github.com/tmc/vz-macos/internal/vmconfig"
	winsetup "github.com/tmc/vz-macos/internal/windows"
	"github.com/tmc/vz-macos/internal/windows/esd"
)

type windowsGraphics string

const (
	windowsGraphicsLinearFramebuffer windowsGraphics = "linear-framebuffer"
	windowsGraphicsVirtio            windowsGraphics = "virtio"
)

type windowsSerial string

const (
	windowsSerialVirtio windowsSerial = "virtio"
	windowsSerialPL011  windowsSerial = "pl011"
	windowsSerial16550  windowsSerial = "16550"
)

func parseWindowsGraphicsMode(s string) (windowsGraphics, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", string(windowsGraphicsVirtio):
		return windowsGraphicsVirtio, nil
	case string(windowsGraphicsLinearFramebuffer), "linear", "framebuffer":
		return windowsGraphicsLinearFramebuffer, nil
	default:
		return "", fmt.Errorf("invalid -windows-graphics %q (must be linear-framebuffer or virtio)", s)
	}
}

func parseWindowsSerialMode(s string) (windowsSerial, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", string(windowsSerialVirtio):
		return windowsSerialVirtio, nil
	case string(windowsSerialPL011):
		return windowsSerialPL011, nil
	case string(windowsSerial16550):
		return windowsSerial16550, nil
	default:
		return "", fmt.Errorf("invalid -windows-serial %q (must be virtio, pl011, or 16550)", s)
	}
}

func buildWindowsVMConfiguration(diskImagePath string) (vz.VZVirtualMachineConfiguration, error) {
	config, err := buildWindowsBaseConfiguration()
	if err != nil {
		return config, err
	}

	storage, err := windowsNVMeStorageDevice(diskImagePath, false)
	if err != nil {
		return config, err
	}
	storageDevices := []vz.VZStorageDeviceConfiguration{storage}

	if isoPath != "" {
		isoStorage, err := windowsUSBStorageDevice(resolvePath(isoPath), true)
		if err != nil {
			return config, fmt.Errorf("attach windows ISO: %w", err)
		}
		storageDevices = append(storageDevices, isoStorage)
	}
	config.SetStorageDevices(storageDevices)

	if len(usbDevices) > 0 {
		if err := AddUSBStorageToConfig(config, usbDevices); err != nil {
			return config, fmt.Errorf("add USB storage: %w", err)
		}
	}
	return config, nil
}

func buildWindowsInstallConfiguration(diskImagePath, windowsISO string) (vz.VZVirtualMachineConfiguration, error) {
	config, err := buildWindowsBaseConfiguration()
	if err != nil {
		return config, err
	}

	bootImage, err := ensureWindowsEFIBootImage(windowsISO)
	if err != nil {
		return config, err
	}

	bootStorage, err := windowsUSBStorageDevice(bootImage, true)
	if err != nil {
		return config, fmt.Errorf("attach EFI boot image: %w", err)
	}
	diskStorage, err := windowsNVMeStorageDevice(diskImagePath, false)
	if err != nil {
		return config, err
	}
	isoStorage, err := windowsUSBStorageDevice(windowsISO, true)
	if err != nil {
		return config, fmt.Errorf("attach windows ISO: %w", err)
	}
	autounattendISO, err := winsetup.CreateAutounattendISO(vmDir, winsetup.DefaultProvisionConfig())
	if err != nil {
		return config, fmt.Errorf("create windows autounattend ISO: %w", err)
	}
	autounattendStorage, err := windowsUSBStorageDevice(autounattendISO, true)
	if err != nil {
		return config, fmt.Errorf("attach windows autounattend ISO: %w", err)
	}
	virtioISO, err := winsetup.EnsureVirtIODriversISO("")
	if err != nil {
		return config, fmt.Errorf("ensure windows virtio drivers ISO: %w", err)
	}
	virtioStorage, err := windowsUSBStorageDevice(virtioISO, true)
	if err != nil {
		return config, fmt.Errorf("attach windows virtio drivers ISO: %w", err)
	}

	config.SetStorageDevices([]vz.VZStorageDeviceConfiguration{
		diskStorage,
		bootStorage,
		isoStorage,
		virtioStorage,
		autounattendStorage,
	})
	return config, nil
}

func buildWindowsBaseConfiguration() (vz.VZVirtualMachineConfiguration, error) {
	config := vz.NewVZVirtualMachineConfiguration()
	config.SetCPUCount(cpuCount)
	config.SetMemorySize(memoryGB * 1024 * 1024 * 1024)

	platformConfig := vz.NewVZGenericPlatformConfiguration()
	machineID := loadOrCreateWindowsMachineIdentifier()
	platformConfig.SetMachineIdentifier(&machineID)
	config.SetPlatform(&platformConfig.VZPlatformConfiguration)

	bootloader, err := createEFIBootLoader()
	if err != nil {
		return config, err
	}
	config.SetBootLoader(&bootloader.VZBootLoader)

	if err := setWindowsGraphicsDevices(config); err != nil {
		return config, err
	}

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

	keyboardConfig := vz.NewVZUSBKeyboardConfiguration()
	setKeyboards(config, keyboardConfig)

	pointingConfig := vz.NewVZUSBScreenCoordinatePointingDeviceConfiguration()
	setPointingDevices(config, []vz.IVZPointingDeviceConfiguration{pointingConfig})

	entropyConfig := vz.NewVZVirtioEntropyDeviceConfiguration()
	setEntropyDevices(config, entropyConfig)

	audioConfig := vz.NewVZVirtioSoundDeviceConfiguration()
	setAudioDevices(config, audioConfig)

	EnsureUSBController(config)

	balloonConfig := vz.NewVZVirtioTraditionalMemoryBalloonDeviceConfiguration()
	if balloonConfig.ID != 0 {
		setMemoryBalloonDevices(config, balloonConfig)
	}

	if sandboxAllowsVsock() {
		vsockConfig := vz.NewVZVirtioSocketDeviceConfiguration()
		if vsockConfig.ID != 0 {
			setSocketDevices(config, vsockConfig)
		}
	}

	serialConfig, err := createWindowsSerialConsoleConfig()
	if err != nil {
		return config, err
	}
	if serialConfig.ID != 0 {
		setSerialPorts(config, serialConfig)
		fmt.Println("  Serial console attached")
	}

	if err := applyPrivateVMConfiguration(config); err != nil {
		return config, err
	}
	return config, nil
}

func createWindowsSerialConsoleConfig() (vz.VZSerialPortConfiguration, error) {
	mode, err := parseWindowsSerialMode(windowsSerialMode)
	if err != nil {
		return vz.VZSerialPortConfiguration{}, err
	}
	if mode == windowsSerialVirtio {
		return createSerialConsoleConfig(), nil
	}
	attachment, ok := createSerialPortAttachment()
	if !ok {
		return vz.VZSerialPortConfiguration{}, nil
	}
	switch mode {
	case windowsSerialPL011:
		if privvz.GetVZPL011SerialPortConfigurationClass().Class() == 0 {
			return vz.VZSerialPortConfiguration{}, fmt.Errorf("private PL011 serial port configuration is unavailable")
		}
		serialConfig := privvz.NewVZPL011SerialPortConfiguration()
		if serialConfig.ID == 0 {
			return vz.VZSerialPortConfiguration{}, fmt.Errorf("create PL011 serial port configuration")
		}
		serialConfig.Retain()
		serial := vz.VZSerialPortConfigurationFromID(serialConfig.ID)
		serial.SetAttachment(&attachment.VZSerialPortAttachment)
		fmt.Println("  Windows serial: PL011")
		return serial, nil
	case windowsSerial16550:
		if privvz.GetVZ16550SerialPortConfigurationClass().Class() == 0 {
			return vz.VZSerialPortConfiguration{}, fmt.Errorf("private 16550 serial port configuration is unavailable")
		}
		serialConfig := privvz.NewVZ16550SerialPortConfiguration()
		if serialConfig.ID == 0 {
			return vz.VZSerialPortConfiguration{}, fmt.Errorf("create 16550 serial port configuration")
		}
		serialConfig.Retain()
		serial := vz.VZSerialPortConfigurationFromID(serialConfig.ID)
		serial.SetAttachment(&attachment.VZSerialPortAttachment)
		fmt.Println("  Windows serial: 16550")
		return serial, nil
	default:
		return vz.VZSerialPortConfiguration{}, fmt.Errorf("unsupported Windows serial mode: %s", mode)
	}
}

func setWindowsGraphicsDevices(config vz.VZVirtualMachineConfiguration) error {
	mode, err := parseWindowsGraphicsMode(windowsGraphicsMode)
	if err != nil {
		return err
	}
	switch mode {
	case windowsGraphicsLinearFramebuffer:
		return setWindowsLinearFramebufferGraphicsDevice(config)
	case windowsGraphicsVirtio:
		displayConfigs := []displayx.Config(displays)
		if len(displayConfigs) == 0 {
			displayConfigs = []displayx.Config{displayx.DefaultLinuxConfig()}
		}
		graphicsConfig, err := displayx.CreateVirtioGraphicsConfig(displayConfigs)
		if err != nil {
			return fmt.Errorf("create virtio graphics config: %w", err)
		}
		setVirtioGraphicsDevices(config, graphicsConfig)
		fmt.Println("  Windows graphics: VirtIO")
		return nil
	default:
		return fmt.Errorf("unsupported Windows graphics mode: %s", mode)
	}
}

func setWindowsLinearFramebufferGraphicsDevice(config vz.VZVirtualMachineConfiguration) error {
	if privvz.GetVZLinearFramebufferGraphicsDeviceConfigurationClass().Class() == 0 {
		return fmt.Errorf("private linear framebuffer graphics configuration is unavailable")
	}
	width, height := windowsDisplaySize()
	graphics := privvz.NewVZLinearFramebufferGraphicsDeviceConfigurationWithBackingStoreSize(corefoundation.CGSize{
		Width:  float64(width),
		Height: float64(height),
	})
	if graphics.ID == 0 {
		return fmt.Errorf("create linear framebuffer graphics configuration")
	}
	graphics.Retain()
	array := objectivec.IObjectSliceToNSArray([]privvz.VZLinearFramebufferGraphicsDeviceConfiguration{graphics})
	objc.Send[struct{}](config.ID, objc.Sel("setGraphicsDevices:"), array)
	fmt.Printf("  Windows graphics: linear framebuffer %dx%d\n", width, height)
	return nil
}

func windowsDisplaySize() (int, int) {
	displayConfigs := []displayx.Config(displays)
	if len(displayConfigs) == 0 {
		return 1920, 1200
	}
	return displayConfigs[0].Width, displayConfigs[0].Height
}

func windowsNVMeStorageDevice(path string, readOnly bool) (vz.VZStorageDeviceConfiguration, error) {
	attachment, err := createSystemDiskAttachment(path, readOnly)
	if err != nil {
		return vz.VZStorageDeviceConfiguration{}, fmt.Errorf("create disk attachment: %w", err)
	}
	device := vz.NewNVMExpressControllerDeviceConfigurationWithAttachment(attachment)
	if device.ID == 0 {
		return vz.VZStorageDeviceConfiguration{}, fmt.Errorf("create NVMe storage device")
	}
	device.Retain()
	return vz.VZStorageDeviceConfigurationFromID(device.ID), nil
}

func windowsUSBStorageDevice(path string, readOnly bool) (vz.VZStorageDeviceConfiguration, error) {
	url := foundation.NewURLFileURLWithPath(path)
	if url.ID == 0 {
		return vz.VZStorageDeviceConfiguration{}, fmt.Errorf("create file url for %s", path)
	}
	url.Retain()

	policy := DiskCacheDurable
	if readOnly {
		policy = DiskCacheReadOnly
	}
	attachment, err := newDiskAttachment(url, readOnly, policy)
	if err != nil {
		return vz.VZStorageDeviceConfiguration{}, err
	}
	attachment.Retain()

	device := vz.NewUSBMassStorageDeviceConfigurationWithAttachment(&attachment.VZStorageDeviceAttachment)
	if device.ID == 0 {
		return vz.VZStorageDeviceConfiguration{}, fmt.Errorf("create USB storage device")
	}
	device.Retain()
	return vz.VZStorageDeviceConfigurationFromID(device.ID), nil
}

func loadOrCreateWindowsMachineIdentifier() vz.VZGenericMachineIdentifier {
	machineIDPath := filepath.Join(vmDir, "windows-machine.id")
	if data, err := os.ReadFile(machineIDPath); err == nil && len(data) > 0 {
		nsData := createNSDataFromBytes(data)
		if nsData != 0 {
			nsDataObj := foundation.NSDataFromID(nsData)
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
		fmt.Printf("  warning: could not save Windows machine identifier: %v\n", err)
	}
	return machineID
}

func runWindowsVM() error {
	fmt.Println("=== Windows VM Runner (experimental) ===")
	if err := validateVMSettings(); err != nil {
		return err
	}

	if err := os.MkdirAll(vmDir, 0755); err != nil {
		return fmt.Errorf("create VM directory: %w", err)
	}
	saveHardwareConfig(vmDir)

	resolvedDiskPath := diskPath
	if resolvedDiskPath == "" {
		resolvedDiskPath = filepath.Join(vmDir, "windows-disk.img")
	}
	if _, err := os.Stat(resolvedDiskPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("windows disk image not found: %s\nrun 'cove install -windows -iso <path>' first", resolvedDiskPath)
		}
		return fmt.Errorf("stat windows disk image: %w", err)
	}

	fmt.Printf("Configuring VM: %d CPUs, %d GB RAM\n", cpuCount, memoryGB)
	config, err := buildWindowsVMConfiguration(resolvedDiskPath)
	if err != nil {
		return fmt.Errorf("build configuration: %w", err)
	}
	config.Retain()

	fmt.Println("Validating configuration...")
	if _, err := config.ValidateWithError(); err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}
	fmt.Println("  Configuration valid")
	updateSaveRestoreSupport(config)

	vmQueue := dispatch.QueueCreate("com.github.tmc.cove.windows.vmqueue")
	fmt.Println("Creating virtual machine...")
	vm := vz.NewVirtualMachineWithConfigurationQueue(&config, vmQueue)
	if vm.ID == 0 {
		return fmt.Errorf("failed to create virtual machine")
	}
	vm.Retain()

	fmt.Println("Starting virtual machine...")
	return startVMWithQueue(vm, vmQueue)
}

func installWindowsVM() error {
	fmt.Println("=== Windows VM Installer (experimental) ===")
	if err := validateVMSettings(); err != nil {
		return err
	}

	if err := os.MkdirAll(vmDir, 0755); err != nil {
		return fmt.Errorf("create VM directory: %w", err)
	}
	saveHardwareConfig(vmDir)

	windowsISO, err := ensureWindowsISO()
	if err != nil {
		return err
	}
	fmt.Printf("Using Windows ISO: %s\n", windowsISO)

	resolvedDiskPath := diskPath
	if resolvedDiskPath == "" {
		resolvedDiskPath = filepath.Join(vmDir, "windows-disk.img")
	}
	if _, err := os.Stat(resolvedDiskPath); os.IsNotExist(err) {
		fmt.Printf("Creating disk image: %s (%d GB)\n", resolvedDiskPath, diskSizeGB)
		if err := createDiskImage(resolvedDiskPath, diskSizeGB); err != nil {
			return fmt.Errorf("create disk image: %w", err)
		}
	}

	fmt.Printf("Configuring VM: %d CPUs, %d GB RAM\n", cpuCount, memoryGB)
	config, err := buildWindowsInstallConfiguration(resolvedDiskPath, windowsISO)
	if err != nil {
		return fmt.Errorf("build configuration: %w", err)
	}
	config.Retain()

	fmt.Println("Validating configuration...")
	if _, err := config.ValidateWithError(); err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}
	fmt.Println("  Configuration valid")

	vmQueue := dispatch.QueueCreate("com.github.tmc.cove.windows.install")
	fmt.Println("Creating virtual machine...")
	vm := vz.NewVirtualMachineWithConfigurationQueue(&config, vmQueue)
	if vm.ID == 0 {
		return fmt.Errorf("failed to create virtual machine")
	}
	vm.Retain()

	fmt.Println("Starting Windows installer...")
	return startVMWithQueue(vm, vmQueue)
}

func ensureWindowsISO() (string, error) {
	if isoPath != "" {
		if isURL(isoPath) {
			isoFile := filepath.Join(vmDir, "windows.iso")
			fmt.Printf("Downloading Windows ISO to: %s\n", isoFile)
			cmd := exec.Command("curl", "-L", "-C", "-", "-#", "-o", isoFile, isoPath)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				return "", fmt.Errorf("download windows ISO: %w", err)
			}
			return isoFile, nil
		}
		absPath, err := filepath.Abs(isoPath)
		if err != nil {
			return "", fmt.Errorf("resolve ISO path: %w", err)
		}
		if _, err := os.Stat(absPath); err != nil {
			return "", fmt.Errorf("windows ISO not found: %s", absPath)
		}
		return absPath, nil
	}

	cacheDir := vmconfig.CacheDir()
	searchPaths := []string{
		filepath.Join(vmDir, "windows.iso"),
		filepath.Join(cacheDir, "windows.iso"),
	}
	if entries, err := os.ReadDir(cacheDir); err == nil {
		for _, e := range entries {
			if e.IsDir() || strings.ToLower(filepath.Ext(e.Name())) != ".iso" {
				continue
			}
			if looksWindowsISOName(e.Name()) {
				searchPaths = append(searchPaths, filepath.Join(cacheDir, e.Name()))
			}
		}
	}
	for _, candidate := range searchPaths {
		if info, err := os.Stat(candidate); err == nil && info.Size() > 1*1024*1024*1024 {
			return candidate, nil
		}
	}

	return fetchWindowsISOFromESD(context.Background())
}

func looksWindowsISOName(name string) bool {
	name = strings.ToLower(name)
	return strings.Contains(name, "windows") ||
		strings.Contains(name, "win10") ||
		strings.Contains(name, "win11") ||
		strings.Contains(name, "clientconsumer") ||
		strings.Contains(name, "clientbusiness")
}

func fetchWindowsISOFromESD(ctx context.Context) (string, error) {
	cacheDir := vmconfig.CacheDir()
	result, err := esd.FetchLatest(ctx, esd.Options{
		CacheDir: cacheDir,
		Output:   os.Stderr,
	})
	if err != nil {
		return "", fmt.Errorf("fetch windows esd: %w", err)
	}

	isoPath := strings.TrimSuffix(result.Path, filepath.Ext(result.Path)) + ".iso"
	if info, err := os.Stat(isoPath); err == nil && info.Size() > 1*1024*1024*1024 {
		fmt.Printf("Using converted Windows ISO: %s (%.1f GB)\n", isoPath, float64(info.Size())/(1024*1024*1024))
		return isoPath, nil
	}

	fmt.Printf("Converting Windows ESD to ISO: %s\n", isoPath)
	if err := convertWindowsESDToISO(result.Path, isoPath); err != nil {
		return "", fmt.Errorf("windows esd downloaded to %s; install CrystalFetch or put esd2iso.sh, wimlib-imagex, and mkisofs in PATH, then rerun: %w", result.Path, err)
	}
	if info, err := os.Stat(isoPath); err != nil {
		return "", fmt.Errorf("stat converted ISO: %w", err)
	} else if info.Size() < 1*1024*1024*1024 {
		return "", fmt.Errorf("converted ISO too small: %s", isoPath)
	}
	return isoPath, nil
}

func convertWindowsESDToISO(esdPath, isoPath string) error {
	script, err := findESD2ISO()
	if err != nil {
		return err
	}
	cmd := exec.Command(script, "-v", esdPath, isoPath, windowsISOLabel(esdPath))
	cmd.Dir = filepath.Dir(isoPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "keepDownloads=1")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run esd2iso: %w", err)
	}
	return nil
}

func findESD2ISO() (string, error) {
	if script := strings.TrimSpace(os.Getenv("COVE_ESD2ISO")); script != "" {
		if _, err := os.Stat(script); err != nil {
			return "", fmt.Errorf("COVE_ESD2ISO: %w", err)
		}
		if err := checkESD2ISOTools(); err != nil {
			return "", err
		}
		return script, nil
	}
	for _, name := range []string{"esd2iso.sh", "w11arm_esd2iso"} {
		if script, err := exec.LookPath(name); err == nil {
			if err := checkESD2ISOTools(); err != nil {
				return "", err
			}
			return script, nil
		}
	}
	script := "/Applications/CrystalFetch.app/Contents/Resources/esd2iso.sh"
	if _, err := os.Stat(script); err == nil {
		if err := checkESD2ISOTools(); err != nil {
			return "", err
		}
		return script, nil
	}
	return "", fmt.Errorf("esd2iso.sh not found")
}

func checkESD2ISOTools() error {
	for _, name := range []string{"wimlib-imagex", "mkisofs"} {
		if _, err := exec.LookPath(name); err != nil {
			return fmt.Errorf("%s not found in PATH", name)
		}
	}
	return nil
}

func windowsISOLabel(esdPath string) string {
	parts := strings.Split(filepath.Base(esdPath), ".")
	label := strings.TrimSuffix(filepath.Base(esdPath), filepath.Ext(esdPath))
	if len(parts) >= 3 {
		label = strings.Join(parts[:3], ".")
	}
	if len(label) > 32 {
		label = label[:32]
	}
	return label
}

func ensureWindowsEFIBootImage(windowsISO string) (string, error) {
	bootImgPath := filepath.Join(vmDir, "efi-boot.img")
	if info, err := os.Stat(bootImgPath); err == nil && info.Size() > 0 {
		if isoInfo, err := os.Stat(windowsISO); err == nil && info.ModTime().After(isoInfo.ModTime()) {
			fmt.Printf("Using cached EFI boot image: %s\n", bootImgPath)
			return bootImgPath, nil
		}
	}

	fmt.Println("Creating EFI boot image from Windows ISO...")
	mountOut, err := exec.Command("hdiutil", "attach", windowsISO, "-nobrowse", "-readonly").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("mount windows ISO: %w: %s", err, mountOut)
	}
	mountLines := strings.Split(strings.TrimSpace(string(mountOut)), "\n")
	lastLine := mountLines[len(mountLines)-1]
	fields := strings.SplitN(strings.TrimSpace(lastLine), "\t", 3)
	isoDevice := strings.TrimSpace(fields[0])
	isoMount := strings.TrimSpace(fields[len(fields)-1])
	defer exec.Command("hdiutil", "detach", isoDevice).Run()

	if _, err := os.Stat(filepath.Join(isoMount, "efi")); err != nil {
		if _, err := os.Stat(filepath.Join(isoMount, "EFI")); err != nil {
			return "", fmt.Errorf("efi directory not found in windows ISO")
		}
	}

	if err := os.Remove(bootImgPath); err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("remove stale boot image: %w", err)
	}
	dmgPath := bootImgPath + ".dmg"
	if err := os.Remove(dmgPath); err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("remove stale boot image dmg: %w", err)
	}

	if out, err := exec.Command("hdiutil", "create",
		"-size", "1100m",
		"-fs", "MS-DOS FAT32",
		"-volname", "WINBOOT",
		"-layout", "GPTSPUD",
		"-o", dmgPath,
	).CombinedOutput(); err != nil {
		return "", fmt.Errorf("create FAT32 disk image: %w: %s", err, out)
	}

	attachOut, err := exec.Command("hdiutil", "attach", dmgPath, "-nobrowse").CombinedOutput()
	if err != nil {
		_ = os.Remove(dmgPath)
		return "", fmt.Errorf("attach FAT32 image: %w: %s", err, attachOut)
	}
	dmgDevice, dmgMount, err := parseHdiutilAttachOutput(string(attachOut))
	if err != nil {
		return "", err
	}
	defer exec.Command("hdiutil", "detach", dmgDevice).Run()

	if out, err := exec.Command("rsync", "-rlt", "--no-perms", "--chmod=ugo=rwX",
		"--exclude", "install.wim",
		"--exclude", "boot.catalog",
		isoMount+"/", dmgMount+"/",
	).CombinedOutput(); err != nil {
		return "", fmt.Errorf("copy installer files: %w: %s", err, out)
	}

	installUEFIShellShim(dmgMount)

	if out, err := exec.Command("hdiutil", "detach", dmgDevice).CombinedOutput(); err != nil {
		return "", fmt.Errorf("detach FAT32 image: %w: %s", err, out)
	}
	if err := os.Rename(dmgPath, bootImgPath); err != nil {
		return "", fmt.Errorf("rename boot image: %w", err)
	}
	return bootImgPath, nil
}

func parseHdiutilAttachOutput(output string) (device, mount string, err error) {
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		fields := strings.SplitN(strings.TrimSpace(line), "\t", 3)
		if len(fields) == 0 {
			continue
		}
		dev := strings.TrimSpace(fields[0])
		if !strings.HasPrefix(dev, "/dev/disk") {
			continue
		}
		if !strings.Contains(dev[len("/dev/disk"):], "s") {
			device = dev
		}
		if len(fields) >= 3 {
			mp := strings.TrimSpace(fields[2])
			if strings.HasPrefix(mp, "/Volumes/") {
				mount = mp
			}
		}
	}
	if device == "" {
		return "", "", fmt.Errorf("could not find device in hdiutil output: %s", output)
	}
	if mount == "" {
		return "", "", fmt.Errorf("fat32 partition not auto-mounted: %s", output)
	}
	return device, mount, nil
}

func installUEFIShellShim(mount string) {
	shellPath := "/tmp/shellaa64.efi"
	if _, err := os.Stat(shellPath); err != nil {
		fmt.Printf("  UEFI Shell not found at %s; using Windows Boot Manager directly\n", shellPath)
		return
	}

	var bootPath string
	for _, p := range []string{
		filepath.Join(mount, "efi/boot/bootaa64.efi"),
		filepath.Join(mount, "EFI/Boot/bootaa64.efi"),
		filepath.Join(mount, "EFI/Boot/BOOTAA64.EFI"),
	} {
		if _, err := os.Stat(p); err == nil {
			bootPath = p
			break
		}
	}
	if bootPath == "" {
		return
	}

	bootmgr := filepath.Join(filepath.Dir(bootPath), "bootmgfw.efi")
	if err := os.Rename(bootPath, bootmgr); err != nil {
		fmt.Printf("  warning: rename Windows Boot Manager: %v\n", err)
		return
	}
	data, err := os.ReadFile(shellPath)
	if err != nil {
		fmt.Printf("  warning: read UEFI Shell: %v\n", err)
		return
	}
	if err := os.WriteFile(bootPath, data, 0644); err != nil {
		fmt.Printf("  warning: install UEFI Shell: %v\n", err)
		return
	}

	startup := `@echo -off
echo "=== UEFI Shell Boot Shim ==="
echo "Chainloading Windows Boot Manager..."
FS0:\efi\boot\bootmgfw.efi
echo "bootmgfw.efi returned (exit code: %lasterror%)"
echo "Trying alternate path..."
FS0:\efi\microsoft\boot\bootmgfw.efi
echo "All boot attempts failed."
stall 10000000
`
	if err := os.WriteFile(filepath.Join(mount, "startup.nsh"), []byte(startup), 0644); err != nil {
		fmt.Printf("  warning: write startup.nsh: %v\n", err)
	}
}
