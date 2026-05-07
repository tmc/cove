package vmrun

import (
	"errors"
	"fmt"
	"strings"
)

// DevicePlan is the derived, pre-AppKit description of the devices the VM
// should expose. Fields are populated only when the corresponding device is
// expected; package main reads the plan and creates the matching VZ objects.
type DevicePlan struct {
	OS GuestOS

	CPUCount uint
	MemoryGB uint64

	Storage []StoragePlan
	Network NetworkPlan
	Display []DisplaySpec
	Volumes []VolumeMount
	USB     []USBPlan

	Audio       AudioPlan
	Rosetta     bool
	Clipboard   bool
	SerialDest  string // "stdout", "none", or absolute file path
	HTTPListen  string
	StartupFwd  []PortForward
	BootCmdFile string
}

// StoragePlan describes one storage attachment without committing to a
// concrete VZ device class. Kind is the small enum below; package main maps
// it onto VZVirtioBlockDevice, VZUSBMassStorageDevice, or VZNVMExpressController.
type StoragePlan struct {
	Kind     StorageKind
	Path     string
	ReadOnly bool
	Cache    string
}

// USBPlan describes a USB mass-storage attachment together with the
// identity slot package main fills in before constructing the VZ device.
// vmrun knows Path and ReadOnly from the user-supplied flag; the UUID
// is owned by package main (it comes from a saved per-VM identity file
// or is generated at attach time) and is recorded here so callers can
// pass DevicePlan to the device builder without re-walking the original
// USB flag slice.
type USBPlan struct {
	Path     string
	ReadOnly bool
	UUID     string
}

// StorageKind names the abstract device family.
type StorageKind int

const (
	StorageRoot StorageKind = iota // primary boot/root disk
	StorageISO                     // boot/install media (read-only)
	StorageUSB                     // user-attached USB mass storage
	StorageBlock                   // raw block device
)

// NetworkPlan describes the single primary NIC. An empty Mode means no NIC.
type NetworkPlan struct {
	Mode string // "nat", "bridged:<iface>", "vmnet", "filehandle", "none"
}

// AudioPlan describes the VirtioSound device, if any.
type AudioPlan struct {
	Enabled     bool
	HostInput   bool
	HostOutput  bool
	Description string // human-readable explanation, used in -verbose output
}

// Validate checks RunConfig for shape errors that do not require host state.
// It runs first inside Plan but is exported so callers can probe a config
// before any host work begins.
func (c *RunConfig) Validate() error {
	if c == nil {
		return errors.New("vmrun: nil RunConfig")
	}
	if c.OS == GuestUnknown {
		return errors.New("vmrun: guest OS not set")
	}
	if c.CPUCount == 0 {
		return errors.New("vmrun: cpu count must be at least 1")
	}
	if c.MemoryGB == 0 {
		return errors.New("vmrun: memory must be at least 1 GB")
	}
	if c.GUI && c.Headless {
		return errors.New("vmrun: -gui and -headless are mutually exclusive")
	}
	switch c.OS {
	case GuestLinux:
		if c.LinuxShell && c.Headless {
			return errors.New("vmrun: -shell requires a host terminal and is incompatible with -headless")
		}
	case GuestMacOS:
		if c.IPSWPath != "" && c.ISOPath != "" {
			return errors.New("vmrun: -ipsw and -iso cannot both be set for macOS")
		}
		if c.KernelPath != "" || c.InitrdPath != "" {
			return errors.New("vmrun: -kernel/-initrd are Linux-only")
		}
	case GuestWindows:
		if c.IPSWPath != "" {
			return errors.New("vmrun: -ipsw is macOS-only")
		}
	}
	for i, u := range c.USB {
		if u.Path == "" {
			return fmt.Errorf("vmrun: usb[%d]: empty path", i)
		}
	}
	for i, b := range c.BlockDevices {
		if b.Path == "" {
			return fmt.Errorf("vmrun: block[%d]: empty path", i)
		}
	}
	for i, v := range c.Volumes {
		if v.HostPath == "" {
			return fmt.Errorf("vmrun: volume[%d]: empty host path", i)
		}
	}
	return nil
}

// Plan derives a DevicePlan from rc and hc. It validates rc, then maps
// per-OS defaults and storage layout. Plan performs no I/O.
func Plan(rc RunConfig, hc HostConfig) (DevicePlan, error) {
	if err := rc.Validate(); err != nil {
		return DevicePlan{}, err
	}
	if hc.VMDir == "" {
		return DevicePlan{}, errors.New("vmrun: HostConfig.VMDir is required")
	}

	plan := DevicePlan{
		OS:          rc.OS,
		CPUCount:    rc.CPUCount,
		MemoryGB:    rc.MemoryGB,
		Display:     append([]DisplaySpec(nil), rc.Displays...),
		Volumes:     append([]VolumeMount(nil), rc.Volumes...),
		Rosetta:     rc.EnableRosetta && rc.OS == GuestLinux,
		Clipboard:   rc.EnableClipboard,
		SerialDest:  normalizeSerial(rc.SerialOutput),
		HTTPListen:  rc.HTTPListenAddr,
		StartupFwd:  append([]PortForward(nil), rc.StartupForwards...),
		BootCmdFile: rc.BootCommandsFile,
	}

	plan.Network = NetworkPlan{Mode: rc.NetworkMode}

	plan.Storage = append(plan.Storage, StoragePlan{
		Kind:     StorageRoot,
		Path:     rc.DiskPath,
		ReadOnly: false,
	})
	if rc.ISOPath != "" {
		plan.Storage = append(plan.Storage, StoragePlan{
			Kind:     StorageISO,
			Path:     rc.ISOPath,
			ReadOnly: true,
		})
	}
	for _, u := range rc.USB {
		plan.Storage = append(plan.Storage, StoragePlan{
			Kind:     StorageUSB,
			Path:     u.Path,
			ReadOnly: u.ReadOnly,
		})
		plan.USB = append(plan.USB, USBPlan{
			Path:     u.Path,
			ReadOnly: u.ReadOnly,
		})
	}
	for _, b := range rc.BlockDevices {
		plan.Storage = append(plan.Storage, StoragePlan{
			Kind:     StorageBlock,
			Path:     b.Path,
			ReadOnly: b.ReadOnly,
			Cache:    b.Cache,
		})
	}

	switch rc.OS {
	case GuestMacOS:
		// macOS installation requires both host audio streams; without them
		// the MobileRestore service fails with DFU 3004/4014. See CLAUDE.md.
		plan.Audio = AudioPlan{
			Enabled:     true,
			HostInput:   true,
			HostOutput:  true,
			Description: "VirtioSound with host source and sink",
		}
	case GuestLinux:
		plan.Audio = AudioPlan{
			Enabled:     true,
			HostInput:   false,
			HostOutput:  true,
			Description: "VirtioSound output only",
		}
	case GuestWindows:
		plan.Audio = AudioPlan{Enabled: false}
	}
	return plan, nil
}

// normalizeSerial maps the user-supplied -serial flag to one of three forms:
// "stdout", "none", or an absolute file path. Empty strings default to
// "stdout" because that matches the historical CLI default.
func normalizeSerial(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "stdout":
		return "stdout"
	case "none", "off", "disable":
		return "none"
	}
	return s
}
