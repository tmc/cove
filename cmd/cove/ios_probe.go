package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/objectivec"
	pvz "github.com/tmc/apple/private/virtualization"
	vz "github.com/tmc/apple/virtualization"
)

type iosHostProbe struct {
	SchemaVersion     int                `json:"schemaVersion"`
	Profile           string             `json:"profile"`
	Selectors         []iosSelectorProbe `json:"selectors"`
	ModelSupported    *bool              `json:"modelSupported"`
	ConstructionError string             `json:"constructionError,omitempty"`
	Error             string             `json:"error,omitempty"`
}

type iosSelectorProbe struct {
	Class            string `json:"class"`
	Selector         string `json:"selector"`
	ClassMethod      bool   `json:"classMethod,omitempty"`
	Available        bool   `json:"available"`
	Encoding         string `json:"encoding,omitempty"`
	ExpectedEncoding string `json:"expectedEncoding"`
}

func probeIOSHost(w io.Writer) error {
	report := iosHostProbe{SchemaVersion: 1, Profile: "vresearch101"}
	err := inspectIOSHost(&report)
	if err != nil {
		report.Error = err.Error()
	}
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if encodeErr := encoder.Encode(report); encodeErr != nil {
		return fmt.Errorf("write ios host probe: %w", encodeErr)
	}
	return err
}

func inspectIOSHost(report *iosHostProbe) error {
	report.Selectors = []iosSelectorProbe{
		{Class: "_VZMacHardwareModelDescriptor", Selector: "setPlatformVersion:", ExpectedEncoding: "v@:I"},
		{Class: "_VZMacHardwareModelDescriptor", Selector: "setBoardID:", ExpectedEncoding: "v@:I"},
		{Class: "_VZMacHardwareModelDescriptor", Selector: "setISA:", ExpectedEncoding: "v@:q"},
		{Class: "VZMacHardwareModel", Selector: "_hardwareModelWithDescriptor:", ClassMethod: true, ExpectedEncoding: "@@:@"},
		{Class: "VZMacMachineIdentifier", Selector: "_ECID", ExpectedEncoding: "Q@:"},
		{Class: "VZMacOSBootLoader", Selector: "_setROMURL:", ExpectedEncoding: "v@:@"},
		{Class: "_VZSEPCoprocessorConfiguration", Selector: "initWithStorageURL:", ExpectedEncoding: "@@:@"},
		{Class: "_VZSEPCoprocessorConfiguration", Selector: "setRomBinaryURL:", ExpectedEncoding: "v@:@"},
		{Class: "VZVirtualMachineConfiguration", Selector: "_setCoprocessors:", ExpectedEncoding: "v@:@"},
		{Class: "_VZPL011SerialPortConfiguration", Selector: "init", ExpectedEncoding: "@@:"},
	}
	var availabilityErr error
	for i := range report.Selectors {
		probe := &report.Selectors[i]
		class := objc.GetClass(probe.Class)
		if class != 0 {
			selector := objectivec.SEL(objc.Sel(probe.Selector))
			method := objectivec.Class_getInstanceMethod(class, selector)
			if probe.ClassMethod {
				method = objectivec.Class_getClassMethod(class, selector)
			}
			if method != 0 {
				probe.Available = true
				probe.Encoding = objc.GoString(objectivec.Method_getTypeEncoding(method))
			}
		}
		if err := validateIOSProbeSelector(*probe); err != nil && availabilityErr == nil {
			availabilityErr = err
		}
	}
	if availabilityErr != nil {
		return availabilityErr
	}
	descriptor := pvz.NewVZMacHardwareModelDescriptor()
	if descriptor.ID == 0 {
		report.ConstructionError = "create ios research hardware descriptor"
		return fmt.Errorf("%s", report.ConstructionError)
	}
	defer descriptor.Release()
	descriptor.SetPlatformVersion(3)
	descriptor.SetBoardID(0x90)
	descriptor.SetISA(2)
	object, err := pvz.GetVZMacHardwareModelClass().HardwareModelWithDescriptor(descriptor)
	if err != nil {
		report.ConstructionError = err.Error()
		return fmt.Errorf("create ios research hardware model: %w", err)
	}
	if object == nil || object.GetID() == 0 {
		report.ConstructionError = "ios research hardware model constructor returned nil"
		return fmt.Errorf("%s", report.ConstructionError)
	}
	model := vz.VZMacHardwareModelFromID(object.GetID())
	supported := model.IsSupported()
	report.ModelSupported = &supported
	if !supported {
		return fmt.Errorf("ios research hardware model unsupported on this host")
	}
	return nil
}

func validateIOSProbeSelector(probe iosSelectorProbe) error {
	if !probe.Available {
		return fmt.Errorf("ios selector %s.%s unavailable", probe.Class, probe.Selector)
	}
	signature := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return -1
		}
		return r
	}, probe.Encoding)
	if signature != probe.ExpectedEncoding {
		return fmt.Errorf("ios selector %s.%s has unsupported encoding %q", probe.Class, probe.Selector, probe.Encoding)
	}
	return nil
}
