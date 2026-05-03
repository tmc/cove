package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	vz "github.com/tmc/apple/virtualization"
	"github.com/tmc/apple/x/vzkit/configcodec"
	"github.com/tmc/vz-macos/internal/bytefmt"
	"github.com/tmc/vz-macos/internal/vmconfig"
)

const vmFrameworkConfigFileName = "framework-config.vzcfg"
const frameworkConfigFormatPrefix = "vzcfg-format:"

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
			return fmt.Errorf("Usage: cove vm config export <path>")
		}
		return exportVMFrameworkConfig(args[1])
	case "import":
		if len(args) < 2 {
			return fmt.Errorf("Usage: cove vm config import <path>")
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

	encoded, formatUsed, err := encodeFrameworkConfig(cfg)
	if err != nil {
		return fmt.Errorf("encode framework config: %w", err)
	}
	snapshot := marshalFrameworkConfigSnapshot(formatUsed, encoded)
	summary := summarizeExportedFrameworkConfig(cfg)
	summary.EncodedBytes = len(snapshot)
	printFrameworkConfigSummary("Export snapshot", summary)

	if err := writeFrameworkConfigBytes(destPath, snapshot); err != nil {
		return err
	}

	fmt.Printf("Wrote %d bytes to %s (format %d)\n", len(snapshot), destPath, formatUsed)
	return nil
}

func importVMFrameworkConfig(sourcePath string) error {
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return fmt.Errorf("read framework config: %w", err)
	}

	format, payload, err := unmarshalFrameworkConfigSnapshot(data)
	if err != nil {
		return fmt.Errorf("parse framework config: %w", err)
	}

	if err := writeFrameworkConfigBytes(filepath.Join(vmDir, vmFrameworkConfigFileName), data); err != nil {
		return err
	}

	fmt.Printf("Imported snapshot format: %d\n", format)
	fmt.Printf("Imported snapshot payload bytes: %d\n", len(payload))
	fmt.Printf("Stored raw snapshot at %s\n", filepath.Join(vmDir, vmFrameworkConfigFileName))
	return nil
}

func currentVMFrameworkDiskPath() (string, error) {
	path := diskPath
	if path == "" {
		if vmDir == "" {
			return "", fmt.Errorf("vm directory not set")
		}
		switch frameworkConfigVMType() {
		case "Linux":
			path = filepath.Join(vmDir, "linux-disk.img")
		case "Windows":
			path = filepath.Join(vmDir, "windows-disk.img")
		default:
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
	switch frameworkConfigVMType() {
	case "Windows":
		return buildWindowsVMConfiguration(diskImagePath)
	case "Linux":
		return buildLinuxVMConfiguration(diskImagePath)
	case "macOS":
		return buildVMConfiguration(diskImagePath)
	default:
		return vz.VZVirtualMachineConfiguration{}, fmt.Errorf("cannot determine vm type for %s", vmDir)
	}
}

func ensureFrameworkConfigInputs() error {
	switch frameworkConfigVMType() {
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
		switch {
		case kernelPath != "":
			if err := ensureReadableFile(resolvePath(kernelPath)); err != nil {
				return err
			}
			if initrdPath != "" {
				if err := ensureReadableFile(resolvePath(initrdPath)); err != nil {
					return err
				}
			}
		case hasInstalledLinuxBootArtifacts(vmDir):
			for _, name := range []string{"vmlinuz", "initrd", linuxRootUUIDFileName} {
				if err := ensureReadableFile(filepath.Join(vmDir, name)); err != nil {
					return err
				}
			}
		default:
			if err := ensureReadableFile(filepath.Join(vmDir, "efi.nvram")); err != nil {
				return err
			}
		}
	case "Windows":
		for _, name := range []string{"windows-disk.img", "windows-machine.id", "efi.nvram"} {
			if err := ensureReadableFile(filepath.Join(vmDir, name)); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("cannot determine vm type for %s", vmDir)
	}
	return nil
}

func frameworkConfigVMType() string {
	if linuxMode {
		return "Linux"
	}
	if windowsMode {
		return "Windows"
	}
	return vmconfig.DetectOSType(vmDir)
}

func marshalFrameworkConfigSnapshot(format configcodec.Format, payload []byte) []byte {
	header := []byte(fmt.Sprintf("%s%d\n", frameworkConfigFormatPrefix, format))
	out := make([]byte, 0, len(header)+len(payload))
	out = append(out, header...)
	out = append(out, payload...)
	return out
}

func unmarshalFrameworkConfigSnapshot(data []byte) (configcodec.Format, []byte, error) {
	line, rest, found := bytes.Cut(data, []byte{'\n'})
	if !found || !bytes.HasPrefix(line, []byte(frameworkConfigFormatPrefix)) {
		return configcodec.DefaultFormat, data, nil
	}
	rawFormat := strings.TrimSpace(strings.TrimPrefix(string(line), frameworkConfigFormatPrefix))
	value, err := strconv.ParseUint(rawFormat, 10, 64)
	if err != nil {
		return configcodec.DefaultFormat, nil, fmt.Errorf("invalid format %q", rawFormat)
	}
	return configcodec.Format(value), rest, nil
}

func encodeFrameworkConfig(cfg vz.VZVirtualMachineConfiguration) ([]byte, configcodec.Format, error) {
	formats := []configcodec.Format{
		configcodec.DefaultFormat,
		100,
		200,
	}
	var errs []string
	for _, format := range formats {
		encoded, err := configcodec.Encode(cfg, format)
		if err == nil && len(encoded) > 0 {
			return encoded, format, nil
		}
		if err != nil {
			errs = append(errs, fmt.Sprintf("format %d: %v", format, err))
			continue
		}
		errs = append(errs, fmt.Sprintf("format %d: empty data", format))
	}
	return nil, configcodec.DefaultFormat, fmt.Errorf("%s", strings.Join(errs, "; "))
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
	fmt.Printf("  memory: %s\n", bytefmt.Size(int64(summary.MemoryBytes)))
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
