package main

import (
	"fmt"
	"testing"
	"unsafe"

	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/objectivec"
	pvz "github.com/tmc/apple/private/virtualization"
)

// safeCall runs fn and recovers from panics, reporting them as test errors.
func safeCall(t *testing.T, name string, fn func()) (panicked bool) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("%s: panic: %v", name, r)
			panicked = true
		}
	}()
	fn()
	return false
}

func newConfig(t *testing.T) pvz.VZVirtualMachineConfiguration {
	t.Helper()
	config := pvz.NewVZVirtualMachineConfiguration()
	if config.GetID() == 0 {
		t.Fatal("failed to create VZVirtualMachineConfiguration")
	}
	return config
}

func TestPrivateConfigBoolProperties(t *testing.T) {
	config := newConfig(t)

	tests := []struct {
		name   string
		getSel string
		set    func(bool)
	}{
		{
			name:   "memoryOvercommitmentAllowed",
			getSel: "_memoryOvercommitmentAllowed",
			set:    config.SetMemoryOvercommitmentAllowed,
		},
		{
			name:   "terminationUnderMemoryPressureEnabled",
			getSel: "_terminationUnderMemoryPressureEnabled",
			set:    config.SetTerminationUnderMemoryPressureEnabled,
		},
		{
			name:   "testIgnoreEntitlementChecks",
			getSel: "_testIgnoreEntitlementChecks",
			set:    config.SetTestIgnoreEntitlementChecks,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			safeCall(t, "get default", func() {
				v := objc.Send[bool](config.ID, objc.Sel(tt.getSel))
				t.Logf("default %s = %v", tt.name, v)
			})
			safeCall(t, "set true", func() {
				tt.set(true)
				v := objc.Send[bool](config.ID, objc.Sel(tt.getSel))
				if !v {
					t.Errorf("set %s to true but got false", tt.name)
				}
				t.Logf("set %s = true -> read back %v", tt.name, v)
			})
			safeCall(t, "set false", func() {
				tt.set(false)
				v := objc.Send[bool](config.ID, objc.Sel(tt.getSel))
				if v {
					t.Errorf("set %s to false but got true", tt.name)
				}
				t.Logf("set %s = false -> read back %v", tt.name, v)
			})
		})
	}
}

func TestPrivateConfigActionProperties(t *testing.T) {
	config := newConfig(t)

	tests := []struct {
		name    string
		getSel  string
		setSel  string
		setVals []int64
	}{
		{
			name:    "fatalErrorAction",
			getSel:  "_fatalErrorAction",
			setSel:  "_setFatalErrorAction:",
			setVals: []int64{0, 1, 2},
		},
		{
			name:    "panicAction",
			getSel:  "_panicAction",
			setSel:  "_setPanicAction:",
			setVals: []int64{0, 1, 2},
		},
		{
			name:    "restartAction",
			getSel:  "_restartAction",
			setSel:  "_setRestartAction:",
			setVals: []int64{0, 1, 2},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			safeCall(t, "get default", func() {
				v := objc.Send[int64](config.ID, objc.Sel(tt.getSel))
				t.Logf("default %s = %d", tt.name, v)
			})

			for _, val := range tt.setVals {
				val := val
				safeCall(t, fmt.Sprintf("set %d", val), func() {
					objc.Send[objc.ID](config.ID, objc.Sel(tt.setSel), val)
					got := objc.Send[int64](config.ID, objc.Sel(tt.getSel))
					t.Logf("set %s = %d -> read back %d", tt.name, val, got)
				})
			}
		})
	}
}

func TestPrivateConfigArrayDefaults(t *testing.T) {
	config := newConfig(t)

	arrayProps := []struct {
		name   string
		getSel string
	}{
		{"acceleratorDevices", "_acceleratorDevices"},
		{"bifrostDevices", "_bifrostDevices"},
		{"biometricDevices", "_biometricDevices"},
		{"coprocessors", "_coprocessors"},
		{"customMMIODevices", "_customMMIODevices"},
		{"customVirtioDevices", "_customVirtioDevices"},
		{"mailboxDevices", "_mailboxDevices"},
		{"multiTouchDevices", "_multiTouchDevices"},
		{"pciPassthroughDevices", "_pciPassthroughDevices"},
		{"powerSourceDevices", "_powerSourceDevices"},
	}

	for _, tt := range arrayProps {
		t.Run(tt.name, func(t *testing.T) {
			safeCall(t, "get default", func() {
				id := objc.Send[objc.ID](config.ID, objc.Sel(tt.getSel))
				if id == 0 {
					t.Logf("default %s = nil", tt.name)
				} else {
					arr := foundation.NSArrayFromID(id)
					t.Logf("default %s = array(%d elements)", tt.name, arr.Count())
				}
			})
		})
	}
}

func TestPrivateConfigArraySetEmpty(t *testing.T) {
	config := newConfig(t)
	emptyArray := foundation.NewNSArray()

	arrayProps := []struct {
		name   string
		getSel string
		setSel string
	}{
		{"acceleratorDevices", "_acceleratorDevices", "_setAcceleratorDevices:"},
		{"bifrostDevices", "_bifrostDevices", "_setBifrostDevices:"},
		{"biometricDevices", "_biometricDevices", "_setBiometricDevices:"},
		{"coprocessors", "_coprocessors", "_setCoprocessors:"},
		{"customMMIODevices", "_customMMIODevices", "_setCustomMMIODevices:"},
		{"customVirtioDevices", "_customVirtioDevices", "_setCustomVirtioDevices:"},
		{"mailboxDevices", "_mailboxDevices", "_setMailboxDevices:"},
		{"multiTouchDevices", "_multiTouchDevices", "_setMultiTouchDevices:"},
		{"pciPassthroughDevices", "_pciPassthroughDevices", "_setPCIPassthroughDevices:"},
		{"powerSourceDevices", "_powerSourceDevices", "_setPowerSourceDevices:"},
	}

	for _, tt := range arrayProps {
		t.Run(tt.name, func(t *testing.T) {
			safeCall(t, "set empty", func() {
				objc.Send[objc.ID](config.ID, objc.Sel(tt.setSel), emptyArray.GetID())
				id := objc.Send[objc.ID](config.ID, objc.Sel(tt.getSel))
				if id == 0 {
					t.Logf("after set empty: %s = nil", tt.name)
				} else {
					arr := foundation.NSArrayFromID(id)
					t.Logf("after set empty: %s = array(%d elements)", tt.name, arr.Count())
				}
			})
		})
	}
}

func TestPrivateConfigPointerDefaults(t *testing.T) {
	config := newConfig(t)

	t.Run("debugStub", func(t *testing.T) {
		safeCall(t, "get default", func() {
			v := objc.Send[unsafe.Pointer](config.ID, objc.Sel("_debugStub"))
			t.Logf("default _debugStub = %v (nil=%v)", v, v == nil)
		})
	})

	t.Run("cpuEmulator", func(t *testing.T) {
		safeCall(t, "get default", func() {
			v := objc.Send[unsafe.Pointer](config.ID, objc.Sel("_cpuEmulator"))
			t.Logf("default _cpuEmulator = %v (nil=%v)", v, v == nil)
		})
	})

	t.Run("panicDevice", func(t *testing.T) {
		safeCall(t, "get default", func() {
			v := objc.Send[unsafe.Pointer](config.ID, objc.Sel("_panicDevice"))
			t.Logf("default _panicDevice = %v (nil=%v)", v, v == nil)
		})
	})
}

func TestPrivateConfigSetDebugStubNil(t *testing.T) {
	config := newConfig(t)

	safeCall(t, "set nil", func() {
		config.SetDebugStub(objectivec.Object{ID: 0})
		v := objc.Send[unsafe.Pointer](config.ID, objc.Sel("_debugStub"))
		t.Logf("SetDebugStub(nil) -> %v (nil=%v)", v, v == nil)
	})
}

func TestPrivateConfigClassMethods(t *testing.T) {
	configClass := pvz.GetVZVirtualMachineConfigurationClass()

	t.Run("maximumAllowedOvercommittedMemorySize", func(t *testing.T) {
		safeCall(t, "class method", func() {
			v := configClass.MaximumAllowedOvercommittedMemorySize()
			t.Logf("maximumAllowedOvercommittedMemorySize = %d bytes (%.1f GB)", v, float64(v)/(1024*1024*1024))
		})
	})
}

// TestPrivateConfigPublicProperties tests public properties via objc.Send
// on the private package's VZVirtualMachineConfiguration.
func TestPrivateConfigPublicProperties(t *testing.T) {
	config := newConfig(t)

	t.Run("CPUCount", func(t *testing.T) {
		safeCall(t, "default", func() {
			v := objc.Send[uint](config.ID, objc.Sel("CPUCount"))
			t.Logf("default CPUCount = %d", v)
		})
		safeCall(t, "set 4", func() {
			objc.Send[objc.ID](config.ID, objc.Sel("setCPUCount:"), uint(4))
			v := objc.Send[uint](config.ID, objc.Sel("CPUCount"))
			t.Logf("set CPUCount = 4 -> read back %d", v)
		})
	})

	t.Run("memorySize", func(t *testing.T) {
		safeCall(t, "default", func() {
			v := objc.Send[uint64](config.ID, objc.Sel("memorySize"))
			t.Logf("default memorySize = %d bytes (%.0f MB)", v, float64(v)/(1024*1024))
		})
		safeCall(t, "set 4GB", func() {
			var fourGB uint64 = 4 * 1024 * 1024 * 1024
			objc.Send[objc.ID](config.ID, objc.Sel("setMemorySize:"), fourGB)
			v := objc.Send[uint64](config.ID, objc.Sel("memorySize"))
			t.Logf("set memorySize = 4GB -> read back %d bytes", v)
		})
	})
}

func TestPrivateConfigIsDuplicateUSB(t *testing.T) {
	config := newConfig(t)

	safeCall(t, "isDuplicateUSB(0,0)", func() {
		v := config.IsDuplicateUSBDeviceConfigurationAtUsbDeviceIndex(0, 0)
		t.Logf("isDuplicateUSBDeviceConfiguration(0, 0) = %v", v)
	})
}

// TestPrivateConfigSharedRamRegions tests the _sharedRamRegions getter.
// KNOWN CRASH: Reading _sharedRamRegions causes an ObjC exception or crash
// on a fresh config. This test is placed last to avoid disrupting other tests.
func TestPrivateConfigSharedRamRegions(t *testing.T) {
	t.Skip("KNOWN CRASH: _sharedRamRegions causes ObjC exception on fresh config")

	config := newConfig(t)
	safeCall(t, "get default", func() {
		v := objc.Send[objc.ID](config.ID, objc.Sel("_sharedRamRegions"))
		t.Logf("default _sharedRamRegions = %v (nil=%v)", v, v == 0)
	})
}
