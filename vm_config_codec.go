package main

import (
	"fmt"
	"os"
	"path/filepath"

	vz "github.com/tmc/apple/virtualization"
	"github.com/tmc/apple/x/vzkit/configcodec"
)

const vmFrameworkConfigFileName = "framework-config.vzcfg"

func handleVMConfigCommand(args []string) error {
	if len(args) == 0 {
		printVMConfigUsage(os.Stderr)
		return fmt.Errorf("command required")
	}

	switch args[0] {
	case "help", "-h", "--help":
		printVMConfigUsage(os.Stderr)
		return nil
	case "export":
		if len(args) < 2 {
			return fmt.Errorf("Usage: vz-macos vm config export <path>")
		}
		return exportVMFrameworkConfig(args[1])
	case "import":
		if len(args) < 2 {
			return fmt.Errorf("Usage: vz-macos vm config import <path>")
		}
		return importVMFrameworkConfig(args[1])
	default:
		return fmt.Errorf("unknown vm config command: %s", args[0])
	}
}

func exportVMFrameworkConfig(destPath string) error {
	if err := ensureFrameworkConfigInputs(); err != nil {
		return err
	}

	resolvedDiskPath, err := currentVMFrameworkDiskPath()
	if err != nil {
		return err
	}

	cfg, err := buildSelectedVMFrameworkConfiguration(resolvedDiskPath)
	if err != nil {
		return err
	}

	encoded, err := configcodec.EncodeAtBasePath(vmDir, cfg, configcodec.DefaultFormat)
	if err != nil {
		return fmt.Errorf("encode framework config: %w", err)
	}
	summary := summarizeExportedFrameworkConfig(cfg)
	summary.EncodedBytes = len(encoded)
	printFrameworkConfigSummary("Export snapshot", summary)

	if err := writeFrameworkConfigBytes(destPath, encoded); err != nil {
		return err
	}

	fmt.Printf("Wrote %d bytes to %s\n", len(encoded), destPath)
	return nil
}

func importVMFrameworkConfig(sourcePath string) error {
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return fmt.Errorf("read framework config: %w", err)
	}

	cfg, err := configcodec.DecodeAtBasePath(vmDir, data, configcodec.DefaultFormat)
	if err != nil {
		return fmt.Errorf("decode framework config: %w", err)
	}

	if err := writeFrameworkConfigBytes(filepath.Join(vmDir, vmFrameworkConfigFileName), data); err != nil {
		return err
	}

	summary := summarizeExportedFrameworkConfig(vz.VZVirtualMachineConfigurationFromID(cfg.GetID()))
	summary.EncodedBytes = len(data)
	printFrameworkConfigSummary("Imported snapshot", summary)
	fmt.Printf("Stored raw snapshot at %s\n", filepath.Join(vmDir, vmFrameworkConfigFileName))
	return nil
}

func currentVMFrameworkDiskPath() (string, error) {
	path := diskPath
	if path == "" {
		if vmDir == "" {
			return "", fmt.Errorf("vm directory not set")
		}
		if detectOSType(vmDir) == "Linux" {
			path = filepath.Join(vmDir, "linux-disk.img")
		} else {
			path = filepath.Join(vmDir, "disk.img")
		}
	}
	path = resolvePath(path)
	if path == "" {
		return "", fmt.Errorf("disk path not available")
	}
	return path, nil
}

func buildSelectedVMFrameworkConfiguration(diskImagePath string) (vz.VZVirtualMachineConfiguration, error) {
	switch detectOSType(vmDir) {
	case "Linux":
		return buildLinuxVMConfiguration(diskImagePath)
	case "macOS":
		return buildVMConfiguration(diskImagePath)
	default:
		return vz.VZVirtualMachineConfiguration{}, fmt.Errorf("cannot determine vm type for %s", vmDir)
	}
}

func ensureFrameworkConfigInputs() error {
	switch detectOSType(vmDir) {
	case "macOS":
		for _, name := range []string{"aux.img", "hw.model", "machine.id"} {
			if err := ensureReadableFile(filepath.Join(vmDir, name)); err != nil {
				return err
			}
		}
		if networkMode != "none" {
			if err := ensureReadableFile(filepath.Join(vmDir, "mac.address")); err != nil {
				return err
			}
		}
	case "Linux":
		for _, name := range []string{"linux-machine.id"} {
			if err := ensureReadableFile(filepath.Join(vmDir, name)); err != nil {
				return err
			}
		}
		if kernelPath != "" {
			if err := ensureReadableFile(resolvePath(kernelPath)); err != nil {
				return err
			}
			if initrdPath != "" {
				if err := ensureReadableFile(resolvePath(initrdPath)); err != nil {
					return err
				}
			}
		} else {
			if err := ensureReadableFile(filepath.Join(vmDir, "efi.nvram")); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("cannot determine vm type for %s", vmDir)
	}
	return nil
}

func ensureReadableFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("missing required file: %s", filepath.Base(path))
		}
		return fmt.Errorf("stat %s: %w", path, err)
	}
	if info.Size() == 0 {
		return fmt.Errorf("required file is empty: %s", filepath.Base(path))
	}
	return nil
}

func writeFrameworkConfigBytes(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write framework config: %w", err)
	}
	return nil
}

type frameworkConfigSummary struct {
	CPU             uint
	MemoryBytes     uint64
	BootLoader      string
	Platform        string
	StorageDevices  int
	GraphicsDevices int
	NetworkDevices  int
	ConsoleDevices  int
	SerialPorts     int
	EntropyDevices  int
	AudioDevices    int
	DirectoryShares int
	Keyboards       int
	PointingDevices int
	UsbControllers  int
	SocketDevices   int
	MemoryBalloon   int
	ValidateOK      bool
	ValidateErr     string
	SaveRestoreOK   bool
	SaveRestoreErr  string
	EncodedBytes    int
}

func summarizeExportedFrameworkConfig(config vz.VZVirtualMachineConfiguration) frameworkConfigSummary {
	summary := frameworkConfigSummary{
		CPU:             config.CPUCount(),
		MemoryBytes:     config.MemorySize(),
		BootLoader:      fmt.Sprintf("%T", config.BootLoader()),
		Platform:        fmt.Sprintf("%T", config.Platform()),
		StorageDevices:  len(config.StorageDevices()),
		GraphicsDevices: len(config.GraphicsDevices()),
		NetworkDevices:  len(config.NetworkDevices()),
		ConsoleDevices:  len(config.ConsoleDevices()),
		SerialPorts:     len(config.SerialPorts()),
		EntropyDevices:  len(config.EntropyDevices()),
		AudioDevices:    len(config.AudioDevices()),
		DirectoryShares: len(config.DirectorySharingDevices()),
		Keyboards:       len(config.Keyboards()),
		PointingDevices: len(config.PointingDevices()),
		UsbControllers:  len(config.UsbControllers()),
		SocketDevices:   len(config.SocketDevices()),
		MemoryBalloon:   len(config.MemoryBalloonDevices()),
	}
	if ok, err := config.ValidateWithError(); err != nil {
		summary.ValidateErr = err.Error()
	} else {
		summary.ValidateOK = ok
	}
	if ok, err := config.ValidateSaveRestoreSupportWithError(); err != nil {
		summary.SaveRestoreErr = err.Error()
	} else {
		summary.SaveRestoreOK = ok
	}
	return summary
}

func printFrameworkConfigSummary(label string, summary frameworkConfigSummary) {
	fmt.Println(label + ":")
	fmt.Printf("  cpu: %d\n", summary.CPU)
	fmt.Printf("  memory: %s\n", FormatSize(int64(summary.MemoryBytes)))
	fmt.Printf("  boot loader: %s\n", emptyIfBlank(summary.BootLoader))
	fmt.Printf("  platform: %s\n", emptyIfBlank(summary.Platform))
	fmt.Printf("  devices: storage=%d graphics=%d network=%d console=%d serial=%d entropy=%d audio=%d shared=%d keyboards=%d pointing=%d usb=%d sockets=%d balloon=%d\n",
		summary.StorageDevices,
		summary.GraphicsDevices,
		summary.NetworkDevices,
		summary.ConsoleDevices,
		summary.SerialPorts,
		summary.EntropyDevices,
		summary.AudioDevices,
		summary.DirectoryShares,
		summary.Keyboards,
		summary.PointingDevices,
		summary.UsbControllers,
		summary.SocketDevices,
		summary.MemoryBalloon,
	)
	switch {
	case summary.ValidateErr != "":
		fmt.Printf("  validate: failed (%s)\n", summary.ValidateErr)
	case summary.ValidateOK:
		fmt.Println("  validate: ok")
	default:
		fmt.Println("  validate: not available")
	}
	switch {
	case summary.SaveRestoreErr != "":
		fmt.Printf("  save/restore: failed (%s)\n", summary.SaveRestoreErr)
	case summary.SaveRestoreOK:
		fmt.Println("  save/restore: supported")
	default:
		fmt.Println("  save/restore: not available")
	}
	if summary.EncodedBytes > 0 {
		fmt.Printf("  encoded bytes: %d\n", summary.EncodedBytes)
	}
}

func emptyIfBlank(s string) string {
	if s == "" {
		return "<none>"
	}
	return s
}
