package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
	"unsafe"

	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/objectivec"
	privvz "github.com/tmc/apple/private/virtualization"
)

// controlTestRequireRunningVM skips the test unless VZ_TEST_VM_NAME is set.
func controlTestRequireRunningVM(t *testing.T) string {
	t.Helper()
	name := os.Getenv("VZ_TEST_VM_NAME")
	if name == "" {
		t.Skip("VZ_TEST_VM_NAME not set; skipping live VM test")
	}
	return name
}

// controlTestConnectVM wraps the running ControlServer's vm as a private
// VZVirtualMachine. Requires VZ_TEST_LIVE=1 and a VM running in-process.
func controlTestConnectVM(t *testing.T, name string) privvz.VZVirtualMachine {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home dir: %v", err)
	}
	sock := filepath.Join(home, ".vz", "vms", name, "control.sock")
	if _, err := os.Stat(sock); err != nil {
		t.Skipf("control socket not found at %s: %v", sock, err)
	}
	if os.Getenv("VZ_TEST_LIVE") != "1" {
		t.Skip("VZ_TEST_LIVE=1 required to test private APIs against a live VM object")
	}
	t.Fatalf("in-process VM access not yet wired up")
	return privvz.VZVirtualMachine{}
}

// =============================================================================
// Compilation tests: verify private API bindings resolve without a running VM.
// =============================================================================

func TestPrivateControlAPIClassExists(t *testing.T) {
	cls := privvz.GetVZVirtualMachineClass()
	if cls == (privvz.VZVirtualMachineClass{}) {
		t.Fatal("VZVirtualMachineClass not found")
	}
	t.Logf("VZVirtualMachine class resolved")
}

func TestPrivateControlAPISelectorCompilation(t *testing.T) {
	// These tests verify the generated bindings compile. The selectors are
	// exercised at runtime only against a live VM (see live tests below).
	tests := []struct {
		name     string
		selector string
	}{
		{"ResetWithType", "_resetWithType:completionHandler:"},
		{"SaveMachineState", "_saveMachineStateToURL:options:completionHandler:"},
		{"EnterRestrictedMode", "_enterRestrictedModeWithCompletionHandler:"},
		{"ValidateRestrictedMode", "_validateRestrictedModeSupportWithError:"},
		{"CreateViewEndpoint", "_createViewEndpointWithOptions:"},
		{"DebugStub", "_debugStub"},
		{"HIDEventMonitor", "_hidEventMonitor"},
		{"CurrentConfiguration", "_currentConfiguration"},
		{"CreateCore", "_createCoreWithCompletionHandler:"},
		{"CreateCores", "_createCoresWithCompletionHandler:"},
		{"StateDescription", "_stateDescription"},
		{"CanCreateCore", "_canCreateCore"},
		{"ShouldSendHIDReports", "_shouldSendHIDReports"},
		{"ServiceProcessIdentifier", "_serviceProcessIdentifier"},
		{"CrashContextMessage", "_crashContextMessage"},
		{"Name", "_name"},
		{"SetName", "_setName:"},
		{"AudioDevices", "_audioDevices"},
		{"Keyboards", "_keyboards"},
		{"PointingDevices", "_pointingDevices"},
		{"MultiTouchDevices", "_multiTouchDevices"},
		{"SerialPorts", "_serialPorts"},
		{"StorageDevices", "_storageDevices"},
		{"Coprocessors", "_coprocessors"},
		{"PowerSourceDevices", "_powerSourceDevices"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sel := objc.Sel(tt.selector)
			if sel == 0 {
				t.Fatalf("selector %q resolved to nil", tt.selector)
			}
			t.Logf("selector %q OK", tt.selector)
		})
	}
}

// =============================================================================
// Live VM tests: require VZ_TEST_VM_NAME + VZ_TEST_LIVE=1.
// These probe the private API behavior and log results for discovery.
// =============================================================================

func TestPrivateControlAPIResetTypes(t *testing.T) {
	name := controlTestRequireRunningVM(t)
	vm := controlTestConnectVM(t, name)

	for _, resetType := range []int64{0, 1, 2} {
		t.Run(fmt.Sprintf("type=%d", resetType), func(t *testing.T) {
			done := make(chan error, 1)
			vm.ResetWithTypeCompletionHandler(resetType, func(err error) {
				done <- err
			})
			select {
			case err := <-done:
				if err != nil {
					t.Logf("reset type %d: error=%v", resetType, err)
				} else {
					t.Logf("reset type %d: success", resetType)
				}
			case <-time.After(30 * time.Second):
				t.Fatalf("reset type %d: timed out", resetType)
			}
		})
	}
}

func TestPrivateControlAPISaveMachineState(t *testing.T) {
	name := controlTestRequireRunningVM(t)
	vm := controlTestConnectVM(t, name)

	tmpDir := t.TempDir()
	savePath := filepath.Join(tmpDir, "test-private-save.vmstate")
	url := foundation.NewURLFileURLWithPath(savePath)

	done := make(chan error, 1)
	var nilObj objectivec.Object
	vm.SaveMachineStateToURLOptionsCompletionHandler(url, nilObj, func(err error) {
		done <- err
	})
	select {
	case err := <-done:
		if err != nil {
			t.Logf("save with nil options: error=%v", err)
		} else {
			t.Log("save with nil options: success")
			if fi, statErr := os.Stat(savePath); statErr == nil {
				t.Logf("saved state file size: %d bytes", fi.Size())
			}
		}
	case <-time.After(60 * time.Second):
		t.Fatal("save machine state: timed out")
	}
}

func TestPrivateControlAPIRestrictedMode(t *testing.T) {
	name := controlTestRequireRunningVM(t)
	vm := controlTestConnectVM(t, name)

	supported, err := vm.ValidateRestrictedModeSupportWithError()
	t.Logf("restricted mode supported: %v, error: %v", supported, err)

	if !supported {
		t.Skip("restricted mode not supported on this VM configuration")
	}

	done := make(chan error, 1)
	vm.EnterRestrictedModeWithCompletionHandler(func(err error) {
		done <- err
	})
	select {
	case err := <-done:
		if err != nil {
			t.Logf("enter restricted mode: error=%v", err)
		} else {
			t.Log("enter restricted mode: success")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("enter restricted mode: timed out")
	}
}

func TestPrivateControlAPICreateViewEndpoint(t *testing.T) {
	name := controlTestRequireRunningVM(t)
	vm := controlTestConnectVM(t, name)

	for _, opt := range []uint64{0, 1, 2} {
		t.Run(fmt.Sprintf("options=%d", opt), func(t *testing.T) {
			endpoint, err := vm.CreateViewEndpointWithOptions(opt)
			if err != nil {
				t.Logf("options=%d: error=%v", opt, err)
				return
			}
			if endpoint == nil {
				t.Logf("options=%d: returned nil", opt)
				return
			}
			obj, ok := endpoint.(objectivec.Object)
			if ok && obj.ID != 0 {
				t.Logf("options=%d: returned object ID=%v", opt, obj.ID)
			} else {
				t.Logf("options=%d: returned non-nil but zero or non-Object", opt)
			}
		})
	}
}

func TestPrivateControlAPIDebugStub(t *testing.T) {
	name := controlTestRequireRunningVM(t)
	vm := controlTestConnectVM(t, name)

	// Use objc.Send directly since the method returns unsafe.Pointer (unexported).
	stub := objc.Send[unsafe.Pointer](vm.ID, objc.Sel("_debugStub"))
	if stub == nil {
		t.Log("debug stub: nil (not configured)")
	} else {
		t.Logf("debug stub: %v", stub)
	}
}

func TestPrivateControlAPIHIDEventMonitor(t *testing.T) {
	name := controlTestRequireRunningVM(t)
	vm := controlTestConnectVM(t, name)

	monitor := objc.Send[unsafe.Pointer](vm.ID, objc.Sel("_hidEventMonitor"))
	if monitor == nil {
		t.Log("HID event monitor: nil")
	} else {
		t.Logf("HID event monitor: %v", monitor)
	}
}

func TestPrivateControlAPICurrentConfiguration(t *testing.T) {
	name := controlTestRequireRunningVM(t)
	vm := controlTestConnectVM(t, name)

	configID := objc.Send[objc.ID](vm.ID, objc.Sel("_currentConfiguration"))
	if configID == 0 {
		t.Log("current configuration: nil")
	} else {
		t.Logf("current configuration: objc.ID=%v", configID)
		config := privvz.VZVirtualMachineConfigurationFromID(configID)
		t.Logf("wrapped as: %T", config)
	}
}

func TestPrivateControlAPIDiagnosticProperties(t *testing.T) {
	name := controlTestRequireRunningVM(t)
	vm := controlTestConnectVM(t, name)

	stateDesc := objc.Send[objc.ID](vm.ID, objc.Sel("_stateDescription"))
	t.Logf("state description: %q", foundation.NSStringFromID(stateDesc).String())

	t.Logf("state: %d", vm.State())

	canCreate := objc.Send[bool](vm.ID, objc.Sel("_canCreateCore"))
	t.Logf("can create core: %v", canCreate)

	hidReports, err := vm.ShouldSendHIDReports()
	if err != nil {
		t.Logf("should send HID reports: error=%v", err)
	} else {
		t.Logf("should send HID reports: %v", hidReports)
	}

	pid := objc.Send[int](vm.ID, objc.Sel("_serviceProcessIdentifier"))
	t.Logf("service process identifier: %d", pid)

	crashMsg := objc.Send[objc.ID](vm.ID, objc.Sel("_crashContextMessage"))
	t.Logf("crash context message: %q", foundation.NSStringFromID(crashMsg).String())

	vmName := objc.Send[objc.ID](vm.ID, objc.Sel("_name"))
	t.Logf("VM name: %q", foundation.NSStringFromID(vmName).String())
}

func TestPrivateControlAPICreateCores(t *testing.T) {
	name := controlTestRequireRunningVM(t)
	vm := controlTestConnectVM(t, name)

	done := make(chan error, 1)
	vm.CreateCoreWithCompletionHandler(func(err error) {
		done <- err
	})
	select {
	case err := <-done:
		if err != nil {
			t.Logf("create core: error=%v", err)
		} else {
			t.Log("create core: success")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("create core: timed out")
	}

	done2 := make(chan error, 1)
	vm.CreateCoresWithCompletionHandler(func(err error) {
		done2 <- err
	})
	select {
	case err := <-done2:
		if err != nil {
			t.Logf("create cores: error=%v", err)
		} else {
			t.Log("create cores: success")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("create cores: timed out")
	}
}

func TestPrivateControlAPIDeviceArrays(t *testing.T) {
	name := controlTestRequireRunningVM(t)
	vm := controlTestConnectVM(t, name)

	arrays := []struct {
		name string
		sel  string
	}{
		{"audio devices", "_audioDevices"},
		{"keyboards", "_keyboards"},
		{"pointing devices", "_pointingDevices"},
		{"multi-touch devices", "_multiTouchDevices"},
		{"serial ports", "_serialPorts"},
		{"storage devices", "_storageDevices"},
		{"coprocessors", "_coprocessors"},
		{"power source devices", "_powerSourceDevices"},
	}
	for _, a := range arrays {
		arrID := objc.Send[objc.ID](vm.ID, objc.Sel(a.sel))
		count := int64(0)
		if arrID != 0 {
			count = objc.Send[int64](arrID, objc.Sel("count"))
		}
		t.Logf("%s: count=%d", a.name, count)
	}
}

func TestPrivateControlAPISetName(t *testing.T) {
	name := controlTestRequireRunningVM(t)
	vm := controlTestConnectVM(t, name)

	origID := objc.Send[objc.ID](vm.ID, objc.Sel("_name"))
	original := foundation.NSStringFromID(origID).String()
	t.Logf("original name: %q", original)

	objc.Send[objc.ID](vm.ID, objc.Sel("_setName:"), objc.String("test-private-api"))
	got := objc.Send[objc.ID](vm.ID, objc.Sel("_name"))
	t.Logf("name after set: %q", foundation.NSStringFromID(got).String())

	objc.Send[objc.ID](vm.ID, objc.Sel("_setName:"), objc.String(original))
	restored := objc.Send[objc.ID](vm.ID, objc.Sel("_name"))
	t.Logf("restored name: %q", foundation.NSStringFromID(restored).String())
}
