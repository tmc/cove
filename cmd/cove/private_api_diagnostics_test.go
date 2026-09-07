//go:build darwin && arm64

package main

import (
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/dispatch"
	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/objectivec"
	privvz "github.com/tmc/apple/private/virtualization"
	"github.com/tmc/apple/security"
	vz "github.com/tmc/apple/virtualization"
)

// createMinimalMacVM creates a minimal VZVirtualMachine with a macOS config
// backed by the default VM disk. Returns zero IDs if the VM directory is missing.
func createMinimalMacVM(t *testing.T) (vz.VZVirtualMachine, privvz.VZVirtualMachine, dispatch.Queue) {
	t.Helper()

	home, _ := os.UserHomeDir()
	vmPath := filepath.Join(home, ".vz", "vms", "default")

	diskPath := filepath.Join(vmPath, "disk.img")
	auxPath := filepath.Join(vmPath, "aux.img")
	hwModelPath := filepath.Join(vmPath, "hw.model")
	machineIDPath := filepath.Join(vmPath, "machine.id")

	for _, p := range []string{diskPath, auxPath, hwModelPath, machineIDPath} {
		if _, err := os.Stat(p); err != nil {
			t.Skipf("VM file missing: %s (no default VM installed)", p)
		}
	}

	// Load hardware model
	hwModelData, err := os.ReadFile(hwModelPath)
	if err != nil {
		t.Fatalf("read hw.model: %v", err)
	}
	hwModelNSData := foundation.NewDataWithBytesLength(hwModelData)
	hwModel := objc.Send[objc.ID](
		objc.Send[objc.ID](objc.ID(objc.GetClass("VZMacHardwareModel")), objc.Sel("alloc")),
		objc.Sel("initWithDataRepresentation:"), hwModelNSData.ID,
	)
	if hwModel == 0 {
		t.Fatal("failed to create VZMacHardwareModel")
	}

	// Load machine identifier
	machineIDData, err := os.ReadFile(machineIDPath)
	if err != nil {
		t.Fatalf("read machine.id: %v", err)
	}
	machineIDNSData := foundation.NewDataWithBytesLength(machineIDData)
	machineID := objc.Send[objc.ID](
		objc.Send[objc.ID](objc.ID(objc.GetClass("VZMacMachineIdentifier")), objc.Sel("alloc")),
		objc.Sel("initWithDataRepresentation:"), machineIDNSData.ID,
	)
	if machineID == 0 {
		t.Fatal("failed to create VZMacMachineIdentifier")
	}

	// Auxiliary storage (existing)
	auxURL := objc.Send[objc.ID](objc.ID(objc.GetClass("NSURL")), objc.Sel("fileURLWithPath:"), objc.String(auxPath))
	auxStorage := objc.Send[objc.ID](
		objc.Send[objc.ID](objc.ID(objc.GetClass("VZMacAuxiliaryStorage")), objc.Sel("alloc")),
		objc.Sel("initWithURL:"), auxURL,
	)

	// Platform config
	platform := objc.Send[objc.ID](
		objc.Send[objc.ID](objc.ID(objc.GetClass("VZMacPlatformConfiguration")), objc.Sel("alloc")),
		objc.Sel("init"),
	)
	objc.Send[objc.ID](platform, objc.Sel("setHardwareModel:"), hwModel)
	objc.Send[objc.ID](platform, objc.Sel("setMachineIdentifier:"), machineID)
	objc.Send[objc.ID](platform, objc.Sel("setAuxiliaryStorage:"), auxStorage)

	// Boot loader
	bootLoader := objc.Send[objc.ID](
		objc.Send[objc.ID](objc.ID(objc.GetClass("VZMacOSBootLoader")), objc.Sel("alloc")),
		objc.Sel("init"),
	)

	// Disk attachment
	diskURL := objc.Send[objc.ID](objc.ID(objc.GetClass("NSURL")), objc.Sel("fileURLWithPath:"), objc.String(diskPath))
	var diskErrPtr objc.ID
	diskAttachment := objc.Send[objc.ID](
		objc.Send[objc.ID](objc.ID(objc.GetClass("VZDiskImageStorageDeviceAttachment")), objc.Sel("alloc")),
		objc.Sel("initWithURL:readOnly:cachingMode:synchronizationMode:error:"),
		diskURL, true, /* readOnly */
		int64(0), /* automatic caching */
		int64(1), /* full sync */
		&diskErrPtr,
	)
	if diskErrPtr != 0 {
		errMsg := foundation.NSStringFromID(objc.Send[objc.ID](diskErrPtr, objc.Sel("localizedDescription"))).String()
		t.Fatalf("disk attachment error: %s", errMsg)
	}

	// Storage device config
	storageConfig := objc.Send[objc.ID](
		objc.Send[objc.ID](objc.ID(objc.GetClass("VZVirtioBlockDeviceConfiguration")), objc.Sel("alloc")),
		objc.Sel("initWithAttachment:"), diskAttachment,
	)

	// VM configuration
	config := objc.Send[objc.ID](
		objc.Send[objc.ID](objc.ID(objc.GetClass("VZVirtualMachineConfiguration")), objc.Sel("alloc")),
		objc.Sel("init"),
	)
	objc.Send[objc.ID](config, objc.Sel("setPlatform:"), platform)
	objc.Send[objc.ID](config, objc.Sel("setBootLoader:"), bootLoader)
	objc.Send[objc.ID](config, objc.Sel("setCPUCount:"), uint64(2))
	objc.Send[objc.ID](config, objc.Sel("setMemorySize:"), uint64(2*1024*1024*1024))

	// Set storage devices array
	storageArray := objc.Send[objc.ID](
		objc.ID(objc.GetClass("NSArray")), objc.Sel("arrayWithObject:"), storageConfig,
	)
	objc.Send[objc.ID](config, objc.Sel("setStorageDevices:"), storageArray)

	// Validate config
	var validateErrPtr objc.ID
	valid := objc.Send[bool](config, objc.Sel("validateWithError:"), &validateErrPtr)
	if !valid {
		errMsg := "(nil)"
		if validateErrPtr != 0 {
			errMsg = foundation.NSStringFromID(objc.Send[objc.ID](validateErrPtr, objc.Sel("localizedDescription"))).String()
		}
		t.Fatalf("config validation failed: %s", errMsg)
	}

	// Create VM with dispatch queue
	queue := dispatch.QueueCreate("com.test.private-api-diagnostics")
	vmInstance := objc.Send[objc.ID](
		objc.Send[objc.ID](objc.ID(objc.GetClass("VZVirtualMachine")), objc.Sel("alloc")),
		objc.Sel("initWithConfiguration:queue:"), config, queue.Handle(),
	)
	if vmInstance == 0 {
		t.Fatal("failed to create VZVirtualMachine")
	}

	pubVM := vz.VZVirtualMachine{Object: objectivec.Object{ID: vmInstance}}
	privVM := privvz.VZVirtualMachineFromID(vmInstance)
	t.Cleanup(func() { queue.Sync(func() { pubVM.Release() }) })

	return pubVM, privVM, queue
}

func TestPrivateAPI_StateDescription(t *testing.T) {
	t.Skip("_stateDescription causes SIGTRAP on stopped VMs; use -integration for live VM tests")
}

func TestPrivateAPI_NameGetSet(t *testing.T) {
	requirePrivateVirtualization(t)
	_, privVM, queue := createMinimalMacVM(t)
	if !objc.RespondsToSelector(privVM.ID, objc.Sel("_name")) || !objc.RespondsToSelector(privVM.ID, objc.Sel("_setName:")) {
		t.Skip("_name accessors unavailable")
	}
	const testName = "diagnostics-test-vm"
	var initialName, newName string
	queue.Sync(func() {
		nameID := objc.Send[objc.ID](privVM.ID, objc.Sel("_name"))
		initialName = foundation.NSStringFromID(nameID).String()
		objc.Send[struct{}](privVM.ID, objc.Sel("_setName:"), objc.String(testName))
		nameID = objc.Send[objc.ID](privVM.ID, objc.Sel("_name"))
		newName = foundation.NSStringFromID(nameID).String()
	})
	t.Logf("_name: %q -> %q", initialName, newName)
	if newName != testName {
		t.Errorf("_name = %q, want %q", newName, testName)
	}
}

func TestPrivateAPI_CrashContextMessage(t *testing.T) {
	_, privVM, queue := createMinimalMacVM(t)
	if !objc.RespondsToSelector(privVM.ID, objc.Sel("_crashContextMessage")) || !objc.RespondsToSelector(privVM.ID, objc.Sel("_setCrashContextMessage:")) {
		t.Skip("_crashContextMessage accessors unavailable")
	}
	const testMessage = "test-crash-context-diagnostics"
	var initialMessage, newMessage string
	queue.Sync(func() {
		messageID := objc.Send[objc.ID](privVM.ID, objc.Sel("_crashContextMessage"))
		initialMessage = foundation.NSStringFromID(messageID).String()
		objc.Send[struct{}](privVM.ID, objc.Sel("_setCrashContextMessage:"), objc.String(testMessage))
		messageID = objc.Send[objc.ID](privVM.ID, objc.Sel("_crashContextMessage"))
		newMessage = foundation.NSStringFromID(messageID).String()
	})
	t.Logf("_crashContextMessage: %q -> %q", initialMessage, newMessage)
	if newMessage != testMessage {
		t.Errorf("_crashContextMessage = %q, want %q", newMessage, testMessage)
	}
}

func TestPrivateAPI_ServiceProcessIdentifier(t *testing.T) {
	t.Skip("_serviceProcessIdentifier causes SIGTRAP on stopped VMs; use -integration for live VM tests")
}

func TestPrivateAPI_ShouldSendHIDReports(t *testing.T) {
	t.Skip("_shouldSendHIDReports causes SIGTRAP on stopped VMs; use -integration for live VM tests")
}

func TestPrivateAPI_CanCreateCore(t *testing.T) {
	t.Skip("_canCreateCore causes SIGTRAP on stopped VMs; use -integration for live VM tests")
}

func TestPrivateAPI_DeviceArrays(t *testing.T) {
	t.Skip("device array accessors cause SIGTRAP on stopped VMs; use -integration for live VM tests")
}

func TestPrivateAPI_PublicState(t *testing.T) {
	pubVM, _, queue := createMinimalMacVM(t)
	var state vz.VZVirtualMachineState
	queue.Sync(func() { state = pubVM.State() })
	if state != vz.VZVirtualMachineStateStopped {
		t.Errorf("state = %v, want stopped", state)
	}
}

func TestPrivateAPI_DiagnosticsSummary(t *testing.T) {
	t.Skip("_stateDescription causes SIGTRAP on stopped VMs; use -integration for live VM tests")
}

func requirePrivateVirtualization(t *testing.T) {
	t.Helper()
	task := security.SecTaskCreateFromSelf(0)
	if task == 0 {
		t.Fatal("create security task")
	}
	defer corefoundation.CFRelease(unsafe.Pointer(task))
	key := corefoundation.CFStringCreateWithCString(0, "com.apple.private.virtualization", corefoundation.CFStringEncoding(corefoundation.KCFStringEncodingUTF8))
	if key == 0 {
		t.Fatal("create entitlement key")
	}
	defer corefoundation.CFRelease(unsafe.Pointer(key))
	var taskErr corefoundation.CFErrorRef
	value := security.SecTaskCopyValueForEntitlement(task, key, &taskErr)
	if taskErr != 0 {
		defer corefoundation.CFRelease(unsafe.Pointer(taskErr))
		t.Fatalf("read private virtualization entitlement: code %d", corefoundation.CFErrorGetCode(taskErr))
	}
	if value == nil {
		t.Skip("requires effective com.apple.private.virtualization entitlement")
	}
	defer corefoundation.CFRelease(value)
	if corefoundation.CFGetTypeID(value) != corefoundation.CFBooleanGetTypeID() || !corefoundation.CFBooleanGetValue(corefoundation.CFBooleanRef(uintptr(value))) {
		t.Skip("requires effective com.apple.private.virtualization entitlement")
	}
}
