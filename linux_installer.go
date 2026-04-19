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
	"bytes"
	"crypto/sha512"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

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
	Variant      LinuxVariant
}

// LinuxVariant identifies the Ubuntu install source to provision.
type LinuxVariant string

const (
	LinuxVariantServer  LinuxVariant = "server"
	LinuxVariantDesktop LinuxVariant = "desktop"
)

const linuxAutoinstallPath = "autoinstall.yaml"

const (
	linuxEFIBootArtifactsDir = "EFI/VZMACOS"
	linuxRootUUIDFileName    = "linux-root-uuid.txt"
)

func currentLinuxVariant() LinuxVariant {
	if linuxDesktop {
		return LinuxVariantDesktop
	}
	return LinuxVariantServer
}

func (v LinuxVariant) sourceID() string {
	switch v {
	case LinuxVariantDesktop:
		return ""
	default:
		return "ubuntu-server"
	}
}

func (v LinuxVariant) installISOVariant() LinuxVariant {
	switch v {
	case LinuxVariantDesktop:
		return LinuxVariantServer
	default:
		return v
	}
}

func linuxInstallCommandLine(seedAddress string) string {
	return fmt.Sprintf("boot=casper autoinstall ip=dhcp ds=nocloud-net;s=http://%s/ console=tty0", seedAddress)
}

// DefaultLinuxProvisionConfig returns default provisioning settings.
func DefaultLinuxProvisionConfig() LinuxProvisionConfig {
	return LinuxProvisionConfig{
		Username:     "ubuntu",
		Password:     "ubuntu",
		Hostname:     "ubuntu-vm",
		TimeZone:     "UTC",
		Locale:       "en_US.UTF-8",
		InstallAgent: false,
		Variant:      currentLinuxVariant(),
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

	// Bump defaults for Desktop variant (needs more resources than Server)
	if linuxDesktop {
		if cpuCount < 4 {
			cpuCount = 4
			fmt.Println("  Desktop mode: bumped CPU to 4")
		}
		if memoryGB < 8 {
			memoryGB = 8
			fmt.Println("  Desktop mode: bumped memory to 8 GB")
		}
		if diskSizeGB < 40 {
			diskSizeGB = 40
			fmt.Println("  Desktop mode: bumped disk size to 40 GB")
		}
	}

	// Ensure VM directory exists
	if err := os.MkdirAll(vmDir, 0755); err != nil {
		return fmt.Errorf("create VM directory: %w", err)
	}
	saveHardwareConfig(vmDir)

	// Configure provisioning
	provConfig := DefaultLinuxProvisionConfig()
	if provisionUser != "" {
		provConfig.Username = provisionUser
	}
	if provisionPassword != "" {
		provConfig.Password = provisionPassword
	}
	provConfig.InstallAgent = !noAgent && sandboxAllowsAgentProvision()
	if err := setVMAgentRequested(vmDir, vmAgentPlatformLinux, provConfig.InstallAgent, vmAgentSourceInstall); err != nil {
		fmt.Printf("warning: save guest agent config: %v\n", err)
	}

	// Get ISO (download if needed)
	resolvedISO, err := ensureLinuxISOForVariant(provConfig.Variant.installISOVariant())
	if err != nil {
		return fmt.Errorf("ensure ISO: %w", err)
	}
	fmt.Printf("Using ISO: %s\n", resolvedISO)

	// Create cloud-init ISO
	cloudInitISO, _, err := createCloudInitISO(provConfig)
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

	// Prefer direct kernel boot to pass `autoinstall` on the kernel cmdline.
	// The ISO's GRUB does not include `autoinstall` so subiquity won't
	// detect autoinstall config without it. We extract and decompress the
	// kernel from the ISO (ARM64 vmlinuz is gzip-compressed).
	installKernelPath := kernelPath
	installInitrdPath := initrdPath
	installCmdLine := cmdLine
	useDirectBoot := installKernelPath != "" && installInitrdPath != ""
	if !useDirectBoot {
		extracted, err := extractKernelFromISO(resolvedISO, vmDir)
		if err == nil {
			installKernelPath = extracted.kernel
			installInitrdPath = extracted.initrd
			useDirectBoot = true
			fmt.Println("Extracted kernel/initrd from ISO for direct boot (autoinstall)")
		} else {
			fmt.Printf("warning: could not extract kernel from ISO: %v\n", err)
			fmt.Println("  Falling back to EFI boot (autoinstall may require manual confirmation)")
		}
	}
	if useDirectBoot {
		seedDir := filepath.Join(vmDir, "cloud-init-data")
		seedAddress, seedCloser, err := startCloudInitHTTPServer(seedDir)
		if err != nil {
			return fmt.Errorf("start cloud-init HTTP server: %w", err)
		}
		defer seedCloser.Close()
		if installCmdLine == "" {
			installCmdLine = linuxInstallCommandLine(seedAddress)
		}
		if verbose {
			fmt.Printf("  Serving NoCloud seed via http://%s/\n", seedAddress)
		}
	}

	// Build VM configuration with both ISOs
	fmt.Printf("Configuring VM: %d CPUs, %d GB RAM\n", cpuCount, memoryGB)
	fmt.Printf("  Boot mode: %s\n", map[bool]string{true: "direct kernel", false: "EFI"}[useDirectBoot])
	config, err := buildLinuxInstallConfiguration(resolvedDiskPath, resolvedISO, cloudInitISO, installKernelPath, installInitrdPath, installCmdLine, useDirectBoot)
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

	if useDirectBoot {
		fmt.Println("Installation will power off when it finishes.")
	} else {
		fmt.Println("After installation completes:")
		fmt.Printf("  1. The VM will reboot automatically\n")
		fmt.Printf("  2. Stop this process (Ctrl+C)\n")
		fmt.Printf("  3. Run: ./cove -linux run\n")
	}
	fmt.Println()

	// Start VM
	fmt.Println("Starting installation...")
	if err := startVMWithQueue(vm, vmQueue); err != nil {
		return err
	}
	if err := verifyLinuxInstallBootable(resolvedDiskPath); err != nil {
		return err
	}
	if err := writeLinuxInstalledMarker(vmDir, provConfig.Variant); err != nil {
		return fmt.Errorf("write install marker: %w", err)
	}
	return nil
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

	// Keep nested virtualization disabled during installation. The Ubuntu
	// desktop bootstrap has hit undefined-instruction crashes in overlayfs
	// with the richer virtual CPU feature exposure.

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

	// Cloud-init data as USB mass storage. The casper live environment's
	// initrd may not include virtio block drivers, so CIDATA on a Virtio
	// block device won't be visible to cloud-init. USB mass storage is
	// universally supported in the initramfs.
	cloudInitURL := foundation.NewURLFileURLWithPath(cloudInitISO)
	cloudInitAttachment, err := vz.NewDiskImageStorageDeviceAttachmentWithURLReadOnlyError(cloudInitURL, true)
	if err != nil {
		return config, fmt.Errorf("create cloud-init attachment: %w", err)
	}
	cloudInitAttachment.Retain()
	cloudInitUSB := vz.NewUSBMassStorageDeviceConfigurationWithAttachment(&cloudInitAttachment.VZStorageDeviceAttachment)
	cloudInitUSB.Retain()

	// Installation ISO as USB mass storage (EFI firmware can boot from USB)
	isoURL := foundation.NewURLFileURLWithPath(installISO)
	isoAttachment, err := vz.NewDiskImageStorageDeviceAttachmentWithURLReadOnlyError(isoURL, true)
	if err != nil {
		return config, fmt.Errorf("create ISO attachment: %w", err)
	}
	isoAttachment.Retain()
	isoUSB := vz.NewUSBMassStorageDeviceConfigurationWithAttachment(&isoAttachment.VZStorageDeviceAttachment)
	isoUSB.Retain()

	// Set all storage: main disk (Virtio) + cloud-init (USB) + install ISO (USB)
	if verbose {
		fmt.Printf("  Storage devices:\n")
		fmt.Printf("    Disk: ID=%x attachment=%x\n", diskStorage.ID, diskAttachment.ID)
		fmt.Printf("    Cloud-init: ID=%x attachment=%x\n", cloudInitUSB.ID, cloudInitAttachment.ID)
		fmt.Printf("    ISO USB: ID=%x attachment=%x\n", isoUSB.ID, isoAttachment.ID)
	}
	config.SetStorageDevices([]vz.VZStorageDeviceConfiguration{
		vz.VZStorageDeviceConfigurationFromID(diskStorage.ID),
		vz.VZStorageDeviceConfigurationFromID(cloudInitUSB.ID),
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

	// Attach a serial console only when the kernel cmdline asks for hvc0.
	// The live installer pauses on a serial-mode chooser when hvc0 is a console,
	// so direct-boot autoinstall keeps the serial device detached.
	if strings.Contains(installCmdLine, "console=hvc0") {
		serialLogPath := filepath.Join(vmDir, "install-serial.log")
		serialFile, serialErr := os.OpenFile(serialLogPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if serialErr == nil {
			readHandle := foundation.NewFileHandleWithFileDescriptor(int(os.Stdin.Fd()))
			writeHandle := foundation.NewFileHandleWithFileDescriptor(int(serialFile.Fd()))
			serialAttachment := vz.NewFileHandleSerialPortAttachmentWithFileHandleForReadingFileHandleForWriting(readHandle, writeHandle)
			if serialAttachment.ID != 0 {
				serialPort := vz.NewVZVirtioConsoleDeviceSerialPortConfiguration()
				serialPort.SetAttachment(&serialAttachment.VZSerialPortAttachment)
				if serialPort.ID != 0 {
					setSerialPorts(config, serialPort)
					if verbose {
						fmt.Printf("  Serial console logging to %s\n", serialLogPath)
					}
				}
			}
		}
	}

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
		cmdLine = "boot=casper autoinstall ds=nocloud console=hvc0 console=tty0"
	}
	bootloader.SetCommandLine(cmdLine)

	return bootloader, nil
}

type extractedKernel struct {
	kernel string
	initrd string
}

// extractKernelFromISO extracts vmlinuz + initrd from an Ubuntu ISO into vmDir.
// Uses bsdtar to read the ISO9660 filesystem (hdiutil cannot mount hybrid ISOs).
// ARM64 vmlinuz is gzip-compressed; VZLinuxBootLoader requires the uncompressed
// Image (starts with MZ), so we decompress if needed.
func extractKernelFromISO(isoPath, vmDir string) (*extractedKernel, error) {
	candidates := []struct {
		kernel string
		initrd string
	}{
		{"casper/vmlinuz", "casper/initrd"},
		{"casper/hwe-vmlinuz", "casper/hwe-initrd"},
	}
	for _, c := range candidates {
		dstKernel := filepath.Join(vmDir, "vmlinuz")
		dstInitrd := filepath.Join(vmDir, "initrd")
		out, err := exec.Command("bsdtar", "-xf", isoPath, "-C", vmDir, "--include", c.kernel, "--include", c.initrd, "--strip-components=1").CombinedOutput()
		if err != nil {
			continue
		}
		if _, err := os.Stat(dstKernel); err != nil {
			continue
		}
		if _, err := os.Stat(dstInitrd); err != nil {
			continue
		}
		_ = out
		// Ensure extracted files are writable (ISO preserves read-only
		// permissions). The kernel may need in-place decompression and
		// the initrd may have cloud-init data appended.
		os.Chmod(dstKernel, 0644)
		os.Chmod(dstInitrd, 0644)
		if err := decompressKernelIfNeeded(dstKernel); err != nil {
			return nil, fmt.Errorf("decompress kernel: %w", err)
		}
		return &extractedKernel{kernel: dstKernel, initrd: dstInitrd}, nil
	}
	return nil, fmt.Errorf("no kernel/initrd found in ISO (tried bsdtar)")
}

// decompressKernelIfNeeded checks if the kernel file is gzip-compressed and
// decompresses it in-place. ARM64 Ubuntu vmlinuz files are gzip-wrapped;
// VZLinuxBootLoader requires the raw Image format (MZ header).
func decompressKernelIfNeeded(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	var magic [2]byte
	_, err = f.Read(magic[:])
	f.Close()
	if err != nil {
		return err
	}
	// 0x1f 0x8b = gzip magic number
	if magic[0] != 0x1f || magic[1] != 0x8b {
		return nil // already uncompressed
	}
	tmp := path + ".raw"
	out, err := exec.Command("gunzip", "-c", path).Output()
	if err != nil {
		return fmt.Errorf("gunzip: %w", err)
	}
	if err := os.WriteFile(tmp, out, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func injectAutoinstallIntoInitrd(initrdPath, autoinstallConfigPath string) (string, error) {
	if initrdPath == "" {
		return "", fmt.Errorf("empty initrd path")
	}
	if autoinstallConfigPath == "" {
		return "", fmt.Errorf("empty autoinstall config path")
	}

	workDir := filepath.Join(filepath.Dir(initrdPath), "initrd.autoinstall.d")
	if err := os.RemoveAll(workDir); err != nil {
		return "", fmt.Errorf("reset initrd work directory: %w", err)
	}
	if err := os.MkdirAll(workDir, 0755); err != nil {
		return "", fmt.Errorf("create initrd work directory: %w", err)
	}
	defer os.RemoveAll(workDir)

	autoinstallData, err := os.ReadFile(autoinstallConfigPath)
	if err != nil {
		return "", fmt.Errorf("read autoinstall config: %w", err)
	}
	if err := os.WriteFile(filepath.Join(workDir, linuxAutoinstallPath), autoinstallData, 0644); err != nil {
		return "", fmt.Errorf("write autoinstall config into initrd: %w", err)
	}

	outputPath := filepath.Join(filepath.Dir(initrdPath), "initrd.autoinstall")
	tempPath := outputPath + ".tmp"
	if err := os.Remove(tempPath); err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("remove temporary initrd: %w", err)
	}
	if err := copyFile(initrdPath, tempPath); err != nil {
		return "", fmt.Errorf("copy initrd for autoinstall injection: %w", err)
	}
	archive, err := buildAutoinstallArchive(workDir)
	if err != nil {
		return "", err
	}
	outputFile, err := os.OpenFile(tempPath, os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return "", fmt.Errorf("open autoinstall initrd for append: %w", err)
	}
	if _, err := outputFile.Write(archive); err != nil {
		outputFile.Close()
		return "", fmt.Errorf("append autoinstall archive: %w", err)
	}
	if err := outputFile.Close(); err != nil {
		return "", fmt.Errorf("close autoinstall initrd: %w", err)
	}
	if err := os.Rename(tempPath, outputPath); err != nil {
		return "", fmt.Errorf("rename autoinstall initrd: %w", err)
	}
	return outputPath, nil
}

func buildAutoinstallArchive(rootDir string) ([]byte, error) {
	var archive bytes.Buffer
	cpioCmd := exec.Command("cpio", "-o", "-H", "newc", "--quiet")
	cpioCmd.Dir = rootDir
	cpioCmd.Stdin = strings.NewReader(linuxAutoinstallPath + "\n")
	cpioCmd.Stdout = &archive
	var cpioStderr bytes.Buffer
	cpioCmd.Stderr = &cpioStderr
	if err := cpioCmd.Run(); err != nil {
		return nil, fmt.Errorf("build autoinstall archive: %w: %s", err, strings.TrimSpace(cpioStderr.String()))
	}
	return archive.Bytes(), nil
}

type linuxCloudInitData struct {
	userData        string
	metaData        string
	vendorData      string
	autoinstallData string
}

func buildLinuxCloudInitData(config LinuxProvisionConfig, includeAgent bool, agentURL string) linuxCloudInitData {
	autoinstallData := generateAutoinstallData(config, includeAgent, agentURL)
	return linuxCloudInitData{
		userData:        "#cloud-config\n" + autoinstallData,
		metaData:        generateMetaData(config),
		vendorData:      "#cloud-config\n{}\n",
		autoinstallData: autoinstallData,
	}
}

func startCloudInitHTTPServer(seedDir string) (string, io.Closer, error) {
	if seedDir == "" {
		return "", nil, fmt.Errorf("empty seed directory")
	}
	if _, err := os.Stat(filepath.Join(seedDir, "user-data")); err != nil {
		return "", nil, fmt.Errorf("seed user-data: %w", err)
	}
	if _, err := os.Stat(filepath.Join(seedDir, "meta-data")); err != nil {
		return "", nil, fmt.Errorf("seed meta-data: %w", err)
	}

	return startCloudInitGoHTTPServer(seedDir)
}

func startCloudInitGoHTTPServer(seedDir string) (string, io.Closer, error) {
	hostIP := "192.168.64.1"
	listener, err := net.Listen("tcp4", net.JoinHostPort(hostIP, "0"))
	if err != nil {
		fmt.Printf("warning: cloud-init HTTP bind to %s failed, falling back to all interfaces: %v\n", hostIP, err)
		listener, err = net.Listen("tcp4", "0.0.0.0:0")
		if err != nil {
			return "", nil, fmt.Errorf("listen: %w", err)
		}
	}

	port := listener.Addr().(*net.TCPAddr).Port
	addr := fmt.Sprintf("%s:%d", hostIP, port)

	fileServer := http.FileServer(http.Dir(seedDir))
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if verbose {
			fmt.Printf("  cloud-init HTTP %s %s from %s\n", r.Method, r.URL.Path, r.RemoteAddr)
		}
		fileServer.ServeHTTP(w, r)
	})

	srv := &http.Server{Handler: handler}
	go srv.Serve(listener)

	return addr, srv, nil
}

// createCloudInitISO creates a cloud-init NoCloud ISO with autoinstall configuration.
func createCloudInitISO(config LinuxProvisionConfig) (string, string, error) {
	// Create temporary directory for cloud-init files
	cloudInitDir := filepath.Join(vmDir, "cloud-init-data")
	if err := os.MkdirAll(cloudInitDir, 0755); err != nil {
		return "", "", fmt.Errorf("create cloud-init directory: %w", err)
	}

	// Build and include vz-agent if requested.
	agentIncluded := false
	agentPath := ""
	if config.InstallAgent {
		agentPath = filepath.Join(cloudInitDir, "vz-agent")
		if err := buildAgentBinaryTo(agentPath, "linux", "arm64"); err != nil {
			fmt.Printf("warning: could not build vz-agent for inclusion: %v\n", err)
			agentPath = ""
		} else {
			agentIncluded = true
			fmt.Println("  vz-agent binary included in cloud-init ISO")
		}
	}

	userDataPath := filepath.Join(cloudInitDir, "user-data")
	seed := buildLinuxCloudInitData(config, agentIncluded, "")
	if err := os.WriteFile(userDataPath, []byte(seed.userData), 0644); err != nil {
		return "", "", fmt.Errorf("write user-data: %w", err)
	}
	autoinstallPath := filepath.Join(cloudInitDir, linuxAutoinstallPath)
	if err := os.WriteFile(autoinstallPath, []byte(seed.autoinstallData), 0644); err != nil {
		return "", "", fmt.Errorf("write %s: %w", linuxAutoinstallPath, err)
	}

	// Generate meta-data (instance identification)
	metaDataPath := filepath.Join(cloudInitDir, "meta-data")
	if err := os.WriteFile(metaDataPath, []byte(seed.metaData), 0644); err != nil {
		return "", "", fmt.Errorf("write meta-data: %w", err)
	}

	// Write a vendor-data file (required by some cloud-init versions).
	vendorDataPath := filepath.Join(cloudInitDir, "vendor-data")
	if err := os.WriteFile(vendorDataPath, []byte(seed.vendorData), 0644); err != nil {
		return "", "", fmt.Errorf("write vendor-data: %w", err)
	}

	// Create ISO9660 CIDATA image. Cloud-init's NoCloud datasource scans
	// block devices for a filesystem labeled "CIDATA" (iso9660 or vfat).
	//
	// Prefer mkisofs (from cdrtools) over hdiutil makehybrid because
	// hdiutil adds Apple HFS+ hybrid extensions that some Linux kernels
	// in the casper live environment fail to read.
	isoPath := filepath.Join(vmDir, "cloud-init.iso")
	os.Remove(isoPath)

	var cmd *exec.Cmd
	if mkisofs, err := exec.LookPath("mkisofs"); err == nil {
		cmd = exec.Command(mkisofs,
			"-output", isoPath,
			"-volid", "CIDATA",
			"-joliet",
			"-rock",
			cloudInitDir,
		)
	} else {
		// Fallback to hdiutil if mkisofs is not available.
		cmd = exec.Command("hdiutil", "makehybrid",
			"-o", isoPath,
			"-joliet",
			"-iso",
			"-default-volume-name", "CIDATA",
			"-iso-volume-name", "CIDATA",
			"-joliet-volume-name", "CIDATA",
			cloudInitDir,
		)
	}
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", "", fmt.Errorf("create cloud-init ISO: %w: %s", err, output)
	}

	return isoPath, agentPath, nil
}

// createFATImage creates a raw FAT12/16 disk image with the given volume label,
// populating it with all files from srcDir.
// buildAgentBinaryTo cross-compiles the vz-agent binary to the given path.
func buildAgentBinaryTo(outputPath, targetOS, targetArch string) error {
	agentPkg := "github.com/tmc/vz-macos/cmd/vz-agent"
	moduleDir, err := findCoveModuleDir()
	if err != nil {
		return fmt.Errorf("locate vz-macos module: %w (run cove from a checkout, or set COVE_SRC=<path-to-vz-macos>)", err)
	}
	cmd := exec.Command("go", "build", "-o", outputPath, agentPkg)
	cmd.Dir = moduleDir
	cmd.Env = append(os.Environ(),
		"CGO_ENABLED=0",
		"GOOS="+targetOS,
		"GOARCH="+targetArch,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	fmt.Printf("Building vz-agent (%s/%s) from %s...\n", targetOS, targetArch, moduleDir)
	return cmd.Run()
}

func linuxAutoLoginLateCommand(config LinuxProvisionConfig) string {
	if !config.AutoLogin || config.Variant != LinuxVariantDesktop {
		return ""
	}
	return fmt.Sprintf(`
    - |
      mkdir -p /target/etc/gdm3
      printf '%%s\n' '[daemon]' 'AutomaticLoginEnable=true' %q > /target/etc/gdm3/custom.conf`, "AutomaticLogin="+config.Username)
}

func linuxDesktopPackagesSection(config LinuxProvisionConfig) string {
	if config.Variant != LinuxVariantDesktop {
		return ""
	}
	return `
  packages:
    - ubuntu-desktop`
}

func linuxSourceSection(config LinuxProvisionConfig) string {
	sourceID := config.Variant.sourceID()
	if sourceID == "" {
		return ""
	}
	return fmt.Sprintf(`
  source:
    search_drivers: false
    id: %s`, sourceID)
}

func linuxEarlyCommandsSection(config LinuxProvisionConfig) string {
	return fmt.Sprintf(`
  early-commands:
    - printf '#!/bin/sh\nexit 0\n' > /usr/sbin/flash-kernel && chmod +x /usr/sbin/flash-kernel
    - |
      # Disable flash-kernel in the target. curthooks installs the kernel
      # inside /target via chroot, which triggers flash-kernel's postinst.
      # flash-kernel fails on VMs without DTB entries. We background a
      # watcher that replaces /target/usr/sbin/flash-kernel with a no-op
      # stub after curtin extracts the root filesystem. The kernel/initrd
      # are still installed correctly; only flash-kernel's DTB flashing
      # (which is irrelevant for EFI VMs) is skipped.
      (while true; do
        if [ -x /target/usr/sbin/flash-kernel ] && \
           ! head -1 /target/usr/sbin/flash-kernel 2>/dev/null | grep -q 'exit 0'; then
          printf '#!/bin/sh\nexit 0\n' > /target/usr/sbin/flash-kernel
        fi
        sleep 1
      done) &`)
}

func linuxStorageSection() string {
	return `
  storage:
    swap:
      size: 0
    config:
      - type: disk
        id: disk0
        match:
          size: largest
        ptable: gpt
        wipe: superblock-recursive
        grub_device: true
      - type: partition
        id: disk0-esp
        device: disk0
        number: 1
        size: 1G
        flag: boot
      - type: format
        id: disk0-esp-fs
        volume: disk0-esp
        fstype: fat32
        label: EFI
      - type: partition
        id: disk0-root
        device: disk0
        number: 2
        size: -1
      - type: format
        id: disk0-root-fs
        volume: disk0-root
        fstype: ext4
        label: cloudimg-rootfs
      - type: mount
        id: disk0-root-mount
        path: /
        device: disk0-root-fs
      - type: mount
        id: disk0-esp-mount
        path: /boot/efi
        device: disk0-esp-fs`
}

func linuxBootloaderLateCommands() string {
	return fmt.Sprintf(`
    - >-
      curtin in-target -- apt-get install -y
      grub-efi-arm64
      grub-efi-arm64-bin
    - >-
      curtin in-target -- grub-install
      --target=arm64-efi
      --efi-directory=/boot/efi
      --bootloader-id=ubuntu
      --no-nvram
      --removable
    - |
      ROOT_UUID=$(findmnt -no UUID /target)
      mkdir -p /target/boot/efi/EFI/BOOT
      printf '%%s\n' \
        'insmod part_gpt' \
        'insmod ext2' \
        "search --no-floppy --fs-uuid --set=root ${ROOT_UUID}" \
        'set prefix=($root)/boot/grub' \
        'configfile $prefix/grub.cfg' \
        > /target/boot/efi/EFI/BOOT/grub.cfg
    - >-
      curtin in-target -- /bin/sh -c
      'if grep -q "^GRUB_CMDLINE_LINUX=" /etc/default/grub; then
         sed -i -e "s/^GRUB_CMDLINE_LINUX=.*/GRUB_CMDLINE_LINUX=\"console=tty0 console=hvc0\"/" /etc/default/grub;
       else
         printf "\nGRUB_CMDLINE_LINUX=\"console=tty0 console=hvc0\"\n" >> /etc/default/grub;
       fi'
    - curtin in-target -- update-grub
    - |
      set -eu
      latest_kernel=$(ls /target/boot/vmlinuz-* | sort -V | tail -n 1)
      latest_initrd=$(ls /target/boot/initrd.img-* | sort -V | tail -n 1)
      mkdir -p /target/boot/efi/%s
      cp "$latest_kernel" /target/boot/efi/%s/vmlinuz
      cp "$latest_initrd" /target/boot/efi/%s/initrd
      findmnt -no UUID /target > /target/boot/efi/%s/%s
`, linuxEFIBootArtifactsDir, linuxEFIBootArtifactsDir, linuxEFIBootArtifactsDir, linuxEFIBootArtifactsDir, linuxRootUUIDFileName)
}

func linuxDesktopLateCommands(config LinuxProvisionConfig) string {
	if config.Variant != LinuxVariantDesktop {
		return ""
	}
	return `
    - >-
      curtin in-target --
      sed -i -e
      's/GRUB_CMDLINE_LINUX_DEFAULT=".*/GRUB_CMDLINE_LINUX_DEFAULT="quiet splash"/'
      /etc/default/grub
    - curtin in-target -- update-grub
    - rm -f /target/etc/netplan/00-installer-config*yaml
    - >-
      printf "network:\n  version: 2\n  renderer: NetworkManager\n"
      > /target/etc/netplan/01-network-manager-all.yaml
    - curtin in-target -- apt-get install -y cloud-init`
}

func generateAutoinstallData(config LinuxProvisionConfig, includeAgent bool, agentURL string) string {
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

	sourceSection := linuxSourceSection(config)
	packagesSection := linuxDesktopPackagesSection(config)

	agentLateCommands := ""
	if includeAgent {
		agentInstallCommand := `
    - |
      found=""
      for d in /media/*/vz-agent /mnt/*/vz-agent /cdrom/vz-agent /var/lib/cloud/seed/nocloud*/vz-agent; do
        if [ -f "$d" ]; then
          cp "$d" /target/usr/local/bin/vz-agent
          found=1
          break
        fi
      done
      if [ -z "$found" ]; then
        seed_dev=$(blkid -L CIDATA || true)
        if [ -n "$seed_dev" ]; then
          mkdir -p /tmp/vz-cidata
          if mount -o ro "$seed_dev" /tmp/vz-cidata 2>/dev/null; then
            cp /tmp/vz-cidata/vz-agent /target/usr/local/bin/vz-agent
            umount /tmp/vz-cidata || true
            found=1
          fi
        fi
      fi
      test -s /target/usr/local/bin/vz-agent`
		if agentURL != "" {
			agentInstallCommand = fmt.Sprintf(`
    - |
      if command -v wget >/dev/null 2>&1; then
        wget -O /target/usr/local/bin/vz-agent %q
      elif command -v curl >/dev/null 2>&1; then
        curl -fsSL -o /target/usr/local/bin/vz-agent %q
      else
        found=""
        for d in /media/*/vz-agent /mnt/*/vz-agent /cdrom/vz-agent /var/lib/cloud/seed/nocloud*/vz-agent; do
          if [ -f "$d" ]; then
            cp "$d" /target/usr/local/bin/vz-agent
            found=1
            break
          fi
        done
        if [ -z "$found" ]; then
          seed_dev=$(blkid -L CIDATA || true)
          if [ -n "$seed_dev" ]; then
            mkdir -p /tmp/vz-cidata
            if mount -o ro "$seed_dev" /tmp/vz-cidata 2>/dev/null; then
              cp /tmp/vz-cidata/vz-agent /target/usr/local/bin/vz-agent
              umount /tmp/vz-cidata || true
            fi
          fi
        fi
      fi
      test -s /target/usr/local/bin/vz-agent`, agentURL, agentURL)
		}
		agentLateCommands = `
` + agentInstallCommand + `
    - chmod 755 /target/usr/local/bin/vz-agent
    - |
      mkdir -p /target/etc/systemd/system
      printf '%s\n' \
        '[Unit]' \
        'Description=cove Guest Agent' \
        'After=network-online.target systemd-modules-load.service' \
        'Wants=network-online.target' \
        '[Service]' \
        'Type=simple' \
        'ExecStartPre=/bin/sh -c "modprobe vsock >/dev/null 2>&1 || true; modprobe virtio_vsock >/dev/null 2>&1 || true; modprobe vmw_vsock_virtio_transport >/dev/null 2>&1 || true; for i in 1 2 3 4 5; do [ -e /dev/vsock ] && exit 0; sleep 1; done; exit 0"' \
        'ExecStart=/bin/sh -c "exec /usr/local/bin/vz-agent -mode daemon 2>&1 | tee -a /var/log/vz-agent.log"' \
        'Restart=always' \
        'RestartSec=2' \
        'StandardOutput=journal+console' \
        'StandardError=journal+console' \
        '[Install]' \
        'WantedBy=multi-user.target' \
        > /target/etc/systemd/system/vz-agent.service
    - curtin in-target --target=/target -- systemctl daemon-reload
    - curtin in-target --target=/target -- systemctl enable vz-agent`
	}

	autoLoginLateCommands := linuxAutoLoginLateCommand(config)
	earlyCommandsSection := linuxEarlyCommandsSection(config)
	storageSection := linuxStorageSection()
	bootloaderLateCommands := linuxBootloaderLateCommands()
	desktopLateCommands := linuxDesktopLateCommands(config)

	return fmt.Sprintf(`autoinstall:
  version: 1
  locale: %s
  keyboard:
    layout: us
%s%s
  identity:
    hostname: %s
    username: %s
    password: %s%s
  shutdown: poweroff
%s
%s
  late-commands:
    - curtin in-target --target=/target -- systemctl enable ssh%s%s%s%s
  error-commands:
    - cat /var/log/installer/curtin-install.log | tail -200 > /dev/hvc0 2>&1 || true
    - cat /var/crash/*.crash > /dev/hvc0 2>&1 || true
  user-data:
    disable_root: false
    timezone: %s
`, config.Locale, packagesSection, sourceSection, config.Hostname, config.Username, hashedPassword, sshSection, earlyCommandsSection, storageSection, bootloaderLateCommands, desktopLateCommands, agentLateCommands, autoLoginLateCommands, config.TimeZone)
}

// generateUserData creates the cloud-init user-data file with autoinstall config.
func generateUserData(config LinuxProvisionConfig, includeAgent bool, agentURL string) string {
	return "#cloud-config\n" + generateAutoinstallData(config, includeAgent, agentURL)
}

func linuxInstalledMarkerPath(vmDir string) string {
	return filepath.Join(vmDir, "linux-installed")
}

func writeLinuxInstalledMarker(vmDir string, variant LinuxVariant) error {
	return os.WriteFile(linuxInstalledMarkerPath(vmDir), []byte(string(variant)+"\n"), 0644)
}

func verifyLinuxInstallBootable(diskPath string) error {
	devices, err := attachLinuxDiskReadOnly(diskPath)
	if err != nil {
		return fmt.Errorf("attach installed disk: %w", err)
	}
	if len(devices) < 2 {
		_ = detachLinuxDisk(diskPath, devices)
		return fmt.Errorf("attach installed disk: expected partition devices, got %v", devices)
	}
	defer func() {
		_ = detachLinuxDisk(diskPath, devices)
	}()

	mountPoint, err := mountLinuxEFIPartitionReadOnly(devices[1])
	if err != nil {
		return fmt.Errorf("mount EFI system partition: %w", err)
	}
	defer unmountLinuxEFIPartition(mountPoint)

	bootloaderPath := filepath.Join(mountPoint, "EFI", "BOOT", "BOOTAA64.EFI")
	if _, err := os.Stat(bootloaderPath); err != nil {
		return fmt.Errorf("missing EFI bootloader %s: %w", bootloaderPath, err)
	}
	if err := stageInstalledLinuxBootArtifacts(vmDir, mountPoint); err != nil {
		return err
	}
	return nil
}

func stageInstalledLinuxBootArtifacts(vmDir, mountPoint string) error {
	artifactDir := filepath.Join(mountPoint, filepath.FromSlash(linuxEFIBootArtifactsDir))
	kernelSource := filepath.Join(artifactDir, "vmlinuz")
	initrdSource := filepath.Join(artifactDir, "initrd")
	rootUUIDSource := filepath.Join(artifactDir, linuxRootUUIDFileName)

	for _, path := range []string{kernelSource, initrdSource, rootUUIDSource} {
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("missing staged linux boot artifact %s: %w", path, err)
		}
	}

	if err := copyFile(kernelSource, filepath.Join(vmDir, "vmlinuz")); err != nil {
		return fmt.Errorf("copy staged linux kernel: %w", err)
	}
	if err := decompressKernelIfNeeded(filepath.Join(vmDir, "vmlinuz")); err != nil {
		return fmt.Errorf("prepare staged linux kernel: %w", err)
	}
	if err := copyFile(initrdSource, filepath.Join(vmDir, "initrd")); err != nil {
		return fmt.Errorf("copy staged linux initrd: %w", err)
	}

	rootUUID, err := os.ReadFile(rootUUIDSource)
	if err != nil {
		return fmt.Errorf("read staged linux root uuid: %w", err)
	}
	rootUUID = bytes.TrimSpace(rootUUID)
	if len(rootUUID) == 0 {
		return fmt.Errorf("staged linux root uuid is empty")
	}
	if err := os.WriteFile(filepath.Join(vmDir, linuxRootUUIDFileName), append(rootUUID, '\n'), 0644); err != nil {
		return fmt.Errorf("write staged linux root uuid: %w", err)
	}
	return nil
}

func attachLinuxDiskReadOnly(diskPath string) ([]string, error) {
	out, err := exec.Command("hdiutil", "attach", "-readonly", "-nomount", diskPath).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("hdiutil attach: %w: %s", err, strings.TrimSpace(string(out)))
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var devices []string
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if strings.HasPrefix(fields[0], "/dev/disk") {
			devices = append(devices, fields[0])
		}
	}
	if len(devices) == 0 {
		return nil, fmt.Errorf("hdiutil attach returned no devices")
	}
	return devices, nil
}

func detachLinuxDisk(diskPath string, devices []string) error {
	if len(devices) == 0 {
		return nil
	}
	if out, err := exec.Command("hdiutil", "detach", devices[0]).CombinedOutput(); err != nil {
		if forceOut, forceErr := exec.Command("hdiutil", "detach", "-force", devices[0]).CombinedOutput(); forceErr != nil {
			return fmt.Errorf("hdiutil detach %s: %w: %s; force detach: %w: %s", devices[0], err, strings.TrimSpace(string(out)), forceErr, strings.TrimSpace(string(forceOut)))
		}
	}
	if diskPath == "" {
		return nil
	}
	return waitForLinuxDiskDetach(diskPath, 45*time.Second)
}

func waitForLinuxDiskDetach(diskPath string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	want := "image-path      : " + diskPath
	for time.Now().Before(deadline) {
		out, err := exec.Command("hdiutil", "info").CombinedOutput()
		if err != nil {
			return fmt.Errorf("hdiutil info: %w: %s", err, strings.TrimSpace(string(out)))
		}
		if !strings.Contains(string(out), want) {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("disk image still attached after %s: %s", timeout, diskPath)
}

func mountLinuxEFIPartitionReadOnly(device string) (string, error) {
	out, err := exec.Command("diskutil", "mount", "readOnly", device).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("diskutil mount: %w: %s", err, strings.TrimSpace(string(out)))
	}
	infoOut, err := exec.Command("diskutil", "info", device).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("diskutil info: %w: %s", err, strings.TrimSpace(string(infoOut)))
	}
	for _, line := range strings.Split(string(infoOut), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Mount Point:") {
			mountPoint := strings.TrimSpace(strings.TrimPrefix(line, "Mount Point:"))
			if mountPoint != "" && mountPoint != "Not mounted" {
				return mountPoint, nil
			}
		}
	}
	return "", fmt.Errorf("could not determine mount point for %s", device)
}

func unmountLinuxEFIPartition(mountPoint string) {
	if mountPoint == "" {
		return
	}
	_ = exec.Command("diskutil", "unmount", mountPoint).Run()
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

// monitorInstallCompletion watches the serial log for the first successful
// shutdown/reboot event. With direct kernel boot the VM would reboot into
// the same installer kernel, causing an install loop. When the shutdown
// line is detected we send SIGINT to ourselves so the VM exits cleanly.
func monitorInstallCompletion(logPath string) {
	// Poll the log file for the shutdown event.
	seen := int64(0)
	for {
		time.Sleep(5 * time.Second)
		f, err := os.Open(logPath)
		if err != nil {
			continue
		}
		if seen > 0 {
			f.Seek(seen, 0)
		}
		buf := make([]byte, 8192)
		n, _ := f.Read(buf)
		f.Close()
		if n == 0 {
			continue
		}
		seen += int64(n)
		chunk := string(buf[:n])
		if strings.Contains(chunk, "subiquity/Shutdown/shutdown: mode=REBOOT") ||
			strings.Contains(chunk, "reboot: Restarting system") {
			fmt.Println()
			fmt.Println("=== Installation Complete ===")
			fmt.Println("The installer has finished and requested a reboot.")
			fmt.Println("Run the installed VM with: ./cove -linux run")
			// Give serial log a moment to flush.
			time.Sleep(2 * time.Second)
			p, _ := os.FindProcess(os.Getpid())
			p.Signal(os.Interrupt)
			return
		}
		// Also detect install failure.
		if strings.Contains(chunk, "install_fail") {
			fmt.Println()
			fmt.Println("=== Installation Failed ===")
			fmt.Printf("Check the serial log for details: %s\n", logPath)
			time.Sleep(2 * time.Second)
			p, _ := os.FindProcess(os.Getpid())
			p.Signal(os.Interrupt)
			return
		}
	}
}

// handleLinuxInstall handles the "install -linux" command.
func handleLinuxInstall() error {
	return installLinuxVM()
}
