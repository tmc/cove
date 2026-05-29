package main

import (
	"errors"
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
		name string
		can  func() bool
		get  func() (bool, error)
		set  func(bool) error
	}{
		{
			name: "memoryOvercommitmentAllowed",
			can:  config.CanMemoryOvercommitmentAllowed,
			get:  config.MemoryOvercommitmentAllowed,
			set:  config.SetMemoryOvercommitmentAllowed,
		},
		{
			name: "terminationUnderMemoryPressureEnabled",
			can:  config.CanTerminationUnderMemoryPressureEnabled,
			get:  config.TerminationUnderMemoryPressureEnabled,
			set:  config.SetTerminationUnderMemoryPressureEnabled,
		},
		{
			name: "testIgnoreEntitlementChecks",
			can:  config.CanTestIgnoreEntitlementChecks,
			get:  config.TestIgnoreEntitlementChecks,
			set:  config.SetTestIgnoreEntitlementChecks,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.can() {
				t.Skipf("private selector for %s is unavailable on this host", tt.name)
			}
			v, err := tt.get()
			if unavailablePrivateSelector(err) {
				t.Skipf("private selector for %s is unavailable on this host: %v", tt.name, err)
			}
			if err != nil {
				t.Fatalf("get default: %v", err)
			}
			t.Logf("default %s = %v", tt.name, v)

			if err := tt.set(true); unavailablePrivateSelector(err) {
				t.Skipf("private setter for %s is unavailable on this host: %v", tt.name, err)
			} else if err != nil {
				t.Fatalf("set true: %v", err)
			}
			v, err = tt.get()
			if err != nil {
				t.Fatalf("get after set true: %v", err)
			}
			if !v {
				t.Errorf("set %s to true but got false", tt.name)
			}
			t.Logf("set %s = true -> read back %v", tt.name, v)

			if err := tt.set(false); unavailablePrivateSelector(err) {
				t.Skipf("private setter for %s is unavailable on this host: %v", tt.name, err)
			} else if err != nil {
				t.Fatalf("set false: %v", err)
			}
			v, err = tt.get()
			if err != nil {
				t.Fatalf("get after set false: %v", err)
			}
			if v {
				t.Errorf("set %s to false but got true", tt.name)
			}
			t.Logf("set %s = false -> read back %v", tt.name, v)
		})
	}
}

func unavailablePrivateSelector(err error) bool {
	return errors.Is(err, objc.ErrUnrecognizedSelector)
}

func configRespondsToSelector(config pvz.VZVirtualMachineConfiguration, sel string) bool {
	return objc.RespondsToSelector(config.ID, objc.Sel(sel))
}

func TestPrivateConfigActionProperties(t *testing.T) {
	config := newConfig(t)

	tests := []struct {
		name    string
		can     func() bool
		get     func() (int64, error)
		set     func(int64) error
		setVals []int64
	}{
		{
			name:    "fatalErrorAction",
			can:     config.CanFatalErrorAction,
			get:     config.FatalErrorAction,
			set:     config.SetFatalErrorAction,
			setVals: []int64{0, 1, 2},
		},
		{
			name:    "panicAction",
			can:     config.CanPanicAction,
			get:     config.PanicAction,
			set:     config.SetPanicAction,
			setVals: []int64{0, 1, 2},
		},
		{
			name:    "restartAction",
			can:     config.CanRestartAction,
			get:     config.RestartAction,
			set:     config.SetRestartAction,
			setVals: []int64{0, 1, 2},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.can() {
				t.Skipf("private selector for %s is unavailable on this host", tt.name)
			}
			v, err := tt.get()
			if unavailablePrivateSelector(err) {
				t.Skipf("private selector for %s is unavailable on this host: %v", tt.name, err)
			}
			if err != nil {
				t.Fatalf("get default: %v", err)
			}
			t.Logf("default %s = %d", tt.name, v)

			for _, val := range tt.setVals {
				val := val
				if err := tt.set(val); unavailablePrivateSelector(err) {
					t.Skipf("private setter for %s is unavailable on this host: %v", tt.name, err)
				} else if err != nil {
					t.Fatalf("set %d: %v", val, err)
				}
				got, err := tt.get()
				if err != nil {
					t.Fatalf("get after set %d: %v", val, err)
				}
				t.Logf("set %s = %d -> read back %d", tt.name, val, got)
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
				if !configRespondsToSelector(config, tt.getSel) {
					t.Skipf("private selector %s is unavailable on this host", tt.getSel)
				}
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
				if !configRespondsToSelector(config, tt.getSel) {
					t.Skipf("private selector %s is unavailable on this host", tt.getSel)
				}
				if !configRespondsToSelector(config, tt.setSel) {
					t.Skipf("private selector %s is unavailable on this host", tt.setSel)
				}
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
			if !configRespondsToSelector(config, "_debugStub") {
				t.Skip("private selector _debugStub is unavailable on this host")
			}
			v := objc.Send[unsafe.Pointer](config.ID, objc.Sel("_debugStub"))
			t.Logf("default _debugStub = %v (nil=%v)", v, v == nil)
		})
	})

	t.Run("cpuEmulator", func(t *testing.T) {
		safeCall(t, "get default", func() {
			if !configRespondsToSelector(config, "_cpuEmulator") {
				t.Skip("private selector _cpuEmulator is unavailable on this host")
			}
			v := objc.Send[unsafe.Pointer](config.ID, objc.Sel("_cpuEmulator"))
			t.Logf("default _cpuEmulator = %v (nil=%v)", v, v == nil)
		})
	})

	t.Run("panicDevice", func(t *testing.T) {
		safeCall(t, "get default", func() {
			if !configRespondsToSelector(config, "_panicDevice") {
				t.Skip("private selector _panicDevice is unavailable on this host")
			}
			v := objc.Send[unsafe.Pointer](config.ID, objc.Sel("_panicDevice"))
			t.Logf("default _panicDevice = %v (nil=%v)", v, v == nil)
		})
	})
}

func TestPrivateConfigSetDebugStubNil(t *testing.T) {
	config := newConfig(t)

	safeCall(t, "set nil", func() {
		config.SetDebugStub(objectivec.Object{ID: 0})
		if !configRespondsToSelector(config, "_debugStub") {
			t.Log("_debugStub unavailable")
			return
		}
		v := objc.Send[unsafe.Pointer](config.ID, objc.Sel("_debugStub"))
		t.Logf("SetDebugStub(nil) -> %v (nil=%v)", v, v == nil)
	})
}

func TestPrivateConfigClassMethods(t *testing.T) {
	configClass := pvz.GetVZVirtualMachineConfigurationClass()

	t.Run("maximumAllowedOvercommittedMemorySize", func(t *testing.T) {
		safeCall(t, "class method", func() {
			v, err := configClass.MaximumAllowedOvercommittedMemorySize()
			if unavailablePrivateSelector(err) {
				t.Skipf("private selector unavailable: %v", err)
			}
			if err != nil {
				t.Fatalf("maximumAllowedOvercommittedMemorySize: %v", err)
			}
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
		v, err := config.IsDuplicateUSBDeviceConfigurationAtUsbDeviceIndex(0, 0)
		if unavailablePrivateSelector(err) {
			t.Skipf("private selector unavailable: %v", err)
		}
		if err != nil {
			t.Fatalf("isDuplicateUSBDeviceConfiguration: %v", err)
		}
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
