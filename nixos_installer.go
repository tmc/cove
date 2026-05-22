package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/tmc/apple/dispatch"
	vz "github.com/tmc/apple/virtualization"
	"github.com/tmc/cove/internal/nixos"
)

func installNixOSVM(resolvedDiskPath string, provConfig LinuxProvisionConfig) error {
	resolvedISO, err := ensureLinuxISOForVariant(LinuxVariantNixOS)
	if err != nil {
		return fmt.Errorf("ensure nixos ISO: %w", err)
	}
	fmt.Printf("Using ISO: %s\n", resolvedISO)

	seedISO, err := createNixOSSeedISO(provConfig)
	if err != nil {
		return fmt.Errorf("create nixos seed ISO: %w", err)
	}
	fmt.Printf("Created NixOS seed ISO: %s\n", seedISO)

	if _, err := os.Stat(resolvedDiskPath); os.IsNotExist(err) {
		fmt.Printf("Creating disk image: %s (%d GB)\n", resolvedDiskPath, diskSizeGB)
		if err := createInstallDiskImage(resolvedDiskPath, diskSizeGB); err != nil {
			return fmt.Errorf("create disk image: %w", err)
		}
	}

	fmt.Printf("Configuring VM: %d CPUs, %d GB RAM\n", cpuCount, memoryGB)
	config, err := buildLinuxInstallConfiguration(resolvedDiskPath, resolvedISO, seedISO, "", "", "", false)
	if err != nil {
		return fmt.Errorf("build configuration: %w", err)
	}
	config.Retain()

	fmt.Println("Validating configuration...")
	if _, err := config.ValidateWithError(); err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}
	fmt.Println("  ✓ Configuration valid")

	fmt.Println()
	fmt.Println("=== NixOS Installation Starting ===")
	fmt.Println("Using EFI boot from the NixOS ISO.")
	fmt.Printf("  Username: %s\n", provConfig.Username)
	fmt.Printf("  Hostname: %s\n", provConfig.Hostname)
	fmt.Println("  Seed volume: COVE-NIXOS")
	fmt.Println()
	fmt.Println("From the live installer, mount the seed volume and run install-nixos.sh.")
	fmt.Println("The script writes /mnt/etc/nixos/configuration.nix and runs nixos-install.")
	fmt.Println()

	vmQueue := dispatch.QueueCreate("com.appledocs.vz.nixos.install")
	vm := vz.NewVirtualMachineWithConfigurationQueue(&config, vmQueue)
	if vm.ID == 0 {
		return fmt.Errorf("failed to create virtual machine")
	}
	vm.Retain()
	if err := startVMWithQueue(vm, vmQueue); err != nil {
		return err
	}
	if err := verifyLinuxInstallBootable(resolvedDiskPath); err != nil {
		return err
	}
	if err := writeLinuxInstalledMarker(vmDir, LinuxVariantNixOS); err != nil {
		return fmt.Errorf("write install marker: %w", err)
	}
	return nil
}

func createNixOSSeedISO(config LinuxProvisionConfig) (string, error) {
	seedDir := filepath.Join(vmDir, "nixos-seed")
	if err := os.RemoveAll(seedDir); err != nil {
		return "", fmt.Errorf("reset nixos seed: %w", err)
	}
	if err := os.MkdirAll(seedDir, 0755); err != nil {
		return "", fmt.Errorf("create nixos seed: %w", err)
	}

	text, err := nixos.RenderConfiguration(nixos.Config{
		Hostname:  config.Hostname,
		Username:  config.Username,
		Password:  config.Password,
		SSHPubKey: config.SSHPubKey,
	})
	if err != nil {
		return "", err
	}
	if err := nixos.ValidateConfiguration(text); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(seedDir, nixos.ConfigPath), []byte(text), 0644); err != nil {
		return "", fmt.Errorf("write configuration.nix: %w", err)
	}
	script := nixos.RenderInstallScript(text)
	scriptPath := filepath.Join(seedDir, nixos.InstallScript)
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		return "", fmt.Errorf("write install script: %w", err)
	}

	isoPath := filepath.Join(vmDir, "nixos-seed.iso")
	_ = os.Remove(isoPath)
	cmd := nixosSeedISOCommand(isoPath, seedDir)
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("create nixos seed ISO: %w: %s", err, output)
	}
	return isoPath, nil
}

func nixosSeedISOCommand(isoPath, seedDir string) *exec.Cmd {
	if mkisofs, err := exec.LookPath("mkisofs"); err == nil {
		return exec.Command(mkisofs,
			"-output", isoPath,
			"-volid", nixos.SeedVolumeID,
			"-joliet",
			"-rock",
			seedDir,
		)
	}
	return exec.Command("hdiutil", "makehybrid",
		"-o", isoPath,
		"-joliet",
		"-iso",
		"-default-volume-name", nixos.SeedVolumeID,
		"-iso-volume-name", nixos.SeedVolumeID,
		"-joliet-volume-name", nixos.SeedVolumeID,
		seedDir,
	)
}
