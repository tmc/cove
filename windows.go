package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tmc/apple/dispatch"
	"github.com/tmc/apple/foundation"
	vz "github.com/tmc/apple/virtualization"
	configx "github.com/tmc/apple/x/vzkit/config"
	displayx "github.com/tmc/apple/x/vzkit/display"
	serialx "github.com/tmc/apple/x/vzkit/exp/serial"
	"github.com/tmc/apple/x/vzkit/framebuffer"
	platformx "github.com/tmc/apple/x/vzkit/platform"
	storagex "github.com/tmc/apple/x/vzkit/storage"
	windowsconfig "github.com/tmc/apple/x/vzkit/windowsconfig"
	"github.com/tmc/cove/internal/guestplan"
	"github.com/tmc/cove/internal/vmconfig"
	"github.com/tmc/cove/internal/vmrun"
	winsetup "github.com/tmc/cove/internal/windows"
	"github.com/tmc/cove/internal/windows/esd"
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
	rc := vmrunRunConfig(vmrun.GuestWindows)

	config, err := buildWindowsBaseConfiguration()
	if err != nil {
		return config, err
	}

	storage, err := windowsNVMeStorageDevice(diskImagePath, false)
	if err != nil {
		return config, err
	}
	storageDevices := []vz.VZStorageDeviceConfiguration{storage}

	if rc.ISOPath != "" {
		isoStorage, err := windowsUSBStorageDevice(resolvePath(rc.ISOPath), true)
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
	hc := vmrunHostConfig()

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
	autounattendISO, err := winsetup.CreateAutounattendISO(hc.VMDir, winsetup.DefaultProvisionConfig())
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
	rc := vmrunRunConfig(vmrun.GuestWindows)
	hc := vmrunHostConfig()

	plan, err := guestplan.Windows(rc, hc)
	if err != nil {
		return vz.VZVirtualMachineConfiguration{}, err
	}
	netConfig, err := ParseNetworkMode(plan.Network.Mode)
	if err != nil {
		return vz.VZVirtualMachineConfiguration{}, fmt.Errorf("parse network mode: %w", err)
	}
	builderNetwork := windowsconfig.Network{Config: netConfig}
	if netConfig.Mode == NetworkModeFileHandle || netConfig.Mode == NetworkModeNone {
		builderNetwork = windowsconfig.Network{}
	}

	graphicsMode, err := parseWindowsGraphicsMode(rc.WindowsGraphicsMode)
	if err != nil {
		return vz.VZVirtualMachineConfiguration{}, err
	}
	var displayConfigs []displayx.Config
	if graphicsMode == windowsGraphicsVirtio {
		displayConfigs = make([]displayx.Config, 0, len(plan.Display))
		for _, d := range plan.Display {
			displayConfigs = append(displayConfigs, displayx.Config{Width: d.Width, Height: d.Height, PPI: d.PPI})
		}
	}
	config, err := windowsconfig.Build(windowsconfig.Config{
		CPUCount:      plan.CPUCount,
		MemoryGB:      plan.MemoryGB,
		Display:       displayConfigs,
		Network:       builderNetwork,
		Keyboard:      true,
		Pointing:      true,
		Entropy:       true,
		Sound:         true,
		USBController: true,
		MemoryBalloon: true,
		Socket:        sandboxAllowsVsock(),
	})
	if err != nil {
		return config, fmt.Errorf("build windows device config: %w", err)
	}

	platformConfig := vz.NewVZGenericPlatformConfiguration()
	machineID := loadOrCreateWindowsMachineIdentifier()
	platformConfig.SetMachineIdentifier(&machineID)
	config.SetPlatform(&platformConfig.VZPlatformConfiguration)

	bootloader, err := createEFIBootLoader()
	if err != nil {
		return config, err
	}
	config.SetBootLoader(&bootloader.VZBootLoader)

	switch graphicsMode {
	case windowsGraphicsLinearFramebuffer:
		if err := setWindowsLinearFramebufferGraphicsDevice(config); err != nil {
			return config, err
		}
	case windowsGraphicsVirtio:
		fmt.Println("  Windows graphics: VirtIO")
	}

	if netConfig.Mode == NetworkModeFileHandle {
		networkDeviceConfig, err := CreateNetworkDeviceConfiguration(netConfig)
		if err != nil {
			return config, fmt.Errorf("create network device: %w", err)
		}
		configx.SetNetworkDevices(config, networkDeviceConfig)
	}

	serialConfig, err := createWindowsSerialConsoleConfig()
	if err != nil {
		return config, err
	}
	if serialConfig.ID != 0 {
		configx.SetSerialPorts(config, serialConfig)
		fmt.Println("  Serial console attached")
	}

	if err := applyPrivateVMConfiguration(config); err != nil {
		return config, err
	}
	return config, nil
}

func createWindowsSerialConsoleConfig() (vz.VZSerialPortConfiguration, error) {
	rc := vmrunRunConfig(vmrun.GuestWindows)
	mode, err := parseWindowsSerialMode(rc.WindowsSerialMode)
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
		serialConfig, err := serialx.New(serialx.PL011, attachment.VZSerialPortAttachment)
		if err != nil {
			return vz.VZSerialPortConfiguration{}, err
		}
		fmt.Println("  Windows serial: PL011")
		return serialConfig, nil
	case windowsSerial16550:
		serialConfig, err := serialx.New(serialx.UART, attachment.VZSerialPortAttachment)
		if err != nil {
			return vz.VZSerialPortConfiguration{}, err
		}
		fmt.Println("  Windows serial: 16550")
		return serialConfig, nil
	default:
		return vz.VZSerialPortConfiguration{}, fmt.Errorf("unsupported Windows serial mode: %s", mode)
	}
}

func setWindowsLinearFramebufferGraphicsDevice(config vz.VZVirtualMachineConfiguration) error {
	width, height := windowsDisplaySize()
	err := framebuffer.SetLinearFramebufferGraphicsDevice(config, framebuffer.LinearFramebufferConfig{
		Width:  width,
		Height: height,
	})
	if err != nil {
		return err
	}
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
	device, err := storagex.CreateNVMeDeviceWithAttachment(attachment)
	if err != nil {
		return vz.VZStorageDeviceConfiguration{}, err
	}
	return vz.VZStorageDeviceConfigurationFromID(device.ID), nil
}

func windowsUSBStorageDevice(path string, readOnly bool) (vz.VZStorageDeviceConfiguration, error) {
	url := foundation.NewURLFileURLWithPath(path)
	if url.ID == 0 {
		return vz.VZStorageDeviceConfiguration{}, fmt.Errorf("create file url for %s", path)
	}
	url.Retain()

	policy := storagex.CacheDurable
	if readOnly {
		policy = storagex.CacheReadOnly
	}
	attachment, err := newDiskAttachment(url, readOnly, policy)
	if err != nil {
		return vz.VZStorageDeviceConfiguration{}, err
	}
	attachment.Retain()

	device, err := storagex.CreateUSBMassStorageDeviceWithAttachment(attachment.VZStorageDeviceAttachment)
	if err != nil {
		return vz.VZStorageDeviceConfiguration{}, err
	}
	return vz.VZStorageDeviceConfigurationFromID(device.ID), nil
}

func loadOrCreateWindowsMachineIdentifier() vz.VZGenericMachineIdentifier {
	machineIDPath := filepath.Join(vmDir, "windows-machine.id")
	machineID, created, err := platformx.LoadOrCreateGenericMachineIdentifier(machineIDPath)
	if err != nil {
		fmt.Printf("  warning: could not save Windows machine identifier: %v\n", err)
	}
	if created {
		fmt.Println("  Created new Windows machine identifier")
	} else {
		fmt.Println("  Loaded existing Windows machine identifier")
	}
	return machineID
}

func runWindowsVM() error {
	rc := vmrunRunConfig(vmrun.GuestWindows)
	hc := vmrunHostConfig()
	fmt.Println("=== Windows VM Runner (experimental) ===")
	if err := validateVMSettings(); err != nil {
		return err
	}

	if err := os.MkdirAll(hc.VMDir, 0755); err != nil {
		return fmt.Errorf("create VM directory: %w", err)
	}
	saveHardwareConfig(hc.VMDir)

	resolvedDiskPath := rc.DiskPath
	if resolvedDiskPath == "" {
		resolvedDiskPath = filepath.Join(hc.VMDir, "windows-disk.img")
	}
	if _, err := os.Stat(resolvedDiskPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("windows disk image not found: %s\nrun 'cove install -windows -iso <path>' first", resolvedDiskPath)
		}
		return fmt.Errorf("stat windows disk image: %w", err)
	}

	fmt.Printf("Configuring VM: %d CPUs, %d GB RAM\n", rc.CPUCount, rc.MemoryGB)
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
	rc := vmrunRunConfig(vmrun.GuestWindows)
	hc := vmrunHostConfig()
	fmt.Println("=== Windows VM Installer (experimental) ===")
	if err := validateVMSettings(); err != nil {
		return err
	}

	if err := os.MkdirAll(hc.VMDir, 0755); err != nil {
		return fmt.Errorf("create VM directory: %w", err)
	}
	saveHardwareConfig(hc.VMDir)
	persistInstallQuota(hc.VMDir)
	if err := applyInstallDiskQuota(hc.VMDir); err != nil {
		return err
	}

	windowsISO, err := ensureWindowsISO()
	if err != nil {
		return err
	}
	fmt.Printf("Using Windows ISO: %s\n", windowsISO)

	resolvedDiskPath := rc.DiskPath
	if resolvedDiskPath == "" {
		resolvedDiskPath = filepath.Join(hc.VMDir, "windows-disk.img")
	}
	if _, err := os.Stat(resolvedDiskPath); os.IsNotExist(err) {
		fmt.Printf("Creating disk image: %s (%d GB)\n", resolvedDiskPath, rc.DiskSizeGB)
		if err := createInstallDiskImage(resolvedDiskPath, rc.DiskSizeGB); err != nil {
			return fmt.Errorf("create disk image: %w", err)
		}
	}

	fmt.Printf("Configuring VM: %d CPUs, %d GB RAM\n", rc.CPUCount, rc.MemoryGB)
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
		return "", "", fmt.Errorf("%w: %s", ErrHdiutilNoDevice, output)
	}
	if mount == "" {
		return "", "", fmt.Errorf("%w: %s", ErrHdiutilNoMountPoint, output)
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
