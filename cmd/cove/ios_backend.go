package main

import (
	"fmt"
	"github.com/tmc/apple/objectivec"
	"path/filepath"
	"strings"

	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
	pvz "github.com/tmc/apple/private/virtualization"
	vz "github.com/tmc/apple/virtualization"
	configx "github.com/tmc/apple/x/vzkit/config"
	displayx "github.com/tmc/apple/x/vzkit/display"
	identityx "github.com/tmc/apple/x/vzkit/identity"
	"github.com/tmc/apple/x/vzkit/macosconfig"
	networkx "github.com/tmc/apple/x/vzkit/network"
	"github.com/tmc/cove/internal/iosbundle"
	"github.com/tmc/cove/internal/vmrun"
)

func buildIOSVMConfiguration(rc vmrun.RunConfig, hc vmrun.HostConfig, cfg *iosbundle.Config) (vz.VZVirtualMachineConfiguration, error) {
	var zero vz.VZVirtualMachineConfiguration
	if cfg == nil {
		return zero, fmt.Errorf("missing ios configuration")
	}
	if err := cfg.Validate(); err != nil {
		return zero, err
	}
	if cfg.BootArgs != "" {
		return zero, fmt.Errorf("ios boot arguments require prepared nvram; runtime nvram updates are not supported")
	}
	if cfg.ROM == "" {
		return zero, fmt.Errorf("ios requires a prepared boot rom")
	}
	if rc.SerialOutput != "" && rc.SerialOutput != "none" && rc.SerialOutput != "stdout" {
		return zero, fmt.Errorf("ios serial output supports stdout or none")
	}
	model, err := identityx.LoadMacHardwareModel(filepath.Join(hc.VMDir, "hw.model"))
	if err != nil {
		return zero, err
	}
	if err := validateIOSHardwareModel(model); err != nil {
		return zero, err
	}
	machine, err := identityx.LoadMacMachineIdentifier(filepath.Join(hc.VMDir, "machine.id"))
	if err != nil {
		return zero, err
	}
	identity := pvz.VZMacMachineIdentifierFromID(machine.ID)
	if !identity.CanECID() {
		return zero, fmt.Errorf("ios machine identifier ecid accessor unavailable")
	}
	if err := requireIOSMethodEncoding(machine.ID, "_ECID", "Q@:"); err != nil {
		return zero, err
	}
	if _, err := identity.ECID(); err != nil {
		return zero, fmt.Errorf("read ios ecid: %w", err)
	}
	aux, err := identityx.LoadMacAuxiliaryStorage(filepath.Join(hc.VMDir, "aux.img"))
	if err != nil {
		return zero, err
	}
	network, err := networkx.Parse(cfg.Network)
	if err != nil {
		return zero, err
	}
	var mac *vz.VZMACAddress
	if cfg.MAC != "" {
		address := vz.NewMACAddressWithString(cfg.MAC)
		if address.ID == 0 {
			return zero, fmt.Errorf("invalid ios mac address")
		}
		mac = &address
	}
	config, err := macosconfig.Build(macosconfig.Config{
		CPUCount: rc.CPUCount, MemoryGB: rc.MemoryGB,
		Display: []displayx.Config{{Width: cfg.Display.Width, Height: cfg.Display.Height, PPI: cfg.Display.PPI}},
		Network: macosconfig.Network{Config: network, MAC: mac},
		Entropy: true, Socket: !cfg.NoVphoned,
	})
	if err != nil {
		return zero, err
	}
	platform := vz.NewVZMacPlatformConfiguration()
	if platform.ID == 0 {
		return zero, fmt.Errorf("ios mac platform unavailable")
	}
	platform.SetHardwareModel(&model)
	platform.SetMachineIdentifier(&machine)
	platform.SetAuxiliaryStorage(&aux)
	config.SetPlatform(&platform.VZPlatformConfiguration)
	boot := vz.NewVZMacOSBootLoader()
	if boot.ID == 0 {
		return zero, fmt.Errorf("ios mac boot loader unavailable")
	}
	privateBoot := pvz.VZMacOSBootLoaderFromID(boot.ID)
	if err := privateBoot.SetROMURL(foundation.NewURLFileURLWithPath(filepath.Join(hc.VMDir, cfg.ROM))); err != nil {
		return zero, fmt.Errorf("set ios boot rom: %w", err)
	}
	config.SetBootLoader(&boot.VZBootLoader)
	sepClass := pvz.GetVZSEPCoprocessorConfigurationClass()
	if sepClass.Class() == 0 {
		return zero, fmt.Errorf("ios sep coprocessor unavailable")
	}
	sep := sepClass.Alloc()
	if !objc.RespondsToSelector(sep.ID, objc.Sel("initWithStorageURL:")) {
		sep.Release()
		return zero, fmt.Errorf("ios sep storage initializer unavailable")
	}
	sep = sep.InitWithStorageURL(foundation.NewURLFileURLWithPath(filepath.Join(hc.VMDir, "sep.img")))
	if sep.ID == 0 {
		return zero, fmt.Errorf("open ios sep storage")
	}
	defer sep.Release()
	if cfg.SEPROM != "" {
		if !objc.RespondsToSelector(sep.ID, objc.Sel("setRomBinaryURL:")) {
			return zero, fmt.Errorf("ios sep rom setter unavailable")
		}
		sep.SetRomBinaryURL(foundation.NewURLFileURLWithPath(filepath.Join(hc.VMDir, cfg.SEPROM)))
	}
	coprocessors := foundation.NewArrayWithObject(sep)
	if coprocessors.ID == 0 {
		return zero, fmt.Errorf("create ios coprocessor array")
	}
	coprocessors.Retain()
	defer coprocessors.Release()
	if err := pvz.VZVirtualMachineConfigurationFromID(config.ID).SetCoprocessors(coprocessors); err != nil {
		return zero, fmt.Errorf("set ios coprocessors: %w", err)
	}
	attachment, err := vz.NewDiskImageStorageDeviceAttachmentWithURLReadOnlyError(foundation.NewURLFileURLWithPath(filepath.Join(hc.VMDir, "disk.img")), false)
	if err != nil {
		return zero, fmt.Errorf("open ios disk: %w", err)
	}
	disk := vz.NewVirtioBlockDeviceConfigurationWithAttachment(&attachment.VZStorageDeviceAttachment)
	if disk.ID == 0 {
		return zero, fmt.Errorf("create ios disk device")
	}
	configx.SetStorageDevices(config, disk)
	keyboard := vz.NewVZUSBKeyboardConfiguration()
	if keyboard.ID == 0 {
		return zero, fmt.Errorf("create ios keyboard")
	}
	configx.SetKeyboards(config, keyboard)
	if rc.SerialOutput == "stdout" {
		if pvz.GetVZPL011SerialPortConfigurationClass().Class() == 0 {
			return zero, fmt.Errorf("ios pl011 serial device unavailable")
		}
		serial := pvz.NewVZPL011SerialPortConfiguration()
		if serial.ID == 0 {
			return zero, fmt.Errorf("create ios serial device")
		}
		handles := foundation.GetFileHandleClass()
		attachment := vz.NewFileHandleSerialPortAttachmentWithFileHandleForReadingFileHandleForWriting(handles.FileHandleWithNullDevice(), handles.FileHandleWithStandardOutput())
		if attachment.ID == 0 {
			return zero, fmt.Errorf("create ios serial attachment")
		}
		publicSerial := vz.VZSerialPortConfigurationFromID(serial.ID)
		publicSerial.SetAttachment(&attachment.VZSerialPortAttachment)
		configx.SetSerialPorts(config, publicSerial)
	}
	valid, err := config.ValidateWithError()
	if err != nil {
		return zero, fmt.Errorf("validate ios configuration: %w", err)
	}
	if !valid {
		return zero, fmt.Errorf("ios configuration validation failed")
	}

	return config, nil
}

func validateIOSHardwareModel(model vz.VZMacHardwareModel) error {
	class := pvz.GetVZMacHardwareModelDescriptorClass()
	if class.Class() == 0 {
		return fmt.Errorf("ios research hardware descriptor unavailable")
	}
	descriptor := pvz.NewVZMacHardwareModelDescriptor()
	if descriptor.ID == 0 {
		return fmt.Errorf("create ios research hardware descriptor")
	}
	defer descriptor.Release()
	for _, method := range []struct{ selector, encoding string }{
		{"setPlatformVersion:", "v@:I"}, {"setBoardID:", "v@:I"}, {"setISA:", "v@:q"},
	} {
		if err := requireIOSMethodEncoding(descriptor.ID, method.selector, method.encoding); err != nil {
			return err
		}
	}
	descriptor.SetPlatformVersion(3)
	descriptor.SetBoardID(0x90)
	descriptor.SetISA(2)
	object, err := pvz.GetVZMacHardwareModelClass().HardwareModelWithDescriptor(descriptor)
	if err != nil {
		return fmt.Errorf("create ios research hardware model: %w", err)
	}
	if object == nil || object.GetID() == 0 {
		return fmt.Errorf("ios research hardware model unavailable")
	}
	expected := vz.VZMacHardwareModelFromID(object.GetID())
	if !expected.IsSupported() {
		return fmt.Errorf("ios research hardware model unsupported on this host")
	}
	expectedData := expected.DataRepresentation()
	savedData := model.DataRepresentation()
	if expectedData.ID == 0 || savedData.ID == 0 || !savedData.IsEqualToData(expectedData) {
		return fmt.Errorf("saved hardware model does not match ios vresearch101 profile")
	}
	return nil
}

func requireIOSMethodEncoding(id objc.ID, selector, want string) error {
	if id == 0 || !objc.RespondsToSelector(id, objc.Sel(selector)) {
		return fmt.Errorf("ios selector %s unavailable", selector)
	}
	class := objectivec.Object_getClass(objectivec.Object{ID: id})
	method := objectivec.Class_getInstanceMethod(class, objectivec.SEL(objc.Sel(selector)))
	if method == 0 {
		return fmt.Errorf("ios selector %s has no inspectable method", selector)
	}
	encoding := objc.GoString(objectivec.Method_getTypeEncoding(method))
	signature := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return -1
		}
		return r
	}, encoding)
	if signature != want {
		return fmt.Errorf("ios selector %s has unsupported encoding %q", selector, encoding)
	}
	return nil
}
