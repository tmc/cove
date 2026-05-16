package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tmc/apple/foundation"
	vz "github.com/tmc/apple/virtualization"
	"golang.org/x/sys/unix"
)

type blockDeviceSpec struct {
	Path     string
	ReadOnly bool
	Sync     string
}

type blockDeviceSlice []blockDeviceSpec

func (s *blockDeviceSlice) String() string {
	if s == nil || len(*s) == 0 {
		return ""
	}
	parts := make([]string, 0, len(*s))
	for _, spec := range *s {
		mode := "rw"
		if spec.ReadOnly {
			mode = "ro"
		}
		part := spec.Path + ":" + mode
		if spec.Sync != "" {
			part += ":sync=" + spec.Sync
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, ", ")
}

func (s *blockDeviceSlice) Set(value string) error {
	spec, err := parseBlockDeviceSpec(value)
	if err != nil {
		return err
	}
	*s = append(*s, spec)
	return nil
}

func parseBlockDeviceSpec(value string) (blockDeviceSpec, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return blockDeviceSpec{}, fmt.Errorf("empty block device spec")
	}

	parts := strings.Split(value, ":")
	if len(parts) < 2 {
		return blockDeviceSpec{}, fmt.Errorf("block device %q missing mode :ro or :rw", value)
	}
	path := strings.TrimSpace(parts[0])
	if path == "" {
		return blockDeviceSpec{}, fmt.Errorf("block device path is required")
	}
	if !filepath.IsAbs(path) {
		return blockDeviceSpec{}, fmt.Errorf("block device %s must be absolute", path)
	}

	mode := strings.ToLower(strings.TrimSpace(parts[1]))
	spec := blockDeviceSpec{Path: path}
	switch mode {
	case "ro", "readonly":
		spec.ReadOnly = true
	case "rw":
	default:
		return blockDeviceSpec{}, fmt.Errorf("block device %s mode must be ro or rw", path)
	}

	for _, opt := range parts[2:] {
		opt = strings.ToLower(strings.TrimSpace(opt))
		switch opt {
		case "":
			return blockDeviceSpec{}, fmt.Errorf("block device %s has empty option", path)
		case "sync=full":
			spec.Sync = "full"
		case "sync=none":
			if spec.ReadOnly {
				return blockDeviceSpec{}, fmt.Errorf("block device %s sync=none requires rw", path)
			}
			spec.Sync = "none"
		default:
			return blockDeviceSpec{}, fmt.Errorf("block device %s has unsupported option %q", path, opt)
		}
	}
	return spec, nil
}

func blockDeviceSyncMode(spec blockDeviceSpec) (vz.VZDiskSynchronizationMode, error) {
	switch spec.Sync {
	case "", "full":
		return vz.VZDiskSynchronizationModeFull, nil
	case "none":
		if spec.ReadOnly {
			return 0, fmt.Errorf("block device %s sync=none requires rw", spec.Path)
		}
		return vz.VZDiskSynchronizationModeNone, nil
	default:
		return 0, fmt.Errorf("block device %s has unsupported sync mode %q", spec.Path, spec.Sync)
	}
}

func openBlockDeviceViaHelper(spec blockDeviceSpec) (foundation.NSFileHandle, error) {
	if !helperInstalled() {
		return foundation.NSFileHandle{}, fmt.Errorf("block devices require an up-to-date cove-helper; run: sudo cove helper install")
	}
	fresh, _, err := helperBinaryFreshness()
	if err != nil {
		return foundation.NSFileHandle{}, fmt.Errorf("check cove-helper freshness: %w", err)
	}
	if !fresh {
		return foundation.NSFileHandle{}, fmt.Errorf("block devices require an up-to-date cove-helper; run: sudo cove helper install")
	}

	conn, err := dialHelper()
	if err != nil {
		return foundation.NSFileHandle{}, fmt.Errorf("connect cove-helper: %w", err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return foundation.NSFileHandle{}, fmt.Errorf("set helper deadline: %w", err)
	}

	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return foundation.NSFileHandle{}, fmt.Errorf("helper connection is not unix")
	}
	req := helperRequest{
		Op: "open_block_device",
		OpenBlockDevice: &blockDeviceRequest{
			Path:     spec.Path,
			ReadOnly: spec.ReadOnly,
		},
	}
	if err := json.NewEncoder(uc).Encode(req); err != nil {
		return foundation.NSFileHandle{}, fmt.Errorf("send block device request: %w", err)
	}

	buf := make([]byte, 256)
	oob := make([]byte, unix.CmsgSpace(4))
	n, oobn, _, _, err := uc.ReadMsgUnix(buf, oob)
	if err != nil {
		return foundation.NSFileHandle{}, fmt.Errorf("read block device response: %w", err)
	}
	status := strings.TrimSpace(string(buf[:n]))
	if status != "ok" {
		var resp helperResponse
		if err := json.Unmarshal(buf[:n], &resp); err == nil && resp.Error != "" {
			return foundation.NSFileHandle{}, fmt.Errorf("helper: %s", resp.Error)
		}
		return foundation.NSFileHandle{}, fmt.Errorf("helper: %s", status)
	}

	msgs, err := unix.ParseSocketControlMessage(oob[:oobn])
	if err != nil {
		return foundation.NSFileHandle{}, fmt.Errorf("parse helper fd control message: %w", err)
	}
	for _, msg := range msgs {
		fds, err := unix.ParseUnixRights(&msg)
		if err != nil {
			continue
		}
		if len(fds) == 0 {
			continue
		}
		handle := foundation.NewFileHandleWithFileDescriptorCloseOnDealloc(fds[0], true)
		if handle.ID == 0 {
			_ = unix.Close(fds[0])
			return foundation.NSFileHandle{}, fmt.Errorf("create file handle for block device")
		}
		handle.Retain()
		return handle, nil
	}
	return foundation.NSFileHandle{}, fmt.Errorf("helper did not return a block device fd")
}

func createBlockStorageDevice(spec blockDeviceSpec) (vz.VZVirtioBlockDeviceConfiguration, error) {
	handle, err := openBlockDeviceViaHelper(spec)
	if err != nil {
		return vz.VZVirtioBlockDeviceConfiguration{}, err
	}
	sync, err := blockDeviceSyncMode(spec)
	if err != nil {
		return vz.VZVirtioBlockDeviceConfiguration{}, err
	}
	attachment, err := vz.NewDiskBlockDeviceStorageDeviceAttachmentWithFileHandleReadOnlySynchronizationModeError(handle, spec.ReadOnly, sync)
	if err != nil {
		return vz.VZVirtioBlockDeviceConfiguration{}, fmt.Errorf("create block device attachment: %w", err)
	}
	attachment.Retain()
	device := vz.NewVirtioBlockDeviceConfigurationWithAttachment(&attachment.VZStorageDeviceAttachment)
	if device.ID == 0 {
		return vz.VZVirtioBlockDeviceConfiguration{}, fmt.Errorf("create block storage device")
	}
	device.Retain()
	return device, nil
}

func addBlockDevicesToConfig(config vz.VZVirtualMachineConfiguration, specs []blockDeviceSpec) error {
	if len(specs) == 0 {
		return nil
	}
	storage := config.StorageDevices()
	devices := make([]vz.VZStorageDeviceConfiguration, 0, len(storage)+len(specs))
	for _, dev := range storage {
		devices = append(devices, vz.VZStorageDeviceConfigurationFromID(dev.GetID()))
	}
	for i, spec := range specs {
		device, err := createBlockStorageDevice(spec)
		if err != nil {
			return fmt.Errorf("block device %d: %w", i, err)
		}
		devices = append(devices, vz.VZStorageDeviceConfigurationFromID(device.ID))
		mode := "rw"
		if spec.ReadOnly {
			mode = "ro"
		}
		fmt.Printf("  Block device: %s (%s)\n", spec.Path, mode)
	}
	config.SetStorageDevices(devices)
	return nil
}

func blockDeviceOpenFlags(readOnly bool) int {
	if readOnly {
		return os.O_RDONLY
	}
	return os.O_RDWR
}
