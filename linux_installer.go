// Linux VM installation support using cloud-init autoinstall.
//
// This implements unattended Ubuntu Server installation using:
// - cloud-init NoCloud datasource for initial configuration
// - Ubuntu autoinstall for fully automated installation
//
// The installer creates a cloud-init ISO containing user-data and meta-data files
// that Ubuntu reads during installation to configure:
// - User account with password
// - SSH server
// - Hostname
package main

import (
	"crypto/sha512"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tmc/apple/dispatch"
	"github.com/tmc/apple/foundation"
	vz "github.com/tmc/apple/virtualization"
)

// LinuxProvisionConfig holds configuration for Linux VM provisioning.
type LinuxProvisionConfig struct {
	Username     string
	Password     string
	Hostname     string
	SSHPubKey    string
	AutoLogin    bool
	TimeZone     string
	Locale       string
	InstallAgent bool // Install vz-agent during provisioning
}

// DefaultLinuxProvisionConfig returns default provisioning settings.
func DefaultLinuxProvisionConfig() LinuxProvisionConfig {
	return LinuxProvisionConfig{
		Username:     "ubuntu",
		Password:     "ubuntu",
		Hostname:     "ubuntu-vm",
		TimeZone:     "UTC",
		Locale:       "en_US.UTF-8",
		InstallAgent: true,
	}
}

// installLinuxVM performs automated Linux (Ubuntu) installation.
func installLinuxVM() error {
	fmt.Println("=== Linux VM Installer ===")

	// Safety check: refuse to overwrite existing VM disk unless -force is specified.
	if err := checkExistingVM(vmDir, "linux-disk.img"); err != nil {
		return err
	}

	// Validate settings
	if err := validateVMSettings(); err != nil {
		return err
	}

	// Ensure VM directory exists
	if err := os.MkdirAll(vmDir, 0755); err != nil {
		return fmt.Errorf("create VM directory: %w", err)
	}

	// Configure provisioning
	provConfig := DefaultLinuxProvisionConfig()
	if provisionUser != "" {
		provConfig.Username = provisionUser
	}
	if provisionPassword != "" {
		provConfig.Password = provisionPassword
	}
	if noAgent {
		provConfig.InstallAgent = false
	}

	// Get ISO (download if needed)
	resolvedISO, err := ensureLinuxISO()
	if err != nil {
		return fmt.Errorf("ensure ISO: %w", err)
	}
	fmt.Printf("Using ISO: %s\n", resolvedISO)

	// Create cloud-init ISO
	cloudInitISO, err := createCloudInitISO(provConfig)
	if err != nil {
		return fmt.Errorf("create cloud-init ISO: %w", err)
	}
	fmt.Printf("Created cloud-init ISO: %s\n", cloudInitISO)

	// Resolve disk path
	resolvedDiskPath := diskPath
	if resolvedDiskPath == "" {
		resolvedDiskPath = filepath.Join(vmDir, "linux-disk.img")
	}

	// Create disk image if it doesn't exist
	if _, err := os.Stat(resolvedDiskPath); os.IsNotExist(err) {
		fmt.Printf("Creating disk image: %s (%d GB)\n", resolvedDiskPath, diskSizeGB)
		if err := createDiskImage(resolvedDiskPath, diskSizeGB); err != nil {
			return fmt.Errorf("create disk image: %w", err)
		}
	}

	// Prefer direct kernel boot to pass `autoinstall` on the kernel cmdline,
	// which suppresses Ubuntu 24.04's interactive confirmation prompt.
	// If no kernel/initrd was provided, try to extract them from the ISO.
	useDirectBoot := kernelPath != "" && initrdPath != ""
	if !useDirectBoot {
		extracted, err := extractKernelFromISO(resolvedISO, vmDir)
		if err == nil {
			kernelPath = extracted.kernel
			initrdPath = extracted.initrd
			useDirectBoot = true
			fmt.Println("Extracted kernel/initrd from ISO for direct boot (autoinstall)")
		}
	}
	if useDirectBoot && cmdLine == "" {
		cmdLine = "boot=casper autoinstall console=tty0 --- quiet"
	}

	// Build VM configuration with both ISOs
	fmt.Printf("Configuring VM: %d CPUs, %d GB RAM\n", cpuCount, memoryGB)
	fmt.Printf("  Boot mode: %s\n", map[bool]string{true: "direct kernel", false: "EFI"}[useDirectBoot])
	config, err := buildLinuxInstallConfiguration(resolvedDiskPath, resolvedISO, cloudInitISO, kernelPath, initrdPath, cmdLine, useDirectBoot)
	if err != nil {
		return fmt.Errorf("build configuration: %w", err)
	}
	config.Retain()

	// Validate configuration
	fmt.Println("Validating configuration...")
	if _, err := config.ValidateWithError(); err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}
	fmt.Println("  ✓ Configuration valid")

	// Create dispatch queue
	vmQueue := dispatch.QueueCreate("com.appledocs.vz.linux.install")

	// Create VM
	fmt.Println("Creating virtual machine...")
	vm := vz.NewVirtualMachineWithConfigurationQueue(&config, vmQueue)
	if vm.ID == 0 {
		return fmt.Errorf("failed to create virtual machine")
	}
	vm.Retain()

	// Print installation instructions
	fmt.Println()
	fmt.Println("=== Ubuntu Installation Starting ===")
	if useDirectBoot {
		fmt.Println("Using direct kernel boot with autoinstall (no confirmation prompt).")
	} else {
		fmt.Println("Using EFI boot from ISO.")
		fmt.Println("The GRUB bootloader will start automatically after ~5 seconds.")
		fmt.Println("Cloud-init will detect the autoinstall configuration.")
		fmt.Println("NOTE: You may need to type 'yes' at the autoinstall confirmation prompt.")
	}
	fmt.Printf("  Username: %s\n", provConfig.Username)
	fmt.Printf("  Hostname: %s\n", provConfig.Hostname)
	fmt.Println()
	fmt.Println("After installation completes:")
	fmt.Printf("  1. The VM will reboot automatically\n")
	fmt.Printf("  2. Stop this process (Ctrl+C)\n")
	fmt.Printf("  3. Run: ./vz-macos -linux run\n")
	fmt.Println()

	// Start VM
	fmt.Println("Starting installation...")
	return startVMWithQueue(vm, vmQueue)
}

// buildLinuxInstallConfiguration builds a VM config for Linux installation.
// This attaches the installation ISO, disk, and cloud-init ISO.
func buildLinuxInstallConfiguration(diskPath, installISO, cloudInitISO, installKernelPath, installInitrdPath, installCmdLine string, directBoot bool) (vz.VZVirtualMachineConfiguration, error) {
	config := vz.NewVZVirtualMachineConfiguration()

	// CPU and memory
	config.SetCPUCount(cpuCount)
	config.SetMemorySize(memoryGB * 1024 * 1024 * 1024)

	// Platform configuration (generic for Linux)
	platformConfig := vz.NewVZGenericPlatformConfiguration()
	machineID := loadOrCreateGenericMachineIdentifier()
	platformConfig.SetMachineIdentifier(&machineID)

	// Enable nested virtualization (KVM in guest) if supported (macOS 15+, M3+)
	if vz.GetVZGenericPlatformConfigurationClass().NestedVirtualizationSupported() {
		platformConfig.SetNestedVirtualizationEnabled(true)
	}

	config.SetPlatform(&platformConfig.VZPlatformConfiguration)

	// Boot loader: direct kernel boot (with autoinstall cmdline) or EFI
	if directBoot {
		bootloader, err := createLinuxInstallBootLoader(installKernelPath, installInitrdPath, installCmdLine)
		if err != nil {
			return config, err
		}
		config.SetBootLoader(&bootloader.VZBootLoader)
	} else {
		bootloader, err := createEFIBootLoader()
		if err != nil {
			return config, err
		}
		config.SetBootLoader(&bootloader.VZBootLoader)
	}

	// Storage devices:
	// 1. Main disk (Virtio block, becomes /dev/vda)
	// 2. Cloud-init ISO (Virtio block, becomes /dev/vdb or sr0)
	// 3. Installation ISO (USB mass storage — matches Code-Hex/vz pattern)

	// Main disk
	diskURL := foundation.NewURLFileURLWithPath(diskPath)
	diskAttachment, err := vz.NewDiskImageStorageDeviceAttachmentWithURLReadOnlyError(diskURL, false)
	if err != nil {
		return config, fmt.Errorf("create disk attachment: %w", err)
	}
	diskAttachment.Retain()
	diskStorage := vz.NewVirtioBlockDeviceConfigurationWithAttachment(&diskAttachment.VZStorageDeviceAttachment)
	diskStorage.Retain()

	// Cloud-init ISO (Virtio block so cloud-init can detect it as NoCloud datasource)
	cloudInitURL := foundation.NewURLFileURLWithPath(cloudInitISO)
	cloudInitAttachment, err := vz.NewDiskImageStorageDeviceAttachmentWithURLReadOnlyError(cloudInitURL, true)
	if err != nil {
		return config, fmt.Errorf("create cloud-init attachment: %w", err)
	}
	cloudInitAttachment.Retain()
	cloudInitStorage := vz.NewVirtioBlockDeviceConfigurationWithAttachment(&cloudInitAttachment.VZStorageDeviceAttachment)
	cloudInitStorage.Retain()

	// Installation ISO as USB mass storage (EFI firmware can boot from USB)
	isoURL := foundation.NewURLFileURLWithPath(installISO)
	isoAttachment, err := vz.NewDiskImageStorageDeviceAttachmentWithURLReadOnlyError(isoURL, true)
	if err != nil {
		return config, fmt.Errorf("create ISO attachment: %w", err)
	}
	isoAttachment.Retain()
	isoUSB := vz.NewUSBMassStorageDeviceConfigurationWithAttachment(&isoAttachment.VZStorageDeviceAttachment)
	isoUSB.Retain()

	// Set all storage: main disk + cloud-init (Virtio) + install ISO (USB)
	if verbose {
		fmt.Printf("  Storage devices:\n")
		fmt.Printf("    Disk: ID=%x attachment=%x\n", diskStorage.ID, diskAttachment.ID)
		fmt.Printf("    Cloud-init: ID=%x attachment=%x\n", cloudInitStorage.ID, cloudInitAttachment.ID)
		fmt.Printf("    ISO USB: ID=%x attachment=%x\n", isoUSB.ID, isoAttachment.ID)
	}
	config.SetStorageDevices([]vz.VZStorageDeviceConfiguration{
		vz.VZStorageDeviceConfigurationFromID(diskStorage.ID),
		vz.VZStorageDeviceConfigurationFromID(cloudInitStorage.ID),
		vz.VZStorageDeviceConfigurationFromID(isoUSB.ID),
	})

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

	// Audio (optional but nice to have)
	audioConfig := vz.NewVZVirtioSoundDeviceConfiguration()
	setAudioDevices(config, audioConfig)

	// USB controller (required for USB keyboard and USB mass storage devices)
	usbController := vz.NewVZXHCIControllerConfiguration()
	usbController.Retain()
	config.SetUsbControllers([]vz.VZUSBControllerConfiguration{
		vz.VZUSBControllerConfigurationFromID(usbController.ID),
	})

	// Serial console — skip during installation to prevent subiquity from
	// detecting hvc0 and showing a serial mode selection prompt that blocks
	// autoinstall (LP: #2018415). This applies to both direct-boot and EFI
	// boot modes. The serial port can be added for post-install running.

	return config, nil
}

// createLinuxInstallBootLoader creates a VZLinuxBootLoader for installer kernel/initrd.
func createLinuxInstallBootLoader(kernelPath, initrdPath, cmdLine string) (vz.VZLinuxBootLoader, error) {
	absKernelPath, err := filepath.Abs(kernelPath)
	if err != nil {
		return vz.VZLinuxBootLoader{}, fmt.Errorf("resolve kernel path: %w", err)
	}
	if _, statErr := os.Stat(absKernelPath); statErr != nil {
		return vz.VZLinuxBootLoader{}, fmt.Errorf("kernel not found: %s", absKernelPath)
	}

	kernelURL := foundation.NewURLFileURLWithPath(absKernelPath)
	if kernelURL.ID == 0 {
		return vz.VZLinuxBootLoader{}, fmt.Errorf("failed to create kernel URL")
	}

	bootloader := vz.NewLinuxBootLoaderWithKernelURL(kernelURL)
	if bootloader.ID == 0 {
		return vz.VZLinuxBootLoader{}, fmt.Errorf("failed to create Linux boot loader")
	}

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
	}

	if cmdLine == "" {
		cmdLine = "boot=casper autoinstall console=tty0 --- quiet"
	}
	bootloader.SetCommandLine(cmdLine)

	return bootloader, nil
}

type extractedKernel struct {
	kernel string
	initrd string
}

// extractKernelFromISO mounts the Ubuntu ISO and copies vmlinuz + initrd to vmDir.
// Returns paths to the extracted files, or an error if extraction fails.
func extractKernelFromISO(isoPath, vmDir string) (*extractedKernel, error) {
	// Mount the ISO
	out, err := exec.Command("hdiutil", "attach", isoPath, "-nobrowse", "-readonly", "-mountpoint", "/tmp/vz-iso-mount").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("mount ISO: %w: %s", err, out)
	}
	defer exec.Command("hdiutil", "detach", "/tmp/vz-iso-mount", "-quiet").Run()

	// Look for kernel and initrd in standard Ubuntu ISO paths.
	candidates := []struct {
		kernel string
		initrd string
	}{
		{"/tmp/vz-iso-mount/casper/vmlinuz", "/tmp/vz-iso-mount/casper/initrd"},
		{"/tmp/vz-iso-mount/casper/hwe-vmlinuz", "/tmp/vz-iso-mount/casper/hwe-initrd"},
	}

	for _, c := range candidates {
		if _, err := os.Stat(c.kernel); err != nil {
			continue
		}
		if _, err := os.Stat(c.initrd); err != nil {
			continue
		}
		dstKernel := filepath.Join(vmDir, "vmlinuz")
		dstInitrd := filepath.Join(vmDir, "initrd")
		if err := copyFile(c.kernel, dstKernel); err != nil {
			return nil, fmt.Errorf("copy kernel: %w", err)
		}
		if err := copyFile(c.initrd, dstInitrd); err != nil {
			return nil, fmt.Errorf("copy initrd: %w", err)
		}
		return &extractedKernel{kernel: dstKernel, initrd: dstInitrd}, nil
	}
	return nil, fmt.Errorf("no kernel/initrd found in ISO")
}

// createCloudInitISO creates a cloud-init NoCloud ISO with autoinstall configuration.
func createCloudInitISO(config LinuxProvisionConfig) (string, error) {
	// Create temporary directory for cloud-init files
	cloudInitDir := filepath.Join(vmDir, "cloud-init-data")
	if err := os.MkdirAll(cloudInitDir, 0755); err != nil {
		return "", fmt.Errorf("create cloud-init directory: %w", err)
	}

	// Build and include vz-agent if requested.
	agentIncluded := false
	if config.InstallAgent {
		agentBin := filepath.Join(cloudInitDir, "vz-agent")
		if err := buildAgentBinaryTo(agentBin, "linux", "arm64"); err != nil {
			fmt.Printf("warning: could not build vz-agent for inclusion: %v\n", err)
		} else {
			agentIncluded = true
			fmt.Println("  vz-agent binary included in cloud-init ISO")
		}
	}

	// Generate user-data (autoinstall configuration)
	userData := generateUserData(config, agentIncluded)
	userDataPath := filepath.Join(cloudInitDir, "user-data")
	if err := os.WriteFile(userDataPath, []byte(userData), 0644); err != nil {
		return "", fmt.Errorf("write user-data: %w", err)
	}

	// Generate meta-data (instance identification)
	metaData := generateMetaData(config)
	metaDataPath := filepath.Join(cloudInitDir, "meta-data")
	if err := os.WriteFile(metaDataPath, []byte(metaData), 0644); err != nil {
		return "", fmt.Errorf("write meta-data: %w", err)
	}

	// Write a vendor-data file (required by some cloud-init versions).
	vendorDataPath := filepath.Join(cloudInitDir, "vendor-data")
	if err := os.WriteFile(vendorDataPath, []byte("#cloud-config\n{}"), 0644); err != nil {
		return "", fmt.Errorf("write vendor-data: %w", err)
	}

	// Create ISO9660 CIDATA image using hdiutil makehybrid.
	// This doesn't require mounting/unmounting, avoiding diskutil hangs.
	// Cloud-init's NoCloud datasource looks for a filesystem labeled CIDATA.
	isoPath := filepath.Join(vmDir, "cloud-init.iso")
	os.Remove(isoPath)
	cmd := exec.Command("hdiutil", "makehybrid",
		"-o", isoPath,
		"-joliet",
		"-iso",
		"-default-volume-name", "CIDATA",
		"-iso-volume-name", "CIDATA",
		"-joliet-volume-name", "CIDATA",
		cloudInitDir,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("create cloud-init ISO: %w: %s", err, output)
	}

	return isoPath, nil
}

// createFATImage creates a raw FAT12/16 disk image with the given volume label,
// populating it with all files from srcDir.
// buildAgentBinaryTo cross-compiles the vz-agent binary to the given path.
func buildAgentBinaryTo(outputPath, targetOS, targetArch string) error {
	agentPkg := "github.com/tmc/vz-macos/cmd/vz-agent"
	cmd := exec.Command("go", "build", "-o", outputPath, agentPkg)
	cmd.Env = append(os.Environ(),
		"CGO_ENABLED=0",
		"GOOS="+targetOS,
		"GOARCH="+targetArch,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	fmt.Printf("Building vz-agent (%s/%s)...\n", targetOS, targetArch)
	return cmd.Run()
}

// generateUserData creates the cloud-init user-data file with autoinstall config.
func generateUserData(config LinuxProvisionConfig, includeAgent bool) string {
	// Hash password using SHA-512 for compatibility
	hashedPassword := hashPassword(config.Password)

	// Build SSH authorized_keys section
	sshSection := ""
	if config.SSHPubKey != "" {
		sshSection = fmt.Sprintf(`
  ssh:
    install-server: true
    authorized-keys:
      - %s`, config.SSHPubKey)
	} else {
		sshSection = `
  ssh:
    install-server: true
    allow-pw: true`
	}

	agentLateCommands := ""
	if includeAgent {
		agentLateCommands = `
    - cp /cdrom/vz-agent /target/usr/local/bin/vz-agent
    - chmod 755 /target/usr/local/bin/vz-agent
    - |
      cat > /target/etc/systemd/system/vz-agent.service << 'SVCEOF'
      [Unit]
      Description=vz-macos Guest Agent
      After=network.target
      [Service]
      Type=simple
      ExecStart=/usr/local/bin/vz-agent
      Restart=always
      RestartSec=5
      [Install]
      WantedBy=multi-user.target
      SVCEOF
    - curtin in-target --target=/target -- systemctl enable vz-agent`
	}

	userData := fmt.Sprintf(`#cloud-config
autoinstall:
  version: 1
  locale: %s
  keyboard:
    layout: us
  identity:
    hostname: %s
    username: %s
    password: %s%s
  early-commands:
    - printf '#!/bin/sh\nexit 0\n' > /usr/sbin/flash-kernel && chmod +x /usr/sbin/flash-kernel
  storage:
    layout:
      name: direct
  late-commands:
    - curtin in-target --target=/target -- systemctl enable ssh%s
  user-data:
    disable_root: false
    timezone: %s
`, config.Locale, config.Hostname, config.Username, hashedPassword, sshSection, agentLateCommands, config.TimeZone)

	return userData
}

// generateMetaData creates the cloud-init meta-data file.
func generateMetaData(config LinuxProvisionConfig) string {
	return fmt.Sprintf(`instance-id: %s
local-hostname: %s
`, config.Hostname, config.Hostname)
}

// hashPassword creates a SHA-512 password hash suitable for /etc/shadow.
// Uses openssl to generate a proper crypt-compatible hash.
func hashPassword(password string) string {
	// Use openssl to generate a proper SHA-512 crypt hash
	// This uses the -6 flag for SHA-512 and generates a random salt
	cmd := exec.Command("openssl", "passwd", "-6", "-stdin")
	cmd.Stdin = strings.NewReader(password)
	output, err := cmd.Output()
	if err != nil {
		// Fallback to a simple hash if openssl fails
		hash := sha512.Sum512([]byte("vz.macos" + password))
		encoded := base64.StdEncoding.EncodeToString(hash[:])
		encoded = strings.TrimRight(encoded, "=")
		encoded = strings.ReplaceAll(encoded, "+", ".")
		return fmt.Sprintf("$6$vz.macos$%s", encoded)
	}
	return strings.TrimSpace(string(output))
}

// handleLinuxInstall handles the "install -linux" command.
func handleLinuxInstall() error {
	return installLinuxVM()
}
