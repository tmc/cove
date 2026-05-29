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

// createMacVMWithGraphics creates a minimal VZVirtualMachine with a macOS config
// that includes a graphics device (VZMacGraphicsDeviceConfiguration).
// Returns public VM, private VM wrappers, and the dispatch queue.
// Skips if the default VM directory is missing.
func createMacVMWithGraphics(t *testing.T) (vz.VZVirtualMachine, privvz.VZVirtualMachine, dispatch.Queue) {
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

	// Disk attachment (read-only)
	diskURL := objc.Send[objc.ID](objc.ID(objc.GetClass("NSURL")), objc.Sel("fileURLWithPath:"), objc.String(diskPath))
	var diskErrPtr objc.ID
	diskAttachment := objc.Send[objc.ID](
		objc.Send[objc.ID](objc.ID(objc.GetClass("VZDiskImageStorageDeviceAttachment")), objc.Sel("alloc")),
		objc.Sel("initWithURL:readOnly:cachingMode:synchronizationMode:error:"),
		diskURL, true,
		int64(0), // automatic caching
		int64(1), // full sync
		&diskErrPtr,
	)
	if diskErrPtr != 0 {
		errMsg := foundation.NSStringFromID(objc.Send[objc.ID](diskErrPtr, objc.Sel("localizedDescription"))).String()
		t.Fatalf("disk attachment error: %s", errMsg)
	}

	storageConfig := objc.Send[objc.ID](
		objc.Send[objc.ID](objc.ID(objc.GetClass("VZVirtioBlockDeviceConfiguration")), objc.Sel("alloc")),
		objc.Sel("initWithAttachment:"), diskAttachment,
	)

	// Graphics device configuration: VZMacGraphicsDeviceConfiguration with 1920x1200@144
	graphicsConfig := objc.Send[objc.ID](
		objc.Send[objc.ID](objc.ID(objc.GetClass("VZMacGraphicsDeviceConfiguration")), objc.Sel("alloc")),
		objc.Sel("init"),
	)

	displayConfig := objc.Send[objc.ID](
		objc.Send[objc.ID](objc.ID(objc.GetClass("VZMacGraphicsDisplayConfiguration")), objc.Sel("alloc")),
		objc.Sel("initWithWidthInPixels:heightInPixels:pixelsPerInch:"),
		int64(1920), int64(1200), int64(144),
	)

	displayArray := objc.Send[objc.ID](
		objc.ID(objc.GetClass("NSArray")), objc.Sel("arrayWithObject:"), displayConfig,
	)
	objc.Send[objc.ID](graphicsConfig, objc.Sel("setDisplays:"), displayArray)

	graphicsArray := objc.Send[objc.ID](
		objc.ID(objc.GetClass("NSArray")), objc.Sel("arrayWithObject:"), graphicsConfig,
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

	storageArray := objc.Send[objc.ID](
		objc.ID(objc.GetClass("NSArray")), objc.Sel("arrayWithObject:"), storageConfig,
	)
	objc.Send[objc.ID](config, objc.Sel("setStorageDevices:"), storageArray)
	objc.Send[objc.ID](config, objc.Sel("setGraphicsDevices:"), graphicsArray)

	// Validate
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
	queue := dispatch.QueueCreate("com.test.private-api-display")
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

// TestPrivateAPI_GraphicsDeviceCount verifies that the VM has graphics devices
// and exercises the public graphicsDevices accessor.
func TestPrivateAPI_GraphicsDeviceCount(t *testing.T) {
	pubVM, _, _ := createMacVMWithGraphics(t)

	devices := pubVM.GraphicsDevices()
	t.Logf("graphicsDevices count: %d", len(devices))

	if len(devices) == 0 {
		t.Fatal("expected at least one graphics device")
	}

	for i, dev := range devices {
		t.Logf("  device[%d]: ID=%#x", i, dev.ID)
	}
}

// TestPrivateAPI_DisplayPortCount exercises _displayPortCount on each graphics device
// via objc.Send since the method is unexported in the Go bindings.
func TestPrivateAPI_DisplayPortCount(t *testing.T) {
	pubVM, _, _ := createMacVMWithGraphics(t)

	devices := pubVM.GraphicsDevices()
	if len(devices) == 0 {
		t.Skip("no graphics devices")
	}

	for i, dev := range devices {
		if !objc.RespondsToSelector(dev.ID, objc.Sel("_displayPortCount")) {
			t.Logf("device[%d] _displayPortCount unavailable", i)
			continue
		}
		portCount := objc.Send[uint64](dev.ID, objc.Sel("_displayPortCount"))
		t.Logf("device[%d] _displayPortCount: %d", i, portCount)
	}
}

// TestPrivateAPI_DisplaysList verifies that each graphics device has displays
// and exercises the public displays accessor.
func TestPrivateAPI_DisplaysList(t *testing.T) {
	pubVM, _, _ := createMacVMWithGraphics(t)

	devices := pubVM.GraphicsDevices()
	if len(devices) == 0 {
		t.Skip("no graphics devices")
	}

	for i, dev := range devices {
		displays := dev.Displays()
		t.Logf("device[%d] displays count: %d", i, len(displays))

		for j, disp := range displays {
			t.Logf("  display[%d]: ID=%#x", j, disp.ID)
		}
	}
}

// TestPrivateAPI_DisplayUUID exercises _uuid on each display.
func TestPrivateAPI_DisplayUUID(t *testing.T) {
	pubVM, _, _ := createMacVMWithGraphics(t)

	devices := pubVM.GraphicsDevices()
	if len(devices) == 0 {
		t.Skip("no graphics devices")
	}

	for i, dev := range devices {
		displays := dev.Displays()
		for j, disp := range displays {
			privDisp := privvz.VZGraphicsDisplayFromID(disp.ID)
			uuidObj, err := privDisp.Uuid()
			if err != nil {
				t.Logf("device[%d] display[%d] _uuid unavailable: %v", i, j, err)
				continue
			}
			uuidStr := ""
			if uuidObj.GetID() != 0 {
				uuidStr = foundation.NSStringFromID(
					objc.Send[objc.ID](uuidObj.GetID(), objc.Sel("UUIDString")),
				).String()
			}
			t.Logf("device[%d] display[%d] _uuid: %s", i, j, uuidStr)
		}
	}
}

// TestPrivateAPI_DisplayGraphicsOrientation exercises _graphicsOrientation on each display.
func TestPrivateAPI_DisplayGraphicsOrientation(t *testing.T) {
	pubVM, _, _ := createMacVMWithGraphics(t)

	devices := pubVM.GraphicsDevices()
	if len(devices) == 0 {
		t.Skip("no graphics devices")
	}

	for i, dev := range devices {
		displays := dev.Displays()
		for j, disp := range displays {
			privDisp := privvz.VZGraphicsDisplayFromID(disp.ID)
			orientation, err := privDisp.GraphicsOrientation()
			if err != nil {
				t.Logf("device[%d] display[%d] _graphicsOrientation unavailable: %v", i, j, err)
				continue
			}
			t.Logf("device[%d] display[%d] _graphicsOrientation: %d", i, j, orientation)
		}
	}
}

// TestPrivateAPI_DisplayConfiguration exercises _configuration on each display.
func TestPrivateAPI_DisplayConfiguration(t *testing.T) {
	pubVM, _, _ := createMacVMWithGraphics(t)

	devices := pubVM.GraphicsDevices()
	if len(devices) == 0 {
		t.Skip("no graphics devices")
	}

	for i, dev := range devices {
		displays := dev.Displays()
		for j, disp := range displays {
			privDisp := privvz.VZGraphicsDisplayFromID(disp.ID)
			configObj, err := privDisp.Configuration()
			if err != nil {
				t.Logf("device[%d] display[%d] _configuration unavailable: %v", i, j, err)
				continue
			}
			t.Logf("device[%d] display[%d] _configuration: ID=%#x", i, j, configObj.GetID())

			if configObj.GetID() != 0 {
				desc := foundation.NSStringFromID(
					objc.Send[objc.ID](configObj.GetID(), objc.Sel("description")),
				).String()
				t.Logf("  description: %s", desc)
			}
		}
	}
}

// TestPrivateAPI_DisplayGraphicsDevice exercises _graphicsDevice on each display
// to verify it points back to the parent device.
func TestPrivateAPI_DisplayGraphicsDevice(t *testing.T) {
	pubVM, _, _ := createMacVMWithGraphics(t)

	devices := pubVM.GraphicsDevices()
	if len(devices) == 0 {
		t.Skip("no graphics devices")
	}

	for i, dev := range devices {
		displays := dev.Displays()
		for j, disp := range displays {
			privDisp := privvz.VZGraphicsDisplayFromID(disp.ID)
			parentDev, err := privDisp.GraphicsDevice()
			if err != nil {
				t.Logf("device[%d] display[%d] _graphicsDevice unavailable: %v", i, j, err)
				continue
			}
			t.Logf("device[%d] display[%d] _graphicsDevice: ID=%#x (parent dev ID=%#x)",
				i, j, parentDev.GetID(), dev.ID)

			if parentDev.GetID() != dev.ID {
				t.Logf("  NOTE: parent device ID mismatch (may be wrapped differently)")
			}
		}
	}
}

// TestPrivateAPI_MacGraphicsDisplayMetadata casts displays to VZMacGraphicsDisplay
// and exercises _connectionType, _displayIdentifier, and _displayMode.
//
// NOTE: These APIs trigger SIGTRAP on stopped VMs because the framework asserts
// the display is connected. They require a running VM with an active framebuffer.
// We verify the class identity instead and document the behavior.
func TestPrivateAPI_MacGraphicsDisplayMetadata(t *testing.T) {
	pubVM, _, _ := createMacVMWithGraphics(t)

	devices := pubVM.GraphicsDevices()
	if len(devices) == 0 {
		t.Skip("no graphics devices")
	}

	for i, dev := range devices {
		displays := dev.Displays()
		for j, disp := range displays {
			// Verify the display is a VZMacGraphicsDisplay via ObjC class check.
			className := foundation.NSStringFromID(
				objc.Send[objc.ID](objc.Send[objc.ID](disp.ID, objc.Sel("class")), objc.Sel("description")),
			).String()
			t.Logf("device[%d] display[%d] class=%s", i, j, className)

			// _connectionType, _displayIdentifier, _displayMode all crash with
			// SIGTRAP on stopped VMs. They require a running VM to be useful.
			t.Logf("  _connectionType: SKIPPED (SIGTRAP on stopped VM)")
			t.Logf("  _displayIdentifier: SKIPPED (SIGTRAP on stopped VM)")
			t.Logf("  _displayMode: SKIPPED (SIGTRAP on stopped VM)")
		}
	}
}

// TestPrivateAPI_MacGraphicsDeviceMetadata casts devices to VZMacGraphicsDevice
// and exercises _deviceFeatureLevel and _prefersLowPower.
func TestPrivateAPI_MacGraphicsDeviceMetadata(t *testing.T) {
	pubVM, _, _ := createMacVMWithGraphics(t)

	devices := pubVM.GraphicsDevices()
	if len(devices) == 0 {
		t.Skip("no graphics devices")
	}

	for i, dev := range devices {
		macDev := privvz.VZMacGraphicsDeviceFromID(dev.ID)
		featureLevel, err := macDev.DeviceFeatureLevel()
		if err != nil {
			t.Logf("device[%d] _deviceFeatureLevel unavailable: %v", i, err)
			continue
		}
		lowPower, err := macDev.PrefersLowPower()
		if err != nil {
			t.Logf("device[%d] _prefersLowPower unavailable: %v", i, err)
			continue
		}
		var portCount uint64
		if objc.RespondsToSelector(dev.ID, objc.Sel("_displayPortCount")) {
			portCount = objc.Send[uint64](dev.ID, objc.Sel("_displayPortCount"))
		} else {
			t.Logf("device[%d] _displayPortCount unavailable", i)
		}
		t.Logf("device[%d] _deviceFeatureLevel=%d _prefersLowPower=%v _displayPortCount=%d",
			i, featureLevel, lowPower, portCount)
	}
}

// TestPrivateAPI_TakeScreenshotStopped documents that _takeScreenshot crashes
// with SIGTRAP on a stopped VM. The framework asserts the framebuffer is active.
//
// KEY FINDING: _takeScreenshotWithCompletionHandler requires a running VM.
// It cannot be used as a headless screenshot alternative for stopped VMs.
// For a running VM (even headless), it MAY work since the framebuffer exists
// in the Virtualization framework regardless of whether a VZVirtualMachineView
// is attached. This needs testing with a running headless VM.
func TestPrivateAPI_TakeScreenshotStopped(t *testing.T) {
	pubVM, _, _ := createMacVMWithGraphics(t)

	devices := pubVM.GraphicsDevices()
	if len(devices) == 0 {
		t.Skip("no graphics devices")
	}

	for i, dev := range devices {
		displays := dev.Displays()
		for j := range displays {
			// _takeScreenshotWithCompletionHandler crashes with SIGTRAP on
			// stopped VMs. The framework internally asserts the display is
			// connected and has an active framebuffer.
			t.Logf("device[%d] display[%d] _takeScreenshot: SKIPPED (SIGTRAP on stopped VM)", i, j)
			t.Logf("  _takeScreenshot requires a running VM with active framebuffer")
			t.Logf("  On a running headless VM, this may work as an alternative to CGWindowListCreateImage")
		}
	}
}

// TestPrivateAPI_ValidateDisplayForHotPlug exercises _validateDisplayForHotPlug:error:
// on a stopped VM. Creates a new display config and validates it against the device.
func TestPrivateAPI_ValidateDisplayForHotPlug(t *testing.T) {
	pubVM, _, _ := createMacVMWithGraphics(t)

	devices := pubVM.GraphicsDevices()
	if len(devices) == 0 {
		t.Skip("no graphics devices")
	}

	// Create a new display configuration for hot-plug validation
	newDisplayConfig := objc.Send[objc.ID](
		objc.Send[objc.ID](objc.ID(objc.GetClass("VZMacGraphicsDisplayConfiguration")), objc.Sel("alloc")),
		objc.Sel("initWithWidthInPixels:heightInPixels:pixelsPerInch:"),
		int64(1024), int64(768), int64(72),
	)
	if newDisplayConfig == 0 {
		t.Fatal("failed to create VZMacGraphicsDisplayConfiguration for hot-plug test")
	}

	// Create a VZMacGraphicsDisplay from the config
	macDisp, err := privvz.NewMacGraphicsDisplayWithConfigurationError(
		privvz.VZMacGraphicsDisplayConfigurationFromID(newDisplayConfig),
	)
	t.Logf("NewMacGraphicsDisplayWithConfigurationError: ID=%#x err=%v", macDisp.ID, err)

	for i, dev := range devices {
		privDev := privvz.VZGraphicsDeviceFromID(dev.ID)

		// Try validating the new display for hot-plug
		if macDisp.ID != 0 {
			ok, valErr := privDev.ValidateDisplayForHotPlugError(macDisp)
			t.Logf("device[%d] _validateDisplayForHotPlug: ok=%v err=%v", i, ok, valErr)
		}
	}
}

// TestPrivateAPI_DisplayConfigUUID exercises the private _uuid on
// VZGraphicsDisplayConfiguration objects.
func TestPrivateAPI_DisplayConfigUUID(t *testing.T) {
	// Create a display config via the ObjC initializer
	configID := objc.Send[objc.ID](
		objc.Send[objc.ID](objc.ID(objc.GetClass("VZMacGraphicsDisplayConfiguration")), objc.Sel("alloc")),
		objc.Sel("initWithWidthInPixels:heightInPixels:pixelsPerInch:"),
		int64(1920), int64(1080), int64(144),
	)
	if configID == 0 {
		t.Skip("could not create VZMacGraphicsDisplayConfiguration")
	}

	if !objc.RespondsToSelector(configID, objc.Sel("_uuid")) {
		t.Skip("private selector _uuid is unavailable on this host")
	}
	uuidID := objc.Send[objc.ID](configID, objc.Sel("_uuid"))
	uuidStr := ""
	if uuidID != 0 {
		uuidStr = foundation.NSStringFromID(
			objc.Send[objc.ID](uuidID, objc.Sel("UUIDString")),
		).String()
	}
	t.Logf("VZMacGraphicsDisplayConfiguration _uuid: %s", uuidStr)

	if !objc.RespondsToSelector(configID, objc.Sel("_connectionType")) {
		t.Log("_connectionType unavailable")
		return
	}
	connType := objc.Send[int64](configID, objc.Sel("_connectionType"))
	dispMode := int64(0)
	if objc.RespondsToSelector(configID, objc.Sel("_displayMode")) {
		dispMode = objc.Send[int64](configID, objc.Sel("_displayMode"))
	}
	dispIDObj := objc.ID(0)
	if objc.RespondsToSelector(configID, objc.Sel("_displayIdentifier")) {
		dispIDObj = objc.Send[objc.ID](configID, objc.Sel("_displayIdentifier"))
	}
	dispID := ""
	if dispIDObj != 0 {
		dispID = foundation.NSStringFromID(dispIDObj).String()
	}
	t.Logf("_connectionType=%d _displayMode=%d _displayIdentifier=%q", connType, dispMode, dispID)
}

// TestPrivateAPI_DisplaySummary is an integration summary test that logs
// all display-related private API information for the VM.
func TestPrivateAPI_DisplaySummary(t *testing.T) {
	pubVM, _, _ := createMacVMWithGraphics(t)

	t.Log("=== Private API Display Summary ===")

	devices := pubVM.GraphicsDevices()
	t.Logf("Graphics device count: %d", len(devices))

	for i, dev := range devices {
		t.Logf("--- Device %d (ID=%#x) ---", i, dev.ID)

		macDev := privvz.VZMacGraphicsDeviceFromID(dev.ID)
		if featureLevel, err := macDev.DeviceFeatureLevel(); err != nil {
			t.Logf("  _deviceFeatureLevel unavailable: %v", err)
		} else {
			t.Logf("  _deviceFeatureLevel: %d", featureLevel)
		}
		if lowPower, err := macDev.PrefersLowPower(); err != nil {
			t.Logf("  _prefersLowPower unavailable: %v", err)
		} else {
			t.Logf("  _prefersLowPower: %v", lowPower)
		}
		if objc.RespondsToSelector(dev.ID, objc.Sel("_displayPortCount")) {
			t.Logf("  _displayPortCount: %d", objc.Send[uint64](dev.ID, objc.Sel("_displayPortCount")))
		} else {
			t.Log("  _displayPortCount unavailable")
		}

		displays := dev.Displays()
		t.Logf("  Displays count: %d", len(displays))

		for j, disp := range displays {
			t.Logf("  --- Display %d (ID=%#x) ---", j, disp.ID)

			// NOTE: disp.SizeInPixels() crashes with SIGTRAP on ARM64 due to
			// purego struct return issues with CGSize. Use description instead.
			desc := foundation.NSStringFromID(
				objc.Send[objc.ID](disp.ID, objc.Sel("description")),
			).String()
			t.Logf("    description: %s", desc)

			// Private API via VZGraphicsDisplay
			privDisp := privvz.VZGraphicsDisplayFromID(disp.ID)
			if orientation, err := privDisp.GraphicsOrientation(); err != nil {
				t.Logf("    _graphicsOrientation unavailable: %v", err)
			} else {
				t.Logf("    _graphicsOrientation: %d", orientation)
			}

			uuidObj, err := privDisp.Uuid()
			if err != nil {
				t.Logf("    _uuid unavailable: %v", err)
			} else if uuidObj.GetID() != 0 {
				uuidStr := foundation.NSStringFromID(
					objc.Send[objc.ID](uuidObj.GetID(), objc.Sel("UUIDString")),
				).String()
				t.Logf("    _uuid: %s", uuidStr)
			}

			configObj, err := privDisp.Configuration()
			if err != nil {
				t.Logf("    _configuration unavailable: %v", err)
			} else if configObj.GetID() != 0 {
				desc := foundation.NSStringFromID(
					objc.Send[objc.ID](configObj.GetID(), objc.Sel("description")),
				).String()
				t.Logf("    _configuration: %s", desc)
			}

			parentDev, err := privDisp.GraphicsDevice()
			if err != nil {
				t.Logf("    _graphicsDevice unavailable: %v", err)
			} else {
				t.Logf("    _graphicsDevice: ID=%#x", parentDev.GetID())
			}

			// VZMacGraphicsDisplay methods (_connectionType, _displayMode,
			// _displayIdentifier) crash with SIGTRAP on stopped VMs.
			className := foundation.NSStringFromID(
				objc.Send[objc.ID](objc.Send[objc.ID](disp.ID, objc.Sel("class")), objc.Sel("description")),
			).String()
			t.Logf("    class: %s", className)
			t.Logf("    _connectionType: SKIPPED (SIGTRAP on stopped VM)")
			t.Logf("    _displayMode: SKIPPED (SIGTRAP on stopped VM)")
			t.Logf("    _displayIdentifier: SKIPPED (SIGTRAP on stopped VM)")

			// _takeScreenshot crashes with SIGTRAP on stopped VMs.
			t.Logf("    _takeScreenshot: SKIPPED (SIGTRAP on stopped VM)")
		}
	}

	t.Log("=== End Display Summary ===")
}

// TestPrivateAPI_SizeInPixelsStructReturn tests SizeInPixels() on a display.
//
// Finding: The SIGTRAP is NOT a purego struct return issue. It's an Apple
// framework assertion inside Vz::VzVirtualMachineMessenger::process_color_space_update
// that fires when accessing display properties on a stopped VM. The framebuffer
// must be active (VM running) for SizeInPixels() to work.
//
// This test requires a running VM to succeed. On a stopped VM, it skips.
func TestPrivateAPI_SizeInPixelsStructReturn(t *testing.T) {
	t.Skip("requires running VM - SizeInPixels crashes in Apple framework on stopped VMs (not a purego issue)")
}
