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
	"archive/tar"
	"bytes"
	"compress/gzip"
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
	agentstate "github.com/tmc/vz-macos/internal/agent"
	"github.com/tmc/vz-macos/internal/vmconfig"
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

// LinuxVariant identifies the Linux install source to provision.
type LinuxVariant string

const (
	LinuxVariantServer        LinuxVariant = "ubuntu"
	LinuxVariantDesktop       LinuxVariant = "ubuntu-desktop"
	LinuxVariantDebian        LinuxVariant = "debian"
	LinuxVariantFedora        LinuxVariant = "fedora"
	LinuxVariantAlpine        LinuxVariant = "alpine"
	LinuxVariantUbuntuServer               = LinuxVariantServer
	LinuxVariantUbuntuDesktop              = LinuxVariantDesktop
)

const linuxAutoinstallPath = "autoinstall.yaml"

const (
	linuxEFIBootArtifactsDir = "EFI/VZMACOS"
	linuxRootUUIDFileName    = "linux-root-uuid.txt"
)

func currentLinuxVariant() LinuxVariant {
	variant, err := parseLinuxVariant(linuxDistro, linuxDesktop)
	if err == nil {
		return variant
	}
	return LinuxVariant(strings.ToLower(strings.TrimSpace(linuxDistro)))
}

func parseLinuxVariant(distro string, desktop bool) (LinuxVariant, error) {
	name := strings.ToLower(strings.TrimSpace(distro))
	if desktop {
		switch name {
		case "", "ubuntu", "server", "ubuntu-server", "desktop", "ubuntu-desktop":
			return LinuxVariantDesktop, nil
		default:
			return "", fmt.Errorf("-desktop only supports ubuntu, not %q", name)
		}
	}
	switch name {
	case "", "ubuntu", "server", "ubuntu-server":
		return LinuxVariantServer, nil
	case "desktop", "ubuntu-desktop":
		return LinuxVariantDesktop, nil
	case "debian":
		return LinuxVariantDebian, nil
	case "fedora":
		return LinuxVariantFedora, nil
	case "alpine":
		return LinuxVariantAlpine, nil
	default:
		return "", fmt.Errorf("unsupported linux distro %q (supported: ubuntu, debian, fedora, alpine)", name)
	}
}

func defaultLinuxUser(variant LinuxVariant) string {
	switch variant {
	case LinuxVariantDebian:
		return "debian"
	case LinuxVariantFedora:
		return "fedora"
	case LinuxVariantAlpine:
		return "alpine"
	default:
		return "ubuntu"
	}
}

func (v LinuxVariant) distroName() string {
	switch v {
	case LinuxVariantDesktop:
		return "ubuntu"
	default:
		return string(v)
	}
}

func (v LinuxVariant) sourceID() string {
	switch v {
	case LinuxVariantDesktop:
		// Server-ISO path: leave source empty so Subiquity falls back to its
		// default and ubuntu-desktop layers via apt.
		// OEM path: select the desktop source baked into the Desktop ISO.
		if strings.EqualFold(linuxDesktopInstaller, "oem") {
			return "ubuntu-desktop"
		}
		return ""
	case LinuxVariantServer:
		return "ubuntu-server"
	default:
		return ""
	}
}

func (v LinuxVariant) installISOVariant() LinuxVariant {
	switch v {
	case LinuxVariantDesktop:
		// "oem" mode boots the desktop ISO directly and runs Subiquity's OEM
		// autoinstall (Ubuntu 23.04+). The default "server" mode boots the
		// server ISO and apt-installs ubuntu-desktop on top — slower at install
		// time but the most-tested cove path.
		if strings.EqualFold(linuxDesktopInstaller, "oem") {
			return LinuxVariantDesktop
		}
		return LinuxVariantServer
	default:
		return v
	}
}

func (v LinuxVariant) displayName() string {
	switch v {
	case LinuxVariantDesktop:
		return "Ubuntu Desktop"
	case LinuxVariantDebian:
		return "Debian"
	case LinuxVariantFedora:
		return "Fedora"
	case LinuxVariantAlpine:
		return "Alpine"
	default:
		return "Ubuntu Server"
	}
}

func validateLinuxVariant(v LinuxVariant) error {
	_, err := parseLinuxVariant(string(v), false)
	return err
}

func linuxInstallCommandLineForVariant(variant LinuxVariant, seedAddress string) string {
	switch variant {
	case LinuxVariantDebian:
		return fmt.Sprintf("auto=true priority=critical preseed/url=http://%s/preseed.cfg debian-installer=en_US.UTF-8 locale=en_US.UTF-8 keyboard-configuration/xkb-keymap=us netcfg/choose_interface=auto interface=auto console=tty0", seedAddress)
	case LinuxVariantFedora:
		return fmt.Sprintf("inst.ks=http://%s/ks.cfg inst.text console=tty0", seedAddress)
	case LinuxVariantAlpine:
		return fmt.Sprintf("modules=loop,squashfs,sd-mod,usb-storage quiet console=tty0 apkovl=http://%s/cove.apkovl.tar.gz cove_answers=http://%s/setup-alpine.answers", seedAddress, seedAddress)
	default:
		return linuxInstallCommandLine(seedAddress)
	}
}

func linuxInstallCommandLine(seedAddress string) string {
	// Subiquity (Ubuntu 22.04+) skips the "Continue with autoinstall? (yes/no)"
	// prompt only when it observes the bare token `autoinstall` in
	// /proc/cmdline AFTER the `---` separator, which is the documented
	// boundary for kernel-vs-init args. With direct kernel boot we control
	// the full cmdline, so the format below mirrors what Ubuntu's GRUB
	// autoinstall menu entry produces. Pairs with the `interactive-sections: []`
	// declaration in generateAutoinstallData so even if the kernel arg fails
	// to suppress the prompt on a future Subiquity version, the empty list
	// prevents per-section "Continue?" prompts from firing.
	_ = seedAddress
	return "boot=casper ds=nocloud console=tty0 --- autoinstall"
}

// DefaultLinuxProvisionConfig returns default provisioning settings.
func DefaultLinuxProvisionConfig() LinuxProvisionConfig {
	variant := currentLinuxVariant()
	user := defaultLinuxUser(variant)
	return LinuxProvisionConfig{
		Username:     user,
		Password:     user,
		Hostname:     variant.distroName() + "-vm",
		TimeZone:     "UTC",
		Locale:       "en_US.UTF-8",
		InstallAgent: false,
		Variant:      variant,
	}
}

// installLinuxVM performs automated Linux (Ubuntu) installation.
func installLinuxVM() error {
	fmt.Println("=== Linux VM Installer ===")

	resolvedDiskPath := diskPath
	if resolvedDiskPath == "" {
		resolvedDiskPath = filepath.Join(vmDir, "linux-disk.img")
	}
	if !forceInstall {
		if _, err := os.Stat(resolvedDiskPath); err == nil {
			if ok, err := completeExistingLinuxInstall(vmDir, resolvedDiskPath, currentLinuxVariant()); err != nil {
				fmt.Printf("warning: inspect existing linux disk: %v\n", err)
			} else if ok {
				fmt.Println("Existing Linux installation is bootable; using it.")
				return nil
			}
		}
	}

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
	if err := validateLinuxVariant(provConfig.Variant); err != nil {
		return err
	}
	if provisionUser != "" {
		provConfig.Username = provisionUser
	}
	if provisionPassword != "" {
		provConfig.Password = provisionPassword
	}
	// Desktop variant: enable GDM autologin so `cove run -linux -gui` boots
	// straight to the Ubuntu desktop without a manual password entry.
	// linuxAutoLoginLateCommand writes /target/etc/gdm3/custom.conf only
	// when both AutoLogin and Variant==Desktop are set.
	if provConfig.Variant == LinuxVariantDesktop {
		provConfig.AutoLogin = true
	}
	if err := vmconfig.SetGuestUser(vmDir, 1000, 1000); err != nil {
		fmt.Printf("warning: save linux guest user mapping: %v\n", err)
	}
	provConfig.InstallAgent = !noAgent && sandboxAllowsAgentProvision()
	if err := agentstate.SetRequested(vmDir, agentstate.PlatformLinux, provConfig.InstallAgent, agentstate.SourceInstall); err != nil {
		fmt.Printf("warning: save guest agent config: %v\n", err)
	}

	fmt.Printf("Installing distro: %s\n", provConfig.Variant.displayName())

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

	if !forceInstall {
		if _, err := os.Stat(resolvedDiskPath); err == nil {
			if ok, err := completeExistingLinuxInstall(vmDir, resolvedDiskPath, provConfig.Variant); err != nil {
				fmt.Printf("warning: inspect existing linux disk: %v\n", err)
			} else if ok {
				fmt.Println("Existing Linux installation is bootable; using it.")
				return nil
			}
		}
	}

	// Create disk image if it doesn't exist
	if _, err := os.Stat(resolvedDiskPath); os.IsNotExist(err) {
		fmt.Printf("Creating disk image: %s (%d GB)\n", resolvedDiskPath, diskSizeGB)
		if err := createInstallDiskImage(resolvedDiskPath, diskSizeGB); err != nil {
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
			installCmdLine = linuxInstallCommandLineForVariant(provConfig.Variant, seedAddress)
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
	fmt.Printf("=== %s Installation Starting ===\n", provConfig.Variant.displayName())
	if useDirectBoot {
		fmt.Println("Using direct kernel boot with autoinstall (no confirmation prompt).")
	} else {
		fmt.Println("Using EFI boot from ISO.")
		fmt.Println("The GRUB bootloader will start automatically after ~5 seconds.")
		fmt.Println("Cloud-init will detect the autoinstall configuration.")
		fmt.Println("NOTE: this path uses the ISO's GRUB; if the bundled GRUB does not pass `autoinstall` to the kernel,")
		fmt.Println("Subiquity may prompt with \"Continue with autoinstall?\" — answer \"yes\" once to proceed.")
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
	// 1. Main disk (Virtio block or NVMe)
	// 2. Cloud-init ISO (Virtio block, becomes /dev/vdb or sr0)
	// 3. Installation ISO (USB mass storage — matches Code-Hex/vz pattern)

	// Main disk
	diskURL := foundation.NewURLFileURLWithPath(diskPath)
	diskAttachment, err := newDiskAttachment(diskURL, false, DiskCacheEphemeral)
	if err != nil {
		return config, fmt.Errorf("create disk attachment: %w", err)
	}
	diskAttachment.Retain()
	diskStorage, err := createLinuxStorageDeviceWithAttachment(diskAttachment.VZStorageDeviceAttachment)
	if err != nil {
		return config, fmt.Errorf("create disk storage device: %w", err)
	}

	// Cloud-init data as USB mass storage. The casper live environment's
	// initrd may not include virtio block drivers, so CIDATA on a Virtio
	// block device won't be visible to cloud-init. USB mass storage is
	// universally supported in the initramfs.
	cloudInitURL := foundation.NewURLFileURLWithPath(cloudInitISO)
	cloudInitAttachment, err := newDiskAttachment(cloudInitURL, true, DiskCacheReadOnly)
	if err != nil {
		return config, fmt.Errorf("create cloud-init attachment: %w", err)
	}
	cloudInitAttachment.Retain()
	cloudInitUSB := vz.NewUSBMassStorageDeviceConfigurationWithAttachment(&cloudInitAttachment.VZStorageDeviceAttachment)
	cloudInitUSB.Retain()

	// Installation ISO as USB mass storage (EFI firmware can boot from USB)
	isoURL := foundation.NewURLFileURLWithPath(installISO)
	isoAttachment, err := newDiskAttachment(isoURL, true, DiskCacheReadOnly)
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
		diskStorage,
		vz.VZStorageDeviceConfigurationFromID(cloudInitUSB.ID),
		vz.VZStorageDeviceConfigurationFromID(isoUSB.ID),
	})

	// Graphics. Use the same scanout dimensions as runLinuxVM so the framebuffer
	// matches the host window 1:1. Subiquity's installer renders fine at this
	// size; larger framebuffers get scaled by the NSWindow on macOS hosts and
	// hurt readability.
	graphicsConfig := vz.NewVZVirtioGraphicsDeviceConfiguration()
	scanout := vz.NewVirtioGraphicsScanoutConfigurationWithWidthInPixelsHeightInPixels(defaultWindowWidth, defaultWindowHeight)
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
					setSerialPorts(config, vz.VZSerialPortConfigurationFromID(serialPort.ID))
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
		// `autoinstall` belongs after `---` for Subiquity's prompt-suppression
		// rules (see linuxInstallCommandLine). This fallback is only used when
		// the caller didn't pass an explicit cmdline; the http-seed-aware
		// builder above is preferred for installs.
		cmdLine = "boot=casper ds=nocloud console=hvc0 console=tty0 --- autoinstall"
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
		{"install.a64/vmlinuz", "install.a64/initrd.gz"},
		{"images/pxeboot/vmlinuz", "images/pxeboot/initrd.img"},
		{"boot/vmlinuz-virt", "boot/initramfs-virt"},
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
	files           map[string][]byte
}

func buildLinuxCloudInitData(config LinuxProvisionConfig, includeAgent bool, agentURL string) linuxCloudInitData {
	metaData := generateMetaData(config)
	vendorData := "#cloud-config\n{}\n"
	switch config.Variant {
	case LinuxVariantDebian:
		preseed := generateDebianPreseed(config, includeAgent, agentURL)
		return linuxCloudInitData{
			userData:   vendorData,
			metaData:   metaData,
			vendorData: vendorData,
			files: map[string][]byte{
				"user-data":   []byte(vendorData),
				"meta-data":   []byte(metaData),
				"vendor-data": []byte(vendorData),
				"preseed.cfg": []byte(preseed),
			},
		}
	case LinuxVariantFedora:
		kickstart := generateFedoraKickstart(config, includeAgent, agentURL)
		return linuxCloudInitData{
			userData:   vendorData,
			metaData:   metaData,
			vendorData: vendorData,
			files: map[string][]byte{
				"user-data":   []byte(vendorData),
				"meta-data":   []byte(metaData),
				"vendor-data": []byte(vendorData),
				"ks.cfg":      []byte(kickstart),
			},
		}
	case LinuxVariantAlpine:
		answers := generateAlpineAnswers(config)
		apkovl, err := buildAlpineAPKOVL()
		if err != nil {
			apkovl = nil
		}
		files := map[string][]byte{
			"user-data":            []byte(vendorData),
			"meta-data":            []byte(metaData),
			"vendor-data":          []byte(vendorData),
			"setup-alpine.answers": []byte(answers),
		}
		if apkovl != nil {
			files["cove.apkovl.tar.gz"] = apkovl
		}
		return linuxCloudInitData{
			userData:   vendorData,
			metaData:   metaData,
			vendorData: vendorData,
			files:      files,
		}
	}
	autoinstallData := generateAutoinstallData(config, includeAgent, agentURL)
	return linuxCloudInitData{
		userData:        "#cloud-config\n" + autoinstallData,
		metaData:        metaData,
		vendorData:      vendorData,
		autoinstallData: autoinstallData,
		files: map[string][]byte{
			"user-data":          []byte("#cloud-config\n" + autoinstallData),
			"meta-data":          []byte(metaData),
			"vendor-data":        []byte(vendorData),
			linuxAutoinstallPath: []byte(autoinstallData),
		},
	}
}

func generateDebianPreseed(config LinuxProvisionConfig, includeAgent bool, agentURL string) string {
	late := "in-target systemctl enable ssh"
	if includeAgent {
		late = late + "; " + agentInstallShell("/target", agentURL, "apt-get update && apt-get install -y ca-certificates curl wget")
	}
	return fmt.Sprintf(`d-i debian-installer/locale string %s
d-i keyboard-configuration/xkb-keymap select us
d-i netcfg/choose_interface select auto
d-i netcfg/get_hostname string %s
d-i passwd/root-login boolean false
d-i passwd/user-fullname string %s
d-i passwd/username string %s
d-i passwd/user-password password %s
d-i passwd/user-password-again password %s
d-i clock-setup/utc boolean true
d-i time/zone string %s
d-i partman-auto/method string regular
d-i partman-auto/choose_recipe select atomic
d-i partman-partitioning/confirm_write_new_label boolean true
d-i partman/choose_partition select finish
d-i partman/confirm boolean true
d-i partman/confirm_nooverwrite boolean true
d-i pkgsel/include string openssh-server sudo ca-certificates curl wget
d-i grub-installer/only_debian boolean true
d-i finish-install/reboot_in_progress note
d-i preseed/late_command string %s
`, config.Locale, config.Hostname, config.Username, config.Username, config.Password, config.Password, config.TimeZone, late)
}

func generateFedoraKickstart(config LinuxProvisionConfig, includeAgent bool, agentURL string) string {
	post := "systemctl enable sshd"
	if includeAgent {
		post = post + "\n" + agentInstallShell("", agentURL, "dnf -y install ca-certificates curl wget")
	}
	return fmt.Sprintf(`text
reboot
lang %s
keyboard us
timezone %s --utc
network --bootproto=dhcp --device=link --activate --hostname=%s
rootpw --lock
user --name=%s --password=%s --plaintext --groups=wheel
zerombr
clearpart --all --initlabel
autopart --type=plain --fstype=ext4
bootloader --location=mbr
%%packages
@core
openssh-server
sudo
curl
wget
%%end
%%post --log=/root/cove-post.log
%s
%%end
`, config.Locale, config.TimeZone, config.Hostname, config.Username, config.Password, post)
}

func generateAlpineAnswers(config LinuxProvisionConfig) string {
	return fmt.Sprintf(`KEYMAPOPTS="us us"
HOSTNAMEOPTS="-n %s"
INTERFACESOPTS="auto lo
iface lo inet loopback
auto eth0
iface eth0 inet dhcp"
DNSOPTS="-d local -n 1.1.1.1"
TIMEZONEOPTS="-z %s"
PROXYOPTS="none"
APKREPOSOPTS="-1"
SSHDOPTS="-c openssh"
NTPOPTS="-c chrony"
DISKOPTS="-m sys /dev/vda"
USEROPTS="-a -u %s"
ROOTPW="%s"
`, config.Hostname, config.TimeZone, config.Username, config.Password)
}

func buildAlpineAPKOVL() ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	entries := []struct {
		name string
		mode int64
		body string
	}{
		{"etc/local.d/cove.start", 0755, alpineSetupScript()},
		{"etc/runlevels/default/local", 0777, ""},
	}
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Mode: e.mode}
		if e.name == "etc/runlevels/default/local" {
			hdr.Typeflag = tar.TypeSymlink
			hdr.Linkname = "/etc/init.d/local"
		} else {
			hdr.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, err
		}
		if e.body != "" {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				return nil, err
			}
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func alpineSetupScript() string {
	return `#!/bin/sh
set -eu
marker=/var/lib/cove-setup.done
test -e "$marker" && exit 0
url=$(sed -n 's/.*cove_answers=\([^ ]*\).*/\1/p' /proc/cmdline)
test -n "$url"
wget -O /tmp/setup-alpine.answers "$url"
setup-alpine -f /tmp/setup-alpine.answers
touch "$marker"
poweroff
`
}

func agentInstallShell(target, agentURL, installDeps string) string {
	prefix := ""
	if target != "" {
		prefix = target
	}
	install := fmt.Sprintf(`%s || true
if command -v curl >/dev/null 2>&1; then
  curl -fsSL -o %s/usr/local/bin/vz-agent %q
elif command -v wget >/dev/null 2>&1; then
  wget -O %s/usr/local/bin/vz-agent %q
fi
chmod 755 %s/usr/local/bin/vz-agent
mkdir -p %s/etc/systemd/system
cat > %s/etc/systemd/system/vz-agent.service <<'EOF'
[Unit]
Description=cove Guest Agent
After=network-online.target
Wants=network-online.target
[Service]
Type=simple
ExecStart=/usr/local/bin/vz-agent -mode daemon
Restart=always
RestartSec=2
[Install]
WantedBy=multi-user.target
EOF
systemctl enable vz-agent`, installDeps, prefix, agentURL, prefix, agentURL, prefix, prefix, prefix)
	if agentURL == "" {
		return "true"
	}
	return install
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
	if err := os.RemoveAll(cloudInitDir); err != nil {
		return "", "", fmt.Errorf("reset cloud-init directory: %w", err)
	}
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

	seed := buildLinuxCloudInitData(config, agentIncluded, "")
	for name, data := range seed.files {
		if strings.Contains(name, string(os.PathSeparator)) || name == "" {
			return "", "", fmt.Errorf("invalid seed file %q", name)
		}
		if err := os.WriteFile(filepath.Join(cloudInitDir, name), data, 0644); err != nil {
			return "", "", fmt.Errorf("write %s: %w", name, err)
		}
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

func linuxDesktopUserLateCommands(config LinuxProvisionConfig, hashedPassword string) string {
	if config.Variant != LinuxVariantDesktop || !strings.EqualFold(linuxDesktopInstaller, "oem") {
		return ""
	}
	username := shellQuote(config.Username)
	password := shellQuote(hashedPassword)
	gecos := shellQuote(config.Username)
	return fmt.Sprintf(`
    - |
      if ! chroot /target id -u %[1]s >/dev/null 2>&1; then
        chroot /target useradd -m -s /bin/bash -c %[3]s %[1]s
      fi
      chroot /target usermod -p %[2]s %[1]s
      chroot /target usermod -aG adm,cdrom,sudo,dip,plugdev,users,lpadmin %[1]s
      mkdir -p /target/home/%[4]s/.config /target/var/lib/AccountsService/users
      touch /target/home/%[4]s/.config/gnome-initial-setup-done
      chroot /target chown -R %[1]s:%[1]s /home/%[4]s/.config
      printf '%%s\n' '[User]' 'SystemAccount=false' > /target/var/lib/AccountsService/users/%[4]s
      rm -f /target/etc/cloud/cloud.cfg.d/90-installer-network.cfg
      mkdir -p /target/etc/cloud
      printf 'Disabled by cove after OEM desktop provisioning.\n' > /target/etc/cloud/cloud-init.disabled`, username, password, gecos, config.Username)
}

func linuxDesktopPackagesSection(config LinuxProvisionConfig) string {
	if config.Variant != LinuxVariantDesktop {
		return ""
	}
	// OEM mode boots the Desktop ISO so ubuntu-desktop is already baked in.
	// The Server-ISO path needs to apt-install it during provisioning.
	if strings.EqualFold(linuxDesktopInstaller, "oem") {
		return ""
	}
	return `
  packages:
    - ubuntu-desktop`
}

// linuxOEMInstallSection emits the autoinstall `oem` block when the user
// opted into the Desktop-ISO/OEM install path. Subiquity 23.04+ recognises
// `oem: install: true` as a request to run the OEM postinstall hooks (locale,
// keyboard, hostname pre-configured but the GNOME Initial Setup wizard is
// suppressed for the cove-provisioned identity).
func linuxOEMInstallSection(config LinuxProvisionConfig) string {
	if config.Variant != LinuxVariantDesktop {
		return ""
	}
	if !strings.EqualFold(linuxDesktopInstaller, "oem") {
		return ""
	}
	return `
  oem:
    install: true`
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
	oemSection := linuxOEMInstallSection(config)
	desktopUserLateCommands := linuxDesktopUserLateCommands(config, hashedPassword)

	return fmt.Sprintf(`autoinstall:
  version: 1
  interactive-sections: []
  locale: %s
  keyboard:
    layout: us
%s%s%s
  identity:
    hostname: %s
    username: %s
    password: %s%s
  shutdown: poweroff
%s
%s
  late-commands:
    - curtin in-target --target=/target -- systemctl enable ssh%s%s%s%s%s
  error-commands:
    - cat /var/log/installer/curtin-install.log | tail -200 > /dev/hvc0 2>&1 || true
    - cat /var/crash/*.crash > /dev/hvc0 2>&1 || true
  user-data:
    disable_root: false
    timezone: %s
`, config.Locale, packagesSection, sourceSection, oemSection, config.Hostname, config.Username, hashedPassword, sshSection, earlyCommandsSection, storageSection, bootloaderLateCommands, desktopLateCommands, agentLateCommands, desktopUserLateCommands, autoLoginLateCommands, config.TimeZone)
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

func completeExistingLinuxInstall(vmDir, diskPath string, variant LinuxVariant) (bool, error) {
	if _, err := os.Stat(linuxInstalledMarkerPath(vmDir)); err == nil {
		return true, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	if err := verifyLinuxInstallBootable(diskPath); err != nil {
		return false, nil
	}
	if err := writeLinuxInstalledMarker(vmDir, variant); err != nil {
		return false, fmt.Errorf("write install marker: %w", err)
	}
	return true, nil
}

func verifyLinuxInstallBootable(diskPath string) error {
	devices, err := attachLinuxDiskReadOnlyWithRetry(diskPath, 45*time.Second)
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

func attachLinuxDiskReadOnlyWithRetry(diskPath string, timeout time.Duration) ([]string, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		devices, err := attachLinuxDiskReadOnly(diskPath)
		if err == nil {
			return devices, nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			return nil, lastErr
		}
		time.Sleep(500 * time.Millisecond)
	}
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
