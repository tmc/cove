//go:build ignore

// Windows ARM64 VM installation with autounattend.xml.
//
// STATUS: Blocked — see windows.go for details on the GOP/framebuffer issue.
//
// This implements unattended Windows 11 ARM64 installation using:
//   - autounattend.xml on a secondary ISO for fully automated setup
//   - VirtIO ARM64 driver ISO so Windows Setup can find disk/network drivers
//   - SPICE guest tools .exe bundled for silent post-install via FirstLogonCommands
//   - UEFI Shell shim as boot loader (Apple's EFI loads shell, shell chainloads bootmgr)
//
// The FAT32 boot image approach is proven to work for file access:
//   - GPT+FAT32 via hdiutil create -layout GPTSPUD
//   - rsync all files except install.wim (>4GB FAT32 limit)
//   - Attach as USB mass storage device
//   - UEFI Shell confirms all files (boot.wim, BCD, bootaa64.efi) are readable
//
// Storage layout during installation (5 devices):
//  1. Main disk (VirtIO block, read-write) — Windows installs here
//  2. EFI boot image (USB) — FAT32 with installer files (minus install.wim)
//  3. Windows installer ISO (USB) — Setup reads install.wim from here
//  4. VirtIO drivers ISO (USB) — Setup loads drivers from here
//  5. Autounattend ISO (USB) — Setup reads autounattend.xml
//
// TODO: The storage layout, autounattend.xml generation, and FAT32 image creation
// are all working. Once the GOP issue is resolved, this should "just work" — the
// only missing piece is display output from the Windows Boot Manager.
package windows

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tmc/apple/dispatch"
	"github.com/tmc/apple/foundation"
	vz "github.com/tmc/apple/virtualization"
)

// WindowsProvisionConfig holds configuration for Windows VM provisioning.
type WindowsProvisionConfig struct {
	Username string
	Password string
	Hostname string
	Locale   string
	TimeZone string
	Edition  string // Pro, Home, Enterprise
}

// DefaultWindowsProvisionConfig returns default provisioning settings.
func DefaultWindowsProvisionConfig() WindowsProvisionConfig {
	return WindowsProvisionConfig{
		Username: "User",
		Password: "password",
		Hostname: "WIN-VM",
		Locale:   "en-US",
		TimeZone: "UTC",
		Edition:  "Windows 11 Pro",
	}
}

// installWindowsVM performs automated Windows ARM64 installation.
func installWindowsVM() error {
	fmt.Println("=== Windows VM Installer ===")

	if err := validateVMSettings(); err != nil {
		return err
	}

	if err := os.MkdirAll(vmDir, 0755); err != nil {
		return fmt.Errorf("create VM directory: %w", err)
	}

	// Configure provisioning
	provConfig := DefaultWindowsProvisionConfig()
	if provisionUser != "" {
		provConfig.Username = provisionUser
	}
	if provisionPassword != "" {
		provConfig.Password = provisionPassword
	}

	// Ensure Windows ISO
	windowsISO, err := ensureWindowsISO()
	if err != nil {
		return fmt.Errorf("ensure Windows ISO: %w", err)
	}
	fmt.Printf("Using Windows ISO: %s\n", windowsISO)

	// Ensure VirtIO drivers ISO
	virtioISO, err := ensureVirtIODriversISO()
	if err != nil {
		return fmt.Errorf("ensure VirtIO drivers: %w", err)
	}
	fmt.Printf("Using VirtIO drivers: %s\n", virtioISO)

	// Ensure SPICE guest tools
	guestToolsExe, err := ensureWindowsGuestTools()
	if err != nil {
		fmt.Printf("warning: could not download SPICE guest tools: %v\n", err)
		fmt.Println("  Clipboard sharing will need manual guest tools installation.")
		guestToolsExe = ""
	}

	// Create autounattend ISO
	autounattendISO, err := createAutounattendISO(provConfig, guestToolsExe)
	if err != nil {
		return fmt.Errorf("create autounattend ISO: %w", err)
	}
	fmt.Printf("Created autounattend ISO: %s\n", autounattendISO)

	// Resolve disk path
	resolvedDiskPath := diskPath
	if resolvedDiskPath == "" {
		resolvedDiskPath = filepath.Join(vmDir, "windows-disk.img")
	}

	// Create disk image
	if _, err := os.Stat(resolvedDiskPath); os.IsNotExist(err) {
		fmt.Printf("Creating disk image: %s (%d GB)\n", resolvedDiskPath, diskSizeGB)
		if err := createDiskImage(resolvedDiskPath, diskSizeGB); err != nil {
			return fmt.Errorf("create disk image: %w", err)
		}
	}

	// Build VM configuration with all ISOs
	fmt.Printf("Configuring VM: %d CPUs, %d GB RAM\n", cpuCount, memoryGB)
	config, err := buildWindowsInstallConfiguration(resolvedDiskPath, windowsISO, virtioISO, autounattendISO)
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

	// Create dispatch queue
	vmQueue := dispatch.QueueCreate("com.appledocs.vz.windows.install")

	// Create VM
	fmt.Println("Creating virtual machine...")
	vm := vz.NewVirtualMachineWithConfigurationQueue(&config, vmQueue)
	if vm.ID == 0 {
		return fmt.Errorf("failed to create virtual machine")
	}
	vm.Retain()

	// Print installation instructions
	fmt.Println()
	fmt.Println("=== Windows Installation Starting ===")
	fmt.Println("The installation will proceed automatically using autounattend.xml.")
	fmt.Printf("  Username: %s\n", provConfig.Username)
	fmt.Printf("  Hostname: %s\n", provConfig.Hostname)
	fmt.Println()
	fmt.Println("Note: Windows installation takes 15-30 minutes.")
	fmt.Println("The VM will reboot several times during installation.")
	fmt.Println()
	fmt.Println("After installation completes:")
	fmt.Printf("  1. The VM will boot to the Windows desktop\n")
	fmt.Printf("  2. Stop this process (Ctrl+C)\n")
	fmt.Printf("  3. Run: ./cove run -windows\n")
	fmt.Println()

	// Start VM
	fmt.Println("Starting installation...")
	return startVMWithQueue(vm, vmQueue)
}

// buildWindowsInstallConfiguration builds a VM config for Windows installation.
func buildWindowsInstallConfiguration(diskPath, windowsISO, virtioISO, autounattendISO string) (vz.VZVirtualMachineConfiguration, error) {
	config := vz.NewVZVirtualMachineConfiguration()

	// CPU and memory (Windows needs more resources for installation)
	effectiveCPU := cpuCount
	if effectiveCPU < 2 {
		effectiveCPU = 2
	}
	effectiveMemory := memoryGB
	if effectiveMemory < 4 {
		effectiveMemory = 4
	}
	config.SetCPUCount(effectiveCPU)
	config.SetMemorySize(effectiveMemory * 1024 * 1024 * 1024)

	// Platform configuration (generic)
	platformConfig := vz.NewVZGenericPlatformConfiguration()
	machineID := loadOrCreateWindowsMachineIdentifier()
	platformConfig.SetMachineIdentifier(&machineID)
	config.SetPlatform(&platformConfig.VZPlatformConfiguration)

	// EFI boot loader
	bootloader, err := createEFIBootLoader()
	if err != nil {
		return config, err
	}
	config.SetBootLoader(&bootloader.VZBootLoader)

	// Storage layout (order matters for EFI boot priority):
	// 1. EFI boot image (VirtIO block, read-only) — FAT32 with full installer
	// 2. Main disk (VirtIO block, read-write) — install target
	// 3. Windows ISO (USB, read-only) — Setup reads install.wim from here
	// 4. VirtIO drivers ISO (USB, read-only) — Setup loads drivers
	// 5. Autounattend ISO (USB, read-only) — Setup reads autounattend.xml
	//
	// Apple's EFI firmware can read FAT32 but not UDF (Windows ISOs use UDF).
	// The BCD in the EFI boot files references \sources\boot.wim on the same disk,
	// so the FAT32 image must contain all installer files (not just EFI/boot/).

	// Storage layout:
	// 1. Main disk (VirtIO block, read-write) — Windows installs here
	// 2. EFI boot image (USB mass storage, read-only) — ISO9660 with installer files
	// 3. Windows ISO (USB mass storage, read-only) — Setup reads install.wim
	// 4. VirtIO drivers ISO (USB mass storage, read-only) — Setup loads drivers
	// 5. Autounattend ISO (USB mass storage, read-only) — Setup reads autounattend.xml

	storageDevices := []vz.VZStorageDeviceConfiguration{}

	// Main disk (VirtIO block, read-write)
	diskURL := foundation.NewURLFileURLWithPath(diskPath)
	diskAttachment, err := newDiskAttachment(&diskURL, false, diskCacheEphemeral)
	if err != nil {
		return config, fmt.Errorf("create disk attachment: %w", err)
	}
	diskAttachment.Retain()
	diskStorage := vz.NewVirtioBlockDeviceConfigurationWithAttachment(&diskAttachment.VZStorageDeviceAttachment)
	diskStorage.Retain()
	storageDevices = append(storageDevices, vz.VZStorageDeviceConfigurationFromID(diskStorage.ID))
	fmt.Printf("  Disk: VirtIO block (read-write) %s\n", diskPath)

	// EFI boot image (USB mass storage) — ISO9660 with Windows installer files
	efiBootImg, err := ensureWindowsEFIBootImage(windowsISO)
	if err != nil {
		return config, fmt.Errorf("create EFI boot image: %w", err)
	}
	efiBootURL := foundation.NewURLFileURLWithPath(efiBootImg)
	efiBootAttachment, err := newDiskAttachment(&efiBootURL, true, diskCacheReadOnly)
	if err != nil {
		return config, fmt.Errorf("create EFI boot attachment: %w", err)
	}
	efiBootAttachment.Retain()
	efiBootUSB := vz.NewUSBMassStorageDeviceConfigurationWithAttachment(&efiBootAttachment.VZStorageDeviceAttachment)
	efiBootUSB.Retain()
	storageDevices = append(storageDevices, vz.VZStorageDeviceConfigurationFromID(efiBootUSB.ID))
	fmt.Printf("  Boot: USB mass storage (read-only) %s\n", efiBootImg)

	// Windows ISO (USB mass storage, read-only) — Setup reads install.wim from here
	winISOURL := foundation.NewURLFileURLWithPath(windowsISO)
	winISOAttachment, err := newDiskAttachment(&winISOURL, true, diskCacheReadOnly)
	if err != nil {
		return config, fmt.Errorf("create Windows ISO attachment: %w", err)
	}
	winISOAttachment.Retain()
	winISOUSB := vz.NewUSBMassStorageDeviceConfigurationWithAttachment(&winISOAttachment.VZStorageDeviceAttachment)
	winISOUSB.Retain()
	storageDevices = append(storageDevices, vz.VZStorageDeviceConfigurationFromID(winISOUSB.ID))
	fmt.Printf("  Windows ISO: USB mass storage (read-only) %s\n", windowsISO)

	// VirtIO drivers ISO (USB, read-only)
	if virtioISO != "" {
		virtioISOURL := foundation.NewURLFileURLWithPath(virtioISO)
		virtioISOAttachment, err := newDiskAttachment(&virtioISOURL, true, diskCacheReadOnly)
		if err != nil {
			fmt.Printf("warning: could not attach VirtIO drivers ISO: %v\n", err)
		} else {
			virtioISOAttachment.Retain()
			virtioISOUSB := vz.NewUSBMassStorageDeviceConfigurationWithAttachment(&virtioISOAttachment.VZStorageDeviceAttachment)
			virtioISOUSB.Retain()
			storageDevices = append(storageDevices, vz.VZStorageDeviceConfigurationFromID(virtioISOUSB.ID))
			fmt.Printf("  VirtIO ISO: USB mass storage (read-only) %s\n", virtioISO)
		}
	}

	// Autounattend ISO (USB, read-only)
	if autounattendISO != "" {
		autoISOURL := foundation.NewURLFileURLWithPath(autounattendISO)
		autoISOAttachment, err := newDiskAttachment(&autoISOURL, true, diskCacheReadOnly)
		if err != nil {
			fmt.Printf("warning: could not attach autounattend ISO: %v\n", err)
		} else {
			autoISOAttachment.Retain()
			autoISOUSB := vz.NewUSBMassStorageDeviceConfigurationWithAttachment(&autoISOAttachment.VZStorageDeviceAttachment)
			autoISOUSB.Retain()
			storageDevices = append(storageDevices, vz.VZStorageDeviceConfigurationFromID(autoISOUSB.ID))
			fmt.Printf("  Autounattend ISO: USB mass storage (read-only) %s\n", autounattendISO)
		}
	}

	fmt.Printf("  Storage devices: %d total\n", len(storageDevices))
	config.SetStorageDevices(storageDevices)

	// Graphics
	graphicsConfig := vz.NewVZVirtioGraphicsDeviceConfiguration()
	scanout := vz.NewVirtioGraphicsScanoutConfigurationWithWidthInPixelsHeightInPixels(1920, 1200)
	if scanout.ID != 0 {
		setVirtioScanouts(graphicsConfig, scanout)
	}
	setVirtioGraphicsDevices(config, graphicsConfig)

	// Network
	natAttachment := vz.NewVZNATNetworkDeviceAttachment()
	networkConfig := vz.NewVZVirtioNetworkDeviceConfiguration()
	networkConfig.SetAttachment(&natAttachment.VZNetworkDeviceAttachment)
	macAddr := vz.GetVZMACAddressClass().RandomLocallyAdministeredAddress()
	if macAddr.ID != 0 {
		networkConfig.SetMACAddress(&macAddr)
	}
	setNetworkDevices(config, networkConfig)

	// Keyboard
	keyboardConfig := vz.NewVZUSBKeyboardConfiguration()
	setKeyboards(config, keyboardConfig)

	// Pointing device
	pointingConfig := vz.NewVZUSBScreenCoordinatePointingDeviceConfiguration()
	setPointingDevices(config, []vz.IVZPointingDeviceConfiguration{pointingConfig})

	// Entropy
	entropyConfig := vz.NewVZVirtioEntropyDeviceConfiguration()
	setEntropyDevices(config, entropyConfig)

	// Audio
	audioConfig := vz.NewVZVirtioSoundDeviceConfiguration()
	setAudioDevices(config, audioConfig)

	// Serial console
	serialConfig := createSerialConsoleConfig()
	if serialConfig.ID != 0 {
		setSerialPorts(config, serialConfig)
	}

	return config, nil
}

// ensureWindowsISO verifies a user-provided Windows ISO exists.
// Unlike Linux, we cannot auto-download Windows ISOs due to licensing.
func ensureWindowsISO() (string, error) {
	if isoPath != "" {
		if isURL(isoPath) {
			isoFile := filepath.Join(vmDir, "windows.iso")
			fmt.Printf("Downloading Windows ISO to: %s\n", isoFile)
			cmd := exec.Command("curl", "-L", "-C", "-", "-#", "-o", isoFile, isoPath)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				return "", fmt.Errorf("download failed: %w", err)
			}
			return isoFile, nil
		}
		absPath, err := filepath.Abs(isoPath)
		if err != nil {
			return "", fmt.Errorf("resolve ISO path: %w", err)
		}
		if _, err := os.Stat(absPath); err != nil {
			return "", fmt.Errorf("iso file not found: %s", absPath)
		}
		return absPath, nil
	}

	// Search common locations for a Windows ISO
	home, _ := os.UserHomeDir()
	searchPaths := []string{
		filepath.Join(vmDir, "windows.iso"),
		filepath.Join(home, ".vz", "cache", "windows.iso"),
	}
	// Also check for any ISO matching Windows naming in ~/.vz/cache/
	cacheDir := filepath.Join(home, ".vz", "cache")
	if entries, err := os.ReadDir(cacheDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() && filepath.Ext(e.Name()) == ".iso" {
				searchPaths = append(searchPaths, filepath.Join(cacheDir, e.Name()))
			}
		}
	}
	for _, candidate := range searchPaths {
		if info, err := os.Stat(candidate); err == nil && info.Size() > 1*1024*1024*1024 {
			fmt.Printf("Using existing ISO: %s (%.1f GB)\n", candidate, float64(info.Size())/(1024*1024*1024))
			return candidate, nil
		}
	}

	return "", fmt.Errorf(`no Windows ISO specified

Download Windows 11 ARM64 from:
  https://www.microsoft.com/en-us/software-download/windows11arm64
  Or use CrystalFetch: https://github.com/nicksulker/CrystalFetch

Then run:
  cove install -windows -iso /path/to/Win11_ARM64.iso`)
}

// createAutounattendISO creates an ISO containing autounattend.xml and
// optionally the SPICE guest tools installer for silent post-install.
func createAutounattendISO(config WindowsProvisionConfig, guestToolsExe string) (string, error) {
	// Create temporary directory for autounattend files
	autoDir := filepath.Join(vmDir, "autounattend")
	if err := os.MkdirAll(autoDir, 0755); err != nil {
		return "", fmt.Errorf("create autounattend directory: %w", err)
	}

	// Generate autounattend.xml
	xmlContent := generateAutounattendXML(config, guestToolsExe != "")
	xmlPath := filepath.Join(autoDir, "autounattend.xml")
	if err := os.WriteFile(xmlPath, []byte(xmlContent), 0644); err != nil {
		return "", fmt.Errorf("write autounattend.xml: %w", err)
	}

	// Copy guest tools .exe if available
	if guestToolsExe != "" {
		data, err := os.ReadFile(guestToolsExe)
		if err != nil {
			fmt.Printf("warning: could not read guest tools: %v\n", err)
		} else {
			destPath := filepath.Join(autoDir, "spice-guest-tools.exe")
			if err := os.WriteFile(destPath, data, 0644); err != nil {
				fmt.Printf("warning: could not copy guest tools: %v\n", err)
			}
		}
	}

	// Create ISO using hdiutil
	isoPath := filepath.Join(vmDir, "autounattend.iso")
	os.Remove(isoPath)
	cmd := exec.Command("hdiutil", "makehybrid",
		"-o", isoPath,
		"-hfs",
		"-joliet",
		"-iso",
		"-default-volume-name", "OEMDRV",
		"-iso-volume-name", "OEMDRV",
		"-joliet-volume-name", "OEMDRV",
		autoDir,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("create ISO: %w: %s", err, output)
	}

	return isoPath, nil
}

// generateAutounattendXML creates the Windows autounattend.xml for unattended installation.
func generateAutounattendXML(config WindowsProvisionConfig, includeGuestTools bool) string {
	// FirstLogonCommands to install SPICE guest tools silently
	firstLogonCmds := ""
	if includeGuestTools {
		// The autounattend ISO is mounted as a removable drive.
		// We search all drive letters for the installer.
		firstLogonCmds = `
            <FirstLogonCommands>
                <SynchronousCommand wcm:action="add">
                    <Order>1</Order>
                    <Description>Install SPICE Guest Tools</Description>
                    <CommandLine>cmd /c "for %%d in (D E F G H) do if exist %%d:\spice-guest-tools.exe %%d:\spice-guest-tools.exe /S"</CommandLine>
                </SynchronousCommand>
            </FirstLogonCommands>`
	}

	return fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<unattend xmlns="urn:schemas-microsoft-com:unattend"
          xmlns:wcm="http://schemas.microsoft.com/WMIConfig/2002/State">

    <!-- Pass 1: windowsPE - partition disk, load VirtIO drivers -->
    <settings pass="windowsPE">
        <component name="Microsoft-Windows-International-Core-WinPE"
                   processorArchitecture="arm64"
                   publicKeyToken="31bf3856ad364e35"
                   language="neutral"
                   versionScope="nonSxS">
            <SetupUILanguage>
                <UILanguage>%[1]s</UILanguage>
            </SetupUILanguage>
            <InputLocale>%[1]s</InputLocale>
            <SystemLocale>%[1]s</SystemLocale>
            <UILanguage>%[1]s</UILanguage>
            <UserLocale>%[1]s</UserLocale>
        </component>

        <component name="Microsoft-Windows-Setup"
                   processorArchitecture="arm64"
                   publicKeyToken="31bf3856ad364e35"
                   language="neutral"
                   versionScope="nonSxS">

            <!-- Load VirtIO drivers from attached ISO -->
            <DriverPaths>
                <PathAndCredentials wcm:action="add" wcm:keyValue="1">
                    <Path>D:\</Path>
                </PathAndCredentials>
                <PathAndCredentials wcm:action="add" wcm:keyValue="2">
                    <Path>E:\</Path>
                </PathAndCredentials>
                <PathAndCredentials wcm:action="add" wcm:keyValue="3">
                    <Path>F:\</Path>
                </PathAndCredentials>
            </DriverPaths>

            <DiskConfiguration>
                <Disk wcm:action="add">
                    <DiskID>0</DiskID>
                    <WillWipeDisk>true</WillWipeDisk>
                    <CreatePartitions>
                        <!-- EFI System Partition -->
                        <CreatePartition wcm:action="add">
                            <Order>1</Order>
                            <Size>260</Size>
                            <Type>EFI</Type>
                        </CreatePartition>
                        <!-- Microsoft Reserved -->
                        <CreatePartition wcm:action="add">
                            <Order>2</Order>
                            <Size>16</Size>
                            <Type>MSR</Type>
                        </CreatePartition>
                        <!-- Windows -->
                        <CreatePartition wcm:action="add">
                            <Order>3</Order>
                            <Extend>true</Extend>
                            <Type>Primary</Type>
                        </CreatePartition>
                    </CreatePartitions>
                    <ModifyPartitions>
                        <ModifyPartition wcm:action="add">
                            <Order>1</Order>
                            <PartitionID>1</PartitionID>
                            <Format>FAT32</Format>
                            <Label>EFI</Label>
                        </ModifyPartition>
                        <ModifyPartition wcm:action="add">
                            <Order>2</Order>
                            <PartitionID>3</PartitionID>
                            <Format>NTFS</Format>
                            <Label>Windows</Label>
                        </ModifyPartition>
                    </ModifyPartitions>
                </Disk>
            </DiskConfiguration>

            <ImageInstall>
                <OSImage>
                    <InstallTo>
                        <DiskID>0</DiskID>
                        <PartitionID>3</PartitionID>
                    </InstallTo>
                </OSImage>
            </ImageInstall>

            <UserData>
                <AcceptEula>true</AcceptEula>
                <FullName>%[2]s</FullName>
                <Organization>VM</Organization>
            </UserData>
        </component>
    </settings>

    <!-- Pass 4: specialize - hostname, time zone -->
    <settings pass="specialize">
        <component name="Microsoft-Windows-Shell-Setup"
                   processorArchitecture="arm64"
                   publicKeyToken="31bf3856ad364e35"
                   language="neutral"
                   versionScope="nonSxS">
            <ComputerName>%[4]s</ComputerName>
            <TimeZone>%[5]s</TimeZone>
        </component>
    </settings>

    <!-- Pass 7: oobeSystem - create user, skip OOBE -->
    <settings pass="oobeSystem">
        <component name="Microsoft-Windows-Shell-Setup"
                   processorArchitecture="arm64"
                   publicKeyToken="31bf3856ad364e35"
                   language="neutral"
                   versionScope="nonSxS">
            <OOBE>
                <HideEULAPage>true</HideEULAPage>
                <HideOnlineAccountScreens>true</HideOnlineAccountScreens>
                <HideWirelessSetupInOOBE>true</HideWirelessSetupInOOBE>
                <ProtectYourPC>3</ProtectYourPC>
            </OOBE>

            <UserAccounts>
                <LocalAccounts>
                    <LocalAccount wcm:action="add">
                        <Name>%[2]s</Name>
                        <Password>
                            <Value>%[3]s</Value>
                            <PlainText>true</PlainText>
                        </Password>
                        <Group>Administrators</Group>
                    </LocalAccount>
                </LocalAccounts>
            </UserAccounts>

            <AutoLogon>
                <Username>%[2]s</Username>
                <Password>
                    <Value>%[3]s</Value>
                    <PlainText>true</PlainText>
                </Password>
                <Enabled>true</Enabled>
                <LogonCount>3</LogonCount>
            </AutoLogon>
%[6]s
        </component>
    </settings>
</unattend>
`, config.Locale, config.Username, config.Password, config.Hostname, config.TimeZone, firstLogonCmds)
}

// ensureWindowsEFIBootImage creates a GPT disk image with a FAT32 partition
// containing the full Windows installer contents (minus install.wim).
//
// Apple's EFI firmware reads FAT32 partitions on GPT disks natively, but cannot
// read UDF (the filesystem used by Windows ISOs). The Windows Boot Manager's BCD
// references \sources\boot.wim on the SAME disk as bootaa64.efi, so we must copy
// all installer files — not just the EFI directory — to a FAT32 image.
//
// install.wim (the actual Windows image, ~4GB) exceeds FAT32's 4GB file limit and
// is NOT needed during the PE boot phase. Windows Setup reads it from the original
// ISO attached as a separate USB device.
func ensureWindowsEFIBootImage(windowsISO string) (string, error) {
	bootImgPath := filepath.Join(vmDir, "efi-boot.img")

	// Return cached image if it exists and is newer than the Windows ISO
	if info, err := os.Stat(bootImgPath); err == nil && info.Size() > 0 {
		if isoInfo, err := os.Stat(windowsISO); err == nil {
			if info.ModTime().After(isoInfo.ModTime()) {
				fmt.Printf("Using cached EFI boot image: %s\n", bootImgPath)
				return bootImgPath, nil
			}
		}
	}

	fmt.Println("Creating EFI boot image from Windows ISO...")

	// Mount Windows ISO to extract files
	mountOut, err := exec.Command("hdiutil", "attach", windowsISO, "-nobrowse", "-readonly").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("mount Windows ISO: %w: %s", err, mountOut)
	}
	mountLines := strings.Split(strings.TrimSpace(string(mountOut)), "\n")
	lastLine := mountLines[len(mountLines)-1]
	fields := strings.SplitN(strings.TrimSpace(lastLine), "\t", 3)
	isoDevice := strings.TrimSpace(fields[0])
	isoMount := strings.TrimSpace(fields[len(fields)-1])
	defer exec.Command("hdiutil", "detach", isoDevice).Run()

	fmt.Printf("  Mounted Windows ISO at: %s\n", isoMount)

	// Verify bootaa64.efi exists
	efiDir := filepath.Join(isoMount, "efi")
	if _, err := os.Stat(efiDir); err != nil {
		efiDir = filepath.Join(isoMount, "EFI")
		if _, err := os.Stat(efiDir); err != nil {
			return "", fmt.Errorf("efi directory not found in Windows ISO")
		}
	}
	fmt.Printf("  Found EFI directory: %s\n", efiDir)

	// Create a FAT32 disk image large enough for all files except install.wim.
	// Typical size: ~850MB of installer files + overhead = 1GB image.
	os.Remove(bootImgPath)
	dmgPath := bootImgPath + ".dmg"
	os.Remove(dmgPath)

	if out, err := exec.Command("hdiutil", "create",
		"-size", "1100m",
		"-fs", "MS-DOS FAT32",
		"-volname", "WINBOOT",
		"-layout", "GPTSPUD",
		"-o", dmgPath,
	).CombinedOutput(); err != nil {
		return "", fmt.Errorf("create FAT32 disk image: %w: %s", err, out)
	}

	// Attach and mount the FAT32 partition
	attachOut, err := exec.Command("hdiutil", "attach", dmgPath, "-nobrowse").CombinedOutput()
	if err != nil {
		os.Remove(dmgPath)
		return "", fmt.Errorf("attach FAT32 image: %w: %s", err, attachOut)
	}
	// Parse device and mount point from hdiutil attach output.
	// Lines are tab-separated: "/dev/diskN\tGUID_partition_scheme\t"
	// and "/dev/diskNs1\ttype\t/Volumes/WINBOOT"
	var dmgDevice, dmgMount string
	for _, line := range strings.Split(strings.TrimSpace(string(attachOut)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		tabFields := strings.SplitN(line, "\t", 3)
		dev := strings.TrimSpace(tabFields[0])
		if !strings.HasPrefix(dev, "/dev/disk") {
			continue
		}
		if !strings.Contains(dev[len("/dev/disk"):], "s") {
			dmgDevice = dev
		}
		// Mount point is in the last tab field
		if len(tabFields) >= 3 {
			mp := strings.TrimSpace(tabFields[2])
			if strings.HasPrefix(mp, "/Volumes/") {
				dmgMount = mp
			}
		}
	}
	if dmgDevice == "" {
		return "", fmt.Errorf("could not find device in hdiutil output: %s", attachOut)
	}
	defer exec.Command("hdiutil", "detach", dmgDevice).Run()

	if dmgMount == "" {
		return "", fmt.Errorf("fat32 partition not auto-mounted: %s", attachOut)
	}
	fmt.Printf("  Mounted FAT32 partition at: %s\n", dmgMount)

	// Copy ALL Windows installer files except:
	//  - install.wim: exceeds FAT32 4GB file limit (Setup reads it from the ISO)
	//  - boot.catalog: El Torito BIOS boot catalog, unreadable on UDF and not needed for EFI
	// The BCD references \sources\boot.wim on the same disk as bootaa64.efi,
	// so boot/, efi/, sources/ (with boot.wim), and support/ must all be present.
	fmt.Printf("  Copying installer files (excluding install.wim)...\n")
	if out, err := exec.Command("rsync", "-rlt", "--no-perms", "--chmod=ugo=rwX",
		"--exclude", "install.wim",
		"--exclude", "boot.catalog",
		isoMount+"/", dmgMount+"/",
	).CombinedOutput(); err != nil {
		return "", fmt.Errorf("copy installer files: %w: %s", err, out)
	}

	// Install UEFI Shell as the primary boot loader with auto-chainload.
	// Apple's EFI firmware lacks Graphics Output Protocol (GOP), so the Windows
	// Boot Manager (bootmgfw.efi) produces no display output when loaded directly.
	// The UEFI Shell uses Simple Text Output which DOES work. By booting through
	// the shell, we get console output and can chainload the Windows Boot Manager.
	uefiShellPath := "/tmp/shellaa64.efi"
	if _, err := os.Stat(uefiShellPath); err == nil {
		// Find the actual bootaa64.efi path (case-insensitive filesystem)
		var bootEFIPath string
		for _, check := range []string{
			filepath.Join(dmgMount, "efi/boot/bootaa64.efi"),
			filepath.Join(dmgMount, "EFI/Boot/bootaa64.efi"),
			filepath.Join(dmgMount, "EFI/Boot/BOOTAA64.EFI"),
		} {
			if _, err := os.Stat(check); err == nil {
				bootEFIPath = check
				break
			}
		}
		if bootEFIPath != "" {
			bootDir := filepath.Dir(bootEFIPath)
			// Rename Windows Boot Manager
			bootmgrDst := filepath.Join(bootDir, "bootmgfw.efi")
			if err := os.Rename(bootEFIPath, bootmgrDst); err != nil {
				fmt.Printf("  warning: could not rename bootaa64.efi: %v\n", err)
			} else {
				// Copy UEFI Shell as the primary boot loader
				shellData, err := os.ReadFile(uefiShellPath)
				if err == nil {
					os.WriteFile(bootEFIPath, shellData, 0644)
					fmt.Printf("  Installed UEFI Shell as %s\n", filepath.Base(bootEFIPath))
					fmt.Printf("  Renamed Windows Boot Manager to bootmgfw.efi\n")
				}
			}
		}

		// Create startup.nsh that auto-chainloads the Windows Boot Manager
		startupNSH := `@echo -off
echo "=== UEFI Shell Boot Shim ==="
echo "Chainloading Windows Boot Manager..."
FS0:\efi\boot\bootmgfw.efi
echo "bootmgfw.efi returned (exit code: %lasterror%)"
echo "Trying alternate path..."
FS0:\efi\microsoft\boot\bootmgfw.efi
echo "All boot attempts failed."
stall 10000000
`
		os.WriteFile(filepath.Join(dmgMount, "startup.nsh"), []byte(startupNSH), 0644)
		fmt.Printf("  Created startup.nsh (auto-chainload)\n")
	} else {
		fmt.Printf("  Note: UEFI Shell not found at %s, using Windows Boot Manager directly\n", uefiShellPath)
	}

	// Verify critical files
	for _, check := range []string{
		"efi/boot/bootaa64.efi", "EFI/Boot/bootaa64.efi", "EFI/Boot/BOOTAA64.EFI",
	} {
		if _, err := os.Stat(filepath.Join(dmgMount, check)); err == nil {
			fmt.Printf("  Verified: %s\n", check)
			break
		}
	}
	for _, check := range []string{
		"efi/boot/bootmgfw.efi", "EFI/Boot/bootmgfw.efi",
	} {
		if _, err := os.Stat(filepath.Join(dmgMount, check)); err == nil {
			fmt.Printf("  Verified: %s (Windows Boot Manager)\n", check)
			break
		}
	}
	for _, check := range []string{
		"sources/boot.wim", "Sources/boot.wim",
	} {
		if info, err := os.Stat(filepath.Join(dmgMount, check)); err == nil {
			fmt.Printf("  Verified: %s (%.0f MB)\n", check, float64(info.Size())/(1024*1024))
			break
		}
	}

	// Detach
	if out, err := exec.Command("hdiutil", "detach", dmgDevice).CombinedOutput(); err != nil {
		return "", fmt.Errorf("detach FAT32 image: %w: %s", err, out)
	}

	// Rename DMG (already raw UDRW format) to final path
	os.Rename(dmgPath, bootImgPath)

	if info, err := os.Stat(bootImgPath); err == nil {
		fmt.Printf("  Created EFI boot image: %s (%.0f MB)\n", bootImgPath, float64(info.Size())/(1024*1024))
	}
	return bootImgPath, nil
}

// handleWindowsInstall handles the "install -windows" command.
func handleWindowsInstall() error {
	return installWindowsVM()
}
