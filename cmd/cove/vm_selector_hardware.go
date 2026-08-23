// vm_selector_hardware.go - CPU and memory editing for the VM selector.

package main

import (
	"fmt"
	"strconv"

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/objc"
	vz "github.com/tmc/apple/virtualization"

	"github.com/tmc/cove/internal/vmconfig"
)

// hardwareBounds describes the CPU and memory range a VM may be configured
// with on this host. The Virtualization framework derives its limits from the
// host hardware, so these are host limits, not arbitrary constants.
type hardwareBounds struct {
	MinCPU      uint
	MaxCPU      uint
	MinMemoryGB uint64
	MaxMemoryGB uint64
}

// fallbackHardwareBounds is used when the Virtualization framework reports
// unusable limits (for example when it is unavailable).
var fallbackHardwareBounds = hardwareBounds{MinCPU: 1, MaxCPU: 8, MinMemoryGB: 1, MaxMemoryGB: 8}

// hostHardwareBounds reports the CPU and memory range allowed on this host.
func hostHardwareBounds() hardwareBounds {
	configClass := vz.GetVZVirtualMachineConfigurationClass()
	b := hardwareBounds{
		MinCPU:      configClass.MinimumAllowedCPUCount(),
		MaxCPU:      configClass.MaximumAllowedCPUCount(),
		MinMemoryGB: configClass.MinimumAllowedMemorySize() / bytesPerGiB,
		MaxMemoryGB: configClass.MaximumAllowedMemorySize() / bytesPerGiB,
	}
	return normalizeHardwareBounds(b)
}

// normalizeHardwareBounds repairs nonsensical bounds so the selector controls
// always have a usable range.
func normalizeHardwareBounds(b hardwareBounds) hardwareBounds {
	if b.MinCPU == 0 {
		b.MinCPU = fallbackHardwareBounds.MinCPU
	}
	if b.MaxCPU < b.MinCPU {
		b.MaxCPU = fallbackHardwareBounds.MaxCPU
	}
	if b.MaxCPU < b.MinCPU {
		b.MaxCPU = b.MinCPU
	}
	if b.MinMemoryGB == 0 {
		b.MinMemoryGB = fallbackHardwareBounds.MinMemoryGB
	}
	if b.MaxMemoryGB < b.MinMemoryGB {
		b.MaxMemoryGB = fallbackHardwareBounds.MaxMemoryGB
	}
	if b.MaxMemoryGB < b.MinMemoryGB {
		b.MaxMemoryGB = b.MinMemoryGB
	}
	return b
}

// clampCPU returns cpu constrained to the host bounds, substituting fallback
// when cpu is zero (unset).
func (b hardwareBounds) clampCPU(cpu, fallback uint) uint {
	if cpu == 0 {
		cpu = fallback
	}
	if cpu < b.MinCPU {
		return b.MinCPU
	}
	if cpu > b.MaxCPU {
		return b.MaxCPU
	}
	return cpu
}

// clampMemoryGB returns memory constrained to the host bounds, substituting
// fallback when memory is zero (unset).
func (b hardwareBounds) clampMemoryGB(memory, fallback uint64) uint64 {
	if memory == 0 {
		memory = fallback
	}
	if memory < b.MinMemoryGB {
		return b.MinMemoryGB
	}
	if memory > b.MaxMemoryGB {
		return b.MaxMemoryGB
	}
	return memory
}

// hardwareHint describes the editable range for display under the controls.
func (b hardwareBounds) hint() string {
	return fmt.Sprintf("CPU %d-%d - Memory %d-%d GB", b.MinCPU, b.MaxCPU, b.MinMemoryGB, b.MaxMemoryGB)
}

// loadVMHardware reports the saved hardware for the VM directory, falling back
// to the process defaults when the config has no values.
func loadVMHardware(dir string, b hardwareBounds) vmconfig.Hardware {
	var cfg vmconfig.Config
	if loaded, err := vmconfig.Load(dir); err == nil && loaded != nil {
		cfg = *loaded
	}
	return vmconfig.Hardware{
		CPU:      b.clampCPU(cfg.CPU, cpuCount),
		MemoryGB: b.clampMemoryGB(cfg.MemoryGB, memoryGB),
	}
}

// buildHardwareControls adds editable CPU and memory fields to the details box.
// Each field is paired with a stepper; edits are clamped to the host bounds and
// written to the VM's config.json so the next launch uses them.
func (s *VMSelector) buildHardwareControls(box appkit.NSBox, width, boxHeight float64) {
	s.bounds = hostHardwareBounds()

	secondary := appkit.GetNSColorClass().SecondaryLabelColor()
	labelFont := appkit.GetNSFontClass().SystemFontOfSize(13)

	addRow := func(title string, y float64, action string) (appkit.NSTextField, appkit.NSStepper) {
		label := selectorLabel(
			title,
			corefoundation.CGRect{
				Origin: corefoundation.CGPoint{X: 18, Y: y + 3},
				Size:   corefoundation.CGSize{Width: 90, Height: 18},
			},
			labelFont,
			secondary,
		)
		objc.Send[objc.ID](label.ID, objc.Sel("setAutoresizingMask:"), uint(selectorViewMinY))
		objc.Send[objc.ID](box.ID, objc.Sel("addSubview:"), label.ID)

		field := appkit.NewTextFieldWithFrame(corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: width - 100, Y: y},
			Size:   corefoundation.CGSize{Width: 60, Height: 22},
		})
		field.SetEditable(true)
		field.SetSelectable(true)
		field.SetAlignment(appkit.NSTextAlignmentRight)
		field.SetAccessibilityLabel(title)
		objc.Send[objc.ID](field.ID, objc.Sel("setAutoresizingMask:"), uint(selectorViewMinX|selectorViewMinY))
		objc.Send[objc.ID](field.ID, objc.Sel("setTarget:"), s.delegateID)
		objc.Send[objc.ID](field.ID, objc.Sel("setAction:"), objc.RegisterName(action))
		objc.Send[objc.ID](box.ID, objc.Sel("addSubview:"), field.ID)

		stepper := appkit.NewStepperWithFrame(corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: width - 36, Y: y},
			Size:   corefoundation.CGSize{Width: 19, Height: 22},
		})
		stepper.SetIncrement(1)
		stepper.SetValueWraps(false)
		objc.Send[objc.ID](stepper.ID, objc.Sel("setAutoresizingMask:"), uint(selectorViewMinX|selectorViewMinY))
		objc.Send[objc.ID](stepper.ID, objc.Sel("setTarget:"), s.delegateID)
		objc.Send[objc.ID](stepper.ID, objc.Sel("setAction:"), objc.RegisterName(action))
		objc.Send[objc.ID](box.ID, objc.Sel("addSubview:"), stepper.ID)

		return field, stepper
	}

	s.cpuField, s.cpuStepper = addRow("CPUs", boxHeight-192, "hardwareCPUChanged:")
	s.cpuStepper.SetMinValue(float64(s.bounds.MinCPU))
	s.cpuStepper.SetMaxValue(float64(s.bounds.MaxCPU))

	s.memoryField, s.memoryStepper = addRow("Memory (GB)", boxHeight-222, "hardwareMemoryChanged:")
	s.memoryStepper.SetMinValue(float64(s.bounds.MinMemoryGB))
	s.memoryStepper.SetMaxValue(float64(s.bounds.MaxMemoryGB))

	s.hardwareHint = selectorLabel(
		s.bounds.hint(),
		corefoundation.CGRect{
			Origin: corefoundation.CGPoint{X: 18, Y: boxHeight - 242},
			Size:   corefoundation.CGSize{Width: width - 36, Height: 16},
		},
		appkit.GetNSFontClass().SystemFontOfSize(10),
		secondary,
	)
	objc.Send[objc.ID](s.hardwareHint.ID, objc.Sel("setAutoresizingMask:"), uint(selectorViewWidth|selectorViewMinY))
	objc.Send[objc.ID](box.ID, objc.Sel("addSubview:"), s.hardwareHint.ID)
}

// updateHardwareControls refreshes the CPU and memory controls for vm.
// Controls are disabled when no VM is selected or the VM is running, since a
// running VM's hardware cannot be changed until it is next booted.
func (s *VMSelector) updateHardwareControls(vm *vmconfig.Info) {
	if s.cpuField.ID == 0 {
		return
	}
	if vm == nil {
		s.cpuField.SetStringValue("-")
		s.memoryField.SetStringValue("-")
		s.setHardwareControlsEnabled(false)
		return
	}
	hw := loadVMHardware(vm.Path, s.bounds)
	s.setHardwareValues(hw)
	s.setHardwareControlsEnabled(vm.State != "running")
	if vm.State == "running" {
		s.hardwareHint.SetStringValue("Stop the VM to change CPU or memory")
		return
	}
	s.hardwareHint.SetStringValue(s.bounds.hint())
}

func (s *VMSelector) setHardwareValues(hw vmconfig.Hardware) {
	s.cpuField.SetStringValue(strconv.FormatUint(uint64(hw.CPU), 10))
	s.cpuStepper.SetDoubleValue(float64(hw.CPU))
	s.memoryField.SetStringValue(strconv.FormatUint(hw.MemoryGB, 10))
	s.memoryStepper.SetDoubleValue(float64(hw.MemoryGB))
}

func (s *VMSelector) setHardwareControlsEnabled(enabled bool) {
	for _, id := range []objc.ID{s.cpuField.ID, s.cpuStepper.ID, s.memoryField.ID, s.memoryStepper.ID} {
		objc.Send[objc.ID](id, objc.Sel("setEnabled:"), enabled)
	}
}

// handleCPUChanged is the action for both the CPU field and its stepper.
func (s *VMSelector) handleCPUChanged(_ objc.ID, _ objc.SEL, sender objc.ID) {
	s.applyHardwareEdit(sender == s.cpuStepper.ID, true)
}

// handleMemoryChanged is the action for both the memory field and its stepper.
func (s *VMSelector) handleMemoryChanged(_ objc.ID, _ objc.SEL, sender objc.ID) {
	s.applyHardwareEdit(sender == s.memoryStepper.ID, false)
}

// applyHardwareEdit reads the edited value, clamps it to the host bounds,
// writes it back to both controls, and persists it to the VM config.
func (s *VMSelector) applyHardwareEdit(fromStepper, isCPU bool) {
	vm := s.selectedVM()
	if vm == nil || vm.State == "running" {
		return
	}
	hw := loadVMHardware(vm.Path, s.bounds)
	if isCPU {
		value := s.cpuField.IntegerValue()
		if fromStepper {
			value = int(s.cpuStepper.DoubleValue())
		}
		if value < 0 {
			value = 0
		}
		hw.CPU = s.bounds.clampCPU(uint(value), hw.CPU)
	} else {
		value := s.memoryField.IntegerValue()
		if fromStepper {
			value = int(s.memoryStepper.DoubleValue())
		}
		if value < 0 {
			value = 0
		}
		hw.MemoryGB = s.bounds.clampMemoryGB(uint64(value), hw.MemoryGB)
	}
	s.setHardwareValues(hw)

	if _, err := vmconfig.SetHardware(vm.Path, hw); err != nil {
		s.hardwareHint.SetStringValue(fmt.Sprintf("save failed: %v", err))
		return
	}
	s.hardwareHint.SetStringValue(fmt.Sprintf("Saved - applies on next boot (%s)", s.bounds.hint()))
}
