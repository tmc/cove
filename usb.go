// usb.go - USB mass storage device support
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tmc/apple/foundation"
	vz "github.com/tmc/apple/virtualization"
)

// USBStorageConfig represents a USB storage device configuration
type USBStorageConfig struct {
	Path     string // Path to disk image file
	ReadOnly bool   // Mount as read-only
	UUID     string // Optional UUID for snapshot restore tracking
}

// ParseUSBStorageFlag parses a USB storage flag value
// Formats:
//   - "/path/to/disk.img" - Read-write
//   - "/path/to/disk.img:ro" or "/path/to/disk.img:readonly" - Read-only
//   - "/path/to/disk.img:rw" - Explicit read-write
func ParseUSBStorageFlag(s string) (USBStorageConfig, error) {
	if s == "" {
		return USBStorageConfig{}, fmt.Errorf("empty USB storage path")
	}

	config := USBStorageConfig{}

	// Check for read-only suffix
	if strings.HasSuffix(s, ":ro") || strings.HasSuffix(s, ":readonly") {
		config.ReadOnly = true
		s = strings.TrimSuffix(strings.TrimSuffix(s, ":ro"), ":readonly")
	} else if strings.HasSuffix(s, ":rw") {
		config.ReadOnly = false
		s = strings.TrimSuffix(s, ":rw")
	}

	// Resolve path
	path, err := filepath.Abs(s)
	if err != nil {
		return config, fmt.Errorf("resolve USB storage path: %w", err)
	}

	// Verify file exists
	if _, err := os.Stat(path); err != nil {
		return config, fmt.Errorf("usb storage file not found: %s", path)
	}

	config.Path = path
	return config, nil
}

// CreateUSBStorageDevice creates a VZUSBMassStorageDeviceConfiguration
func CreateUSBStorageDevice(config USBStorageConfig) (vz.VZUSBMassStorageDeviceConfiguration, error) {
	// Create file URL
	url := foundation.NewURLFileURLWithPath(config.Path)
	if url.ID == 0 {
		return vz.VZUSBMassStorageDeviceConfiguration{}, fmt.Errorf("failed to create URL for %s", config.Path)
	}
	url.Retain()

	// Create disk attachment
	attachment, err := vz.NewDiskImageStorageDeviceAttachmentWithURLReadOnlyError(url, config.ReadOnly)
	if err != nil {
		return vz.VZUSBMassStorageDeviceConfiguration{}, fmt.Errorf("create USB attachment: %w", err)
	}
	attachment.Retain()

	// Create USB mass storage configuration
	usbConfig := vz.NewUSBMassStorageDeviceConfigurationWithAttachment(&attachment.VZStorageDeviceAttachment)
	if usbConfig.ID == 0 {
		return usbConfig, fmt.Errorf("failed to create USB mass storage configuration")
	}

	// Set UUID if provided (for snapshot restore tracking)
	if config.UUID != "" {
		uuid := foundation.NewUUIDWithUUIDString(config.UUID)
		if uuid.ID != 0 {
			usbConfig.SetUuid(uuid)
		}
	}

	mode := "read-write"
	if config.ReadOnly {
		mode = "read-only"
	}
	fmt.Printf("  USB storage: %s (%s)\n", config.Path, mode)

	return usbConfig, nil
}

// AddUSBStorageToConfig adds USB storage devices to a VM configuration
func AddUSBStorageToConfig(config vz.VZVirtualMachineConfiguration, usbConfigs []USBStorageConfig) error {
	if len(usbConfigs) == 0 {
		return nil
	}

	// Get existing storage devices
	existingStorage := config.StorageDevices()

	// Create new list with existing + USB devices
	storageDevices := make([]vz.VZStorageDeviceConfiguration, 0, len(existingStorage)+len(usbConfigs))
	for _, dev := range existingStorage {
		storageDevices = append(storageDevices, vz.VZStorageDeviceConfigurationFromID(dev.GetID()))
	}

	// Add USB devices
	for i, usbConf := range usbConfigs {
		usbDevice, err := CreateUSBStorageDevice(usbConf)
		if err != nil {
			return fmt.Errorf("usb device %d: %w", i, err)
		}
		storageDevices = append(storageDevices, vz.VZStorageDeviceConfigurationFromID(usbDevice.ID))
	}

	config.SetStorageDevices(storageDevices)
	fmt.Printf("  Added %d USB storage device(s) to VM configuration\n", len(usbConfigs))
	return nil
}

// Use truncate to create a sparse file

// USBStorageSlice implements flag.Value for collecting multiple -usb flags
type USBStorageSlice []USBStorageConfig

func (s *USBStorageSlice) String() string {
	if s == nil || len(*s) == 0 {
		return ""
	}
	var parts []string
	for _, c := range *s {
		if c.ReadOnly {
			parts = append(parts, c.Path+":ro")
		} else {
			parts = append(parts, c.Path)
		}
	}
	return strings.Join(parts, ",")
}

func (s *USBStorageSlice) Set(value string) error {
	config, err := ParseUSBStorageFlag(value)
	if err != nil {
		return err
	}
	*s = append(*s, config)
	return nil
}

// Get USB controllers

// Get attached devices from the controller

// Check if it's a mass storage device

