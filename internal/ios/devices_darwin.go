//go:build darwin

package ios

import (
	"fmt"

	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/objectivec"
	privatevz "github.com/tmc/apple/private/virtualization"
	vz "github.com/tmc/apple/virtualization"
)

type objectScope []objc.ID

func (s *objectScope) add(name string, object objectivec.IObject) error {
	if object == nil || object.GetID() == 0 {
		return fmt.Errorf("create ios %s: nil object", name)
	}
	*s = append(*s, object.GetID())
	return nil
}

func (s objectScope) release() {
	for i := len(s) - 1; i >= 0; i-- {
		objectivec.Object{ID: s[i]}.Release()
	}
}

func setDeviceArray(set func(objectivec.IObject) error, devices ...objectivec.IObject) error {
	array := foundation.NewNSMutableArray()
	if array.ID == 0 {
		return fmt.Errorf("create ios device array: nil object")
	}
	defer array.Release()
	for _, device := range devices {
		if device == nil || device.GetID() == 0 {
			return fmt.Errorf("nil ios device in array")
		}
		array.AddObject(device)
	}
	return set(array)
}

// configureResearchDevices requires the runtime's main thread and autorelease pool.
// The configuration retains or copies the assigned device arrays.
func configureResearchDevices(config vz.VZVirtualMachineConfiguration, sepPath, sepROM string, debugPort uint16) error {
	if debugPort != 0 && debugPort < 6000 {
		return fmt.Errorf("ios debug port must be zero or between 6000 and 65535")
	}
	if err := probeResearchDevices(); err != nil {
		return err
	}
	var scope objectScope
	defer func() { scope.release() }()
	video := privatevz.NewVZMacVideoToolboxDeviceConfiguration()
	if err := scope.add("video accelerator", video); err != nil {
		return err
	}
	neural := privatevz.NewVZMacNeuralEngineDeviceConfiguration()
	if err := scope.add("neural accelerator", neural); err != nil {
		return err
	}
	scaler := privatevz.NewVZMacScalerAcceleratorDeviceConfiguration()
	if err := scope.add("scaler accelerator", scaler); err != nil {
		return err
	}
	private := privatevz.VZVirtualMachineConfigurationFromID(config.ID)
	if err := setDeviceArray(private.SetAcceleratorDevices, video, neural, scaler); err != nil {
		return fmt.Errorf("set ios accelerators: %w", err)
	}
	touch := privatevz.NewVZUSBTouchScreenConfiguration()
	if err := scope.add("touch screen", touch); err != nil {
		return err
	}
	if err := setDeviceArray(private.SetMultiTouchDevices, touch); err != nil {
		return fmt.Errorf("set ios touch screen: %w", err)
	}
	battery := privatevz.NewVZMacSyntheticBatterySource()
	if err := scope.add("battery source", battery); err != nil {
		return err
	}
	battery.SetCharge(100)
	battery.SetConnectivity(1)
	power := privatevz.NewVZMacBatteryPowerSourceDeviceConfiguration()
	if err := scope.add("battery device", power); err != nil {
		return err
	}
	power.SetSource(battery)
	if err := setDeviceArray(private.SetPowerSourceDevices, power); err != nil {
		return fmt.Errorf("set ios battery: %w", err)
	}
	var apDebug privatevz.VZGDBDebugStubConfiguration
	if debugPort == 0 {
		apDebug = privatevz.NewVZGDBDebugStubConfiguration()
	} else {
		apDebug = privatevz.NewVZGDBDebugStubConfigurationWithPort(debugPort)
	}
	if err := scope.add("ap debug stub", apDebug); err != nil {
		return err
	}
	apDebug.SetListensOnAllNetworkInterfaces(false)
	if err := private.SetDebugStub(apDebug); err != nil {
		return fmt.Errorf("set ios ap debug stub: %w", err)
	}
	sepURL := foundation.NewURLFileURLWithPath(sepPath)
	if err := scope.add("sep storage url", sepURL); err != nil {
		return err
	}
	sep := privatevz.NewVZSEPCoprocessorConfigurationWithStorageURL(sepURL)
	if err := scope.add("sep coprocessor", sep); err != nil {
		return err
	}
	if sepROM != "" {
		url := foundation.NewURLFileURLWithPath(sepROM)
		if err := scope.add("sep rom url", url); err != nil {
			return err
		}
		sep.SetRomBinaryURL(url)
	}
	sepDebug := privatevz.NewVZGDBDebugStubConfiguration()
	if err := scope.add("sep debug stub", sepDebug); err != nil {
		return err
	}
	sepDebug.SetListensOnAllNetworkInterfaces(false)
	sep.SetDebugStub(sepDebug)
	if err := setDeviceArray(private.SetCoprocessors, sep); err != nil {
		return fmt.Errorf("set ios sep coprocessor: %w", err)
	}
	return nil
}

func probeResearchDevices() error {
	for _, name := range []string{
		"_VZMacVideoToolboxDeviceConfiguration", "_VZMacNeuralEngineDeviceConfiguration",
		"_VZMacScalerAcceleratorDeviceConfiguration", "_VZUSBTouchScreenConfiguration",
		"_VZMacSyntheticBatterySource", "_VZMacBatteryPowerSourceDeviceConfiguration",
		"_VZSEPCoprocessorConfiguration", "_VZGDBDebugStubConfiguration",
	} {
		if objc.GetClass(name) == 0 {
			return fmt.Errorf("ios device class unavailable: %s", name)
		}
	}

	for _, method := range []struct {
		class, selector, result string
		args                    []string
	}{
		{"_VZMacSyntheticBatterySource", "setCharge:", "v", []string{"@", ":", "d"}},
		{"_VZMacSyntheticBatterySource", "setConnectivity:", "v", []string{"@", ":", "q"}},
		{"_VZMacBatteryPowerSourceDeviceConfiguration", "setSource:", "v", []string{"@", ":", "@"}},
		{"_VZGDBDebugStubConfiguration", "setListensOnAllNetworkInterfaces:", "v", []string{"@", ":", "B"}},
		{"_VZGDBDebugStubConfiguration", "initWithPort:", "@", []string{"@", ":", "S"}},
		{"_VZSEPCoprocessorConfiguration", "initWithStorageURL:", "@", []string{"@", ":", "@"}},
		{"_VZSEPCoprocessorConfiguration", "setRomBinaryURL:", "v", []string{"@", ":", "@"}},
		{"_VZSEPCoprocessorConfiguration", "setDebugStub:", "v", []string{"@", ":", "@"}},
	} {
		m := objectivec.Class_getInstanceMethod(objc.GetClass(method.class), objectivec.SEL(objc.Sel(method.selector)))
		if err := checkMethod(m, method.result, method.args); err != nil {
			return fmt.Errorf("ios %s %s: %w", method.class, method.selector, err)
		}
	}
	return nil
}
