//go:build darwin && arm64

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/apple/dispatch"
	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/objectivec"
	privvz "github.com/tmc/apple/private/virtualization"
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
	objc.Send[objc.ID](vmInstance, objc.Sel("retain"))

	pubVM := vz.VZVirtualMachine{Object: objectivec.Object{ID: vmInstance}}
	privVM := privvz.VZVirtualMachineFromID(vmInstance)

	return pubVM, privVM, queue
}

func TestPrivateAPI_StateDescription(t *testing.T) {
	t.Skip("_stateDescription causes SIGTRAP on stopped VMs; use -integration for live VM tests")
}

func TestPrivateAPI_NameGetSet(t *testing.T) {
	_, privVM, _ := createMinimalMacVM(t)

	if !objc.RespondsToSelector(privVM.ID, objc.Sel("_name")) || !objc.RespondsToSelector(privVM.ID, objc.Sel("_setName:")) {
		t.Skip("_name accessors unavailable")
	}

	// Get initial name
	nameID := objc.Send[objc.ID](privVM.ID, objc.Sel("_name"))
	initialName := foundation.NSStringFromID(nameID).String()
	t.Logf("_name (initial): %q", initialName)

	// Set a name (use objc.Send directly — binding generates wrong selector "set_name:" instead of "_setName:")
	testName := "diagnostics-test-vm"
	objc.Send[objc.ID](privVM.ID, objc.Sel("_setName:"), objc.String(testName))

	nameID = objc.Send[objc.ID](privVM.ID, objc.Sel("_name"))
	newName := foundation.NSStringFromID(nameID).String()
	t.Logf("_name (after set): %q", newName)

	if newName != testName {
		t.Errorf("_name after Set_name(%q) = %q", testName, newName)
	}
}

func TestPrivateAPI_CrashContextMessage(t *testing.T) {
	_, privVM, _ := createMinimalMacVM(t)

	if !objc.RespondsToSelector(privVM.ID, objc.Sel("_crashContextMessage")) || !objc.RespondsToSelector(privVM.ID, objc.Sel("_setCrashContextMessage:")) {
		t.Skip("_crashContextMessage accessors unavailable")
	}

	// Get initial crash context
	msgID := objc.Send[objc.ID](privVM.ID, objc.Sel("_crashContextMessage"))
	initialMsg := foundation.NSStringFromID(msgID).String()
	t.Logf("_crashContextMessage (initial): %q", initialMsg)

	// Set crash context (use objc.Send directly — binding generates wrong selector)
	testMsg := "test-crash-context-diagnostics"
	objc.Send[objc.ID](privVM.ID, objc.Sel("_setCrashContextMessage:"), objc.String(testMsg))

	msgID = objc.Send[objc.ID](privVM.ID, objc.Sel("_crashContextMessage"))
	newMsg := foundation.NSStringFromID(msgID).String()
	t.Logf("_crashContextMessage (after set): %q", newMsg)

	if newMsg != testMsg {
		t.Errorf("_crashContextMessage after Set = %q, want %q", newMsg, testMsg)
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
	t.Skip("State() causes SIGTRAP on stopped VMs; use -integration for live VM tests")
}

func TestPrivateAPI_DiagnosticsSummary(t *testing.T) {
	t.Skip("_stateDescription causes SIGTRAP on stopped VMs; use -integration for live VM tests")
}
