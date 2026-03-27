package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/objectivec"
	pvz "github.com/tmc/apple/private/virtualization"
	vz "github.com/tmc/apple/virtualization"
)

// TestVZStorageDeviceClass verifies that the VZStorageDevice class is
// accessible through the private bindings.
func TestVZStorageDeviceClass(t *testing.T) {
	cls := pvz.GetVZStorageDeviceClass()
	if cls == (pvz.VZStorageDeviceClass{}) {
		t.Fatal("VZStorageDevice class is nil")
	}
	t.Log("VZStorageDevice class loaded successfully")
}

// TestVZXHCIControllerClass verifies that the VZXHCIController class
// is accessible.
func TestVZXHCIControllerClass(t *testing.T) {
	cls := pvz.GetVZXHCIControllerClass()
	if cls == (pvz.VZXHCIControllerClass{}) {
		t.Fatal("VZXHCIController class is nil")
	}
	t.Log("VZXHCIController class loaded successfully")
}

// TestVZXHCIControllerConfigurationClass verifies the XHCI config class.
func TestVZXHCIControllerConfigurationClass(t *testing.T) {
	cls := pvz.GetVZXHCIControllerConfigurationClass()
	if cls == (pvz.VZXHCIControllerConfigurationClass{}) {
		t.Fatal("VZXHCIControllerConfiguration class is nil")
	}
	cfg := cls.Alloc().Init()
	if cfg.ID == 0 {
		t.Fatal("failed to alloc+init VZXHCIControllerConfiguration")
	}
	t.Logf("VZXHCIControllerConfiguration: %v", cfg.ID)
}

// TestVZUSBControllerClass verifies the base USB controller class and
// its methods for passthrough device management.
func TestVZUSBControllerClass(t *testing.T) {
	cls := pvz.GetVZUSBControllerClass()
	if cls == (pvz.VZUSBControllerClass{}) {
		t.Fatal("VZUSBController class is nil")
	}
	t.Log("VZUSBController class loaded successfully")
}

// TestVZUSBMassStorageDeviceClass verifies the USB mass storage device class.
func TestVZUSBMassStorageDeviceClass(t *testing.T) {
	cls := pvz.GetVZUSBMassStorageDeviceClass()
	if cls == (pvz.VZUSBMassStorageDeviceClass{}) {
		t.Fatal("VZUSBMassStorageDevice class is nil")
	}
	t.Log("VZUSBMassStorageDevice class loaded successfully")
}

// TestVZUSBMassStorageDeviceConfigurationClass verifies the config class.
func TestVZUSBMassStorageDeviceConfigurationClass(t *testing.T) {
	cls := pvz.GetVZUSBMassStorageDeviceConfigurationClass()
	if cls == (pvz.VZUSBMassStorageDeviceConfigurationClass{}) {
		t.Fatal("VZUSBMassStorageDeviceConfiguration class is nil")
	}
	t.Log("VZUSBMassStorageDeviceConfiguration class loaded successfully")
}

// TestVZStorageDeviceAttachmentClass verifies the storage attachment class.
func TestVZStorageDeviceAttachmentClass(t *testing.T) {
	cls := pvz.GetVZStorageDeviceAttachmentClass()
	if cls == (pvz.VZStorageDeviceAttachmentClass{}) {
		t.Fatal("VZStorageDeviceAttachment class is nil")
	}
	t.Log("VZStorageDeviceAttachment class loaded successfully")
}

// TestDiskImageAttachmentCreation creates a disk image attachment using the
// public API and verifies it can be accessed through the private type system.
func TestDiskImageAttachmentCreation(t *testing.T) {
	imgPath := createTempDiskImage(t, 1*1024*1024)
	url := foundation.NewURLFileURLWithPath(imgPath)
	if url.ID == 0 {
		t.Fatal("failed to create file URL")
	}
	url.Retain()

	attachment, err := vz.NewDiskImageStorageDeviceAttachmentWithURLReadOnlyError(url, true)
	if err != nil {
		t.Fatalf("create disk attachment: %v", err)
	}
	attachment.Retain()

	if attachment.ID == 0 {
		t.Fatal("disk attachment ID is nil")
	}
	t.Logf("created disk image attachment: ID=%v readOnly=%v", attachment.ID, attachment.ReadOnly())

	// Wrap as private type to verify interop
	pvzAttachment := pvz.VZDiskImageStorageDeviceAttachmentFromID(attachment.ID)
	if pvzAttachment.ID == 0 {
		t.Fatal("private type wrapping failed")
	}
	t.Logf("private attachment Description: %s", pvzAttachment.Description())
}

// TestDiskImageAttachmentUpdateDiskSize exercises the private _updateDiskSize method.
func TestDiskImageAttachmentUpdateDiskSize(t *testing.T) {
	imgPath := createTempDiskImage(t, 1*1024*1024)
	url := foundation.NewURLFileURLWithPath(imgPath)
	url.Retain()

	attachment, err := vz.NewDiskImageStorageDeviceAttachmentWithURLReadOnlyError(url, false)
	if err != nil {
		t.Fatalf("create disk attachment: %v", err)
	}
	attachment.Retain()

	pvzAttachment := pvz.VZDiskImageStorageDeviceAttachmentFromID(attachment.ID)

	// UpdateDiskSize is a private API that resizes the backing disk
	// Without an actual VM this may not have visible effects but should not crash
	pvzAttachment.UpdateDiskSize(2 * 1024 * 1024)
	t.Log("UpdateDiskSize(2MB) called without crash")
}

// TestUSBMassStorageDeviceCreation creates a USB mass storage config using
// the public API and verifies the private type interop.
func TestUSBMassStorageDeviceCreation(t *testing.T) {
	imgPath := createTempDiskImage(t, 1*1024*1024)
	url := foundation.NewURLFileURLWithPath(imgPath)
	url.Retain()

	attachment, err := vz.NewDiskImageStorageDeviceAttachmentWithURLReadOnlyError(url, true)
	if err != nil {
		t.Fatalf("create disk attachment: %v", err)
	}
	attachment.Retain()

	usbConfig := vz.NewUSBMassStorageDeviceConfigurationWithAttachment(&attachment.VZStorageDeviceAttachment)
	if usbConfig.ID == 0 {
		t.Fatal("USB mass storage config is nil")
	}
	usbConfig.Retain()

	// Wrap as private type
	pvzUSBConfig := pvz.VZUSBMassStorageDeviceConfigurationFromID(usbConfig.ID)
	if pvzUSBConfig.ID == 0 {
		t.Fatal("private USB config wrapping failed")
	}
	t.Logf("USB mass storage config: ID=%v Description=%s", pvzUSBConfig.ID, pvzUSBConfig.Description())
}

// TestStorageDeviceInitWithAttachment tests creating a VZStorageDevice
// directly via the private _initWithAttachment method.
func TestStorageDeviceInitWithAttachment(t *testing.T) {
	imgPath := createTempDiskImage(t, 1*1024*1024)
	url := foundation.NewURLFileURLWithPath(imgPath)
	url.Retain()

	attachment, err := vz.NewDiskImageStorageDeviceAttachmentWithURLReadOnlyError(url, true)
	if err != nil {
		t.Fatalf("create disk attachment: %v", err)
	}
	attachment.Retain()

	pvzAttachment := pvz.VZStorageDeviceAttachmentFromID(attachment.ID)

	storageDevice := pvz.GetVZStorageDeviceClass().Alloc()
	result := storageDevice.InitWithAttachment(pvzAttachment)
	if result == nil {
		t.Log("InitWithAttachment returned nil (may require VM context)")
	} else {
		t.Logf("InitWithAttachment returned: ID=%v", result.GetID())
	}
}

// TestStorageDeviceAttachmentProperty tests the private _attachment getter.
func TestStorageDeviceAttachmentProperty(t *testing.T) {
	imgPath := createTempDiskImage(t, 1*1024*1024)
	url := foundation.NewURLFileURLWithPath(imgPath)
	url.Retain()

	attachment, err := vz.NewDiskImageStorageDeviceAttachmentWithURLReadOnlyError(url, true)
	if err != nil {
		t.Fatalf("create disk attachment: %v", err)
	}
	attachment.Retain()

	pvzAttachment := pvz.VZStorageDeviceAttachmentFromID(attachment.ID)

	storageDevice := pvz.GetVZStorageDeviceClass().Alloc()
	result := storageDevice.InitWithAttachment(pvzAttachment)
	if result == nil || result.GetID() == 0 {
		t.Skip("InitWithAttachment returned nil; skipping Attachment() test")
	}

	device := pvz.VZStorageDeviceFromID(result.GetID())
	got := device.Attachment()
	if got == nil || got.GetID() == 0 {
		t.Log("Attachment() returned nil (may require VM association)")
	} else {
		t.Logf("Attachment() returned: ID=%v", got.GetID())
	}
}

// TestStorageDeviceSetAttachmentAsync exercises the async hot-swap API
// _setAttachment:completionHandler: without a running VM.
// Framework crashes (SIGSEGV) when calling completion handler APIs on devices
// not attached to a running VM. Requires -integration for live VM testing.
func TestStorageDeviceSetAttachmentAsync(t *testing.T) {
	t.Skip("completion handler on storage device without VM causes SIGSEGV — requires running VM")
	imgPath := createTempDiskImage(t, 1*1024*1024)
	url := foundation.NewURLFileURLWithPath(imgPath)
	url.Retain()

	attachment, err := vz.NewDiskImageStorageDeviceAttachmentWithURLReadOnlyError(url, false)
	if err != nil {
		t.Fatalf("create disk attachment: %v", err)
	}
	attachment.Retain()

	pvzAttachment := pvz.VZStorageDeviceAttachmentFromID(attachment.ID)

	device := pvz.GetVZStorageDeviceClass().Alloc()
	result := device.InitWithAttachment(pvzAttachment)
	if result == nil || result.GetID() == 0 {
		t.Skip("cannot create storage device without VM context")
	}

	storageDevice := pvz.VZStorageDeviceFromID(result.GetID())

	// Create a second attachment for the hot-swap target
	imgPath2 := createTempDiskImage(t, 2*1024*1024)
	url2 := foundation.NewURLFileURLWithPath(imgPath2)
	url2.Retain()

	attachment2, err := vz.NewDiskImageStorageDeviceAttachmentWithURLReadOnlyError(url2, false)
	if err != nil {
		t.Fatalf("create second disk attachment: %v", err)
	}
	attachment2.Retain()

	pvzAttachment2 := pvz.VZStorageDeviceAttachmentFromID(attachment2.ID)

	type hotswapResult struct {
		ok  bool
		err error
	}
	done := make(chan hotswapResult, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	storageDevice.SetAttachmentCompletionHandler(pvzAttachment2, func(err error) {
		done <- hotswapResult{err == nil, err}
	})

	select {
	case r := <-done:
		if r.err != nil {
			t.Logf("SetAttachmentCompletionHandler error (expected without VM): %v", r.err)
		} else {
			t.Logf("SetAttachmentCompletionHandler succeeded: ok=%v", r.ok)
		}
	case <-ctx.Done():
		t.Logf("SetAttachmentCompletionHandler timed out (expected without VM)")
	}
}

// TestStorageDeviceSetVirtualMachine tests associating a storage device
// with a virtual machine (nil case).
func TestStorageDeviceSetVirtualMachine(t *testing.T) {
	imgPath := createTempDiskImage(t, 1*1024*1024)
	url := foundation.NewURLFileURLWithPath(imgPath)
	url.Retain()

	attachment, err := vz.NewDiskImageStorageDeviceAttachmentWithURLReadOnlyError(url, true)
	if err != nil {
		t.Fatalf("create disk attachment: %v", err)
	}
	attachment.Retain()

	pvzAttachment := pvz.VZStorageDeviceAttachmentFromID(attachment.ID)

	device := pvz.GetVZStorageDeviceClass().Alloc()
	result := device.InitWithAttachment(pvzAttachment)
	if result == nil || result.GetID() == 0 {
		t.Skip("cannot create storage device without VM context")
	}

	storageDevice := pvz.VZStorageDeviceFromID(result.GetID())

	// Setting VM to nil (zero object) should be safe
	storageDevice.SetVirtualMachine(objectivec.Object{ID: 0})
	t.Log("SetVirtualMachine(nil) called without crash")
}

// TestXHCIControllerAttachDetachWithoutVM exercises the XHCI attach/detach
// APIs. Without a running VM the framework crashes (SIGSEGV).
func TestXHCIControllerAttachDetachWithoutVM(t *testing.T) {
	t.Skip("XHCI controller attach/detach without VM causes SIGSEGV — requires running VM")
	imgPath := createTempDiskImage(t, 1*1024*1024)
	url := foundation.NewURLFileURLWithPath(imgPath)
	url.Retain()

	attachment, err := vz.NewDiskImageStorageDeviceAttachmentWithURLReadOnlyError(url, true)
	if err != nil {
		t.Fatalf("create disk attachment: %v", err)
	}
	attachment.Retain()

	usbConfig := vz.NewUSBMassStorageDeviceConfigurationWithAttachment(&attachment.VZStorageDeviceAttachment)
	if usbConfig.ID == 0 {
		t.Fatal("USB config is nil")
	}
	usbConfig.Retain()

	pvzUSBConfig := pvz.VZUSBMassStorageDeviceConfigurationFromID(usbConfig.ID)

	// Create an XHCI controller (without VM context it may not fully work)
	controller := pvz.GetVZXHCIControllerClass().Alloc().Init()
	if controller.ID == 0 {
		t.Skip("cannot create XHCI controller without VM")
	}

	// MakeUSBDeviceWithVirtualMachine requires a VM; pass nil to test the API shape
	usbDevice := pvzUSBConfig.MakeUSBDeviceWithVirtualMachine(objectivec.Object{ID: 0})
	if usbDevice == nil || usbDevice.GetID() == 0 {
		t.Log("MakeUSBDeviceWithVirtualMachine(nil) returned nil (expected)")
		t.Skip("cannot test attach/detach without a real USB device")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = controller.AttachDevice(ctx, usbDevice)
	if err != nil {
		t.Logf("AttachDevice error (expected without VM): %v", err)
	} else {
		t.Log("AttachDevice: success")

		// Only try detach if attach succeeded
		err2 := controller.DetachDevice(ctx, usbDevice)
		if err2 != nil {
			t.Logf("DetachDevice error: %v", err2)
		} else {
			t.Log("DetachDevice: success")
		}
	}
}

// TestUSBMassStorageDeviceProperties checks properties on USB mass storage
// devices created via the private type system.
func TestUSBMassStorageDeviceProperties(t *testing.T) {
	imgPath := createTempDiskImage(t, 1*1024*1024)
	url := foundation.NewURLFileURLWithPath(imgPath)
	url.Retain()

	attachment, err := vz.NewDiskImageStorageDeviceAttachmentWithURLReadOnlyError(url, true)
	if err != nil {
		t.Fatalf("create disk attachment: %v", err)
	}
	attachment.Retain()

	usbConfig := vz.NewUSBMassStorageDeviceConfigurationWithAttachment(&attachment.VZStorageDeviceAttachment)
	usbConfig.Retain()

	pvzUSBConfig := pvz.VZUSBMassStorageDeviceConfigurationFromID(usbConfig.ID)

	t.Logf("USB mass storage config Description: %s", pvzUSBConfig.Description())
	t.Logf("USB mass storage config DebugDescription: %s", pvzUSBConfig.DebugDescription())
	t.Logf("USB mass storage config Hash: %d", pvzUSBConfig.Hash())

	dup := pvzUSBConfig.IsDuplicateConfiguration(pvzUSBConfig)
	t.Logf("IsDuplicateConfiguration(self): %v", dup)
}

// TestStorageDeviceConfigSetAttachment exercises the private _setAttachment
// method on VZStorageDeviceConfiguration (config-level, not device-level).
func TestStorageDeviceConfigSetAttachment(t *testing.T) {
	imgPath := createTempDiskImage(t, 1*1024*1024)
	url := foundation.NewURLFileURLWithPath(imgPath)
	url.Retain()

	attachment1, err := vz.NewDiskImageStorageDeviceAttachmentWithURLReadOnlyError(url, true)
	if err != nil {
		t.Fatalf("create attachment: %v", err)
	}
	attachment1.Retain()

	usbConfig := vz.NewUSBMassStorageDeviceConfigurationWithAttachment(&attachment1.VZStorageDeviceAttachment)
	usbConfig.Retain()

	pvzConfig := pvz.VZStorageDeviceConfigurationFromID(usbConfig.ID)
	t.Logf("initial config: %s", pvzConfig.Description())

	// Create second attachment and swap it in via private API
	imgPath2 := createTempDiskImage(t, 2*1024*1024)
	url2 := foundation.NewURLFileURLWithPath(imgPath2)
	url2.Retain()

	attachment2, err := vz.NewDiskImageStorageDeviceAttachmentWithURLReadOnlyError(url2, false)
	if err != nil {
		t.Fatalf("create second attachment: %v", err)
	}
	attachment2.Retain()

	pvzAttachment2 := pvz.VZStorageDeviceAttachmentFromID(attachment2.ID)

	// Use the private _setAttachment to swap the backing store at config level
	pvzConfig.SetAttachment(pvzAttachment2)
	t.Log("VZStorageDeviceConfiguration.SetAttachment called successfully")
	t.Logf("config after swap: %s", pvzConfig.Description())
}

// TestVZDiskImageStorageDeviceAttachmentDiskImageClass exercises the private
// class method _diskImageStorageDeviceAttachmentWithDiskImage.
func TestVZDiskImageStorageDeviceAttachmentDiskImageClass(t *testing.T) {
	cls := pvz.GetVZDiskImageStorageDeviceAttachmentClass()
	if cls == (pvz.VZDiskImageStorageDeviceAttachmentClass{}) {
		t.Fatal("VZDiskImageStorageDeviceAttachment class is nil")
	}

	// Passing nil for the disk image triggers an ObjC exception (NSInvalidArgumentException).
	// Just verify the class method selector exists.
	responds := objc.Send[bool](objc.ID(cls.Class()), objc.Sel("respondsToSelector:"),
		objc.Sel("_diskImageStorageDeviceAttachmentWithDiskImage:"))
	t.Logf("responds to _diskImageStorageDeviceAttachmentWithDiskImage: %v", responds)
}

// TestStorageDeviceAPIInventory documents the private API surface for
// storage device hot-swap capabilities.
func TestStorageDeviceAPIInventory(t *testing.T) {
	t.Log("=== VZStorageDevice Private API Surface ===")
	t.Log("")
	t.Log("VZStorageDevice._attachment()")
	t.Log("  - Returns the current storage device attachment")
	t.Log("  - Can be used to inspect what disk image is currently backing a device")
	t.Log("")
	t.Log("VZStorageDevice._initWithAttachment(attachment)")
	t.Log("  - Initialize a storage device with an attachment")
	t.Log("")
	t.Log("VZStorageDevice._initWithVirtualMachine:attachment:(machine, attachment)")
	t.Log("  - Initialize with VM association")
	t.Log("")
	t.Log("VZStorageDevice._initWithVirtualMachine:storageDeviceIndex:attachment:(machine, index, attachment)")
	t.Log("  - Initialize with VM and device index")
	t.Log("")
	t.Log("VZStorageDevice._setAttachment:completionHandler:(attachment, handler)")
	t.Log("  - HOT-SWAP: Asynchronously replace the storage backing of a running device")
	t.Log("  - This is the key API for live disk image swapping")
	t.Log("  - Completion handler reports success/failure")
	t.Log("")
	t.Log("VZStorageDevice._setVirtualMachine(machine)")
	t.Log("  - Associate/disassociate a device with a VM")
	t.Log("")
	t.Log("VZStorageDeviceConfiguration._setAttachment(attachment)")
	t.Log("  - Change the attachment on a configuration (pre-boot)")
	t.Log("")
	t.Log("VZDiskImageStorageDeviceAttachment._updateDiskSize(size)")
	t.Log("  - Resize the backing disk image")
	t.Log("")
	t.Log("=== VZXHCIController API Surface ===")
	t.Log("")
	t.Log("VZXHCIController.attachDevice:completionHandler:(device, handler)")
	t.Log("  - HOT-PLUG: Dynamically attach a USB device to a running VM")
	t.Log("  - Public API (not private)")
	t.Log("")
	t.Log("VZXHCIController.detachDevice:completionHandler:(device, handler)")
	t.Log("  - HOT-UNPLUG: Dynamically detach a USB device from a running VM")
	t.Log("  - Public API (not private)")
	t.Log("")
	t.Log("=== VZUSBController Private API Surface ===")
	t.Log("")
	t.Log("VZUSBController._capturePassthroughDevicesWithCompletionHandler(handler)")
	t.Log("  - Capture host USB devices for passthrough to guest")
	t.Log("")
	t.Log("VZUSBController._releasePassthroughDevices()")
	t.Log("  - Release previously captured passthrough devices")
	t.Log("")
	t.Log("VZUSBController.delegate / setDelegate")
	t.Log("  - Delegate for USB controller events")
	t.Log("")
	t.Log("=== Summary ===")
	t.Log("")
	t.Log("Hot-swap disk images:  YES via VZStorageDevice._setAttachment:completionHandler:")
	t.Log("Hot-plug USB devices:  YES via VZXHCIController.attachDevice:completionHandler:")
	t.Log("Hot-unplug USB:        YES via VZXHCIController.detachDevice:completionHandler:")
	t.Log("USB passthrough:       YES via VZUSBController._capturePassthroughDevices")
	t.Log("Live disk resize:      YES via VZDiskImageStorageDeviceAttachment._updateDiskSize")
}

// TestUSBMassStorageDeviceInheritance verifies the type hierarchy:
// VZUSBMassStorageDevice -> VZStorageDevice -> objectivec.Object
func TestUSBMassStorageDeviceInheritance(t *testing.T) {
	// Verify class responds to expected selectors via ObjC runtime
	cls := objc.GetClass("VZUSBMassStorageDevice")
	if cls == 0 {
		t.Fatal("VZUSBMassStorageDevice ObjC class not found")
	}

	superCls := objc.Send[objc.ID](objc.ID(cls), objc.Sel("superclass"))
	t.Logf("VZUSBMassStorageDevice superclass: %v", superCls)

	// VZStorageDevice should be in the chain
	storageCls := objc.GetClass("VZStorageDevice")
	if storageCls == 0 {
		t.Fatal("VZStorageDevice ObjC class not found")
	}
	t.Logf("VZStorageDevice class: %v", storageCls)

	// VZXHCIController -> VZUSBController
	xhciCls := objc.GetClass("VZXHCIController")
	if xhciCls == 0 {
		t.Fatal("VZXHCIController ObjC class not found")
	}
	xhciSuper := objc.Send[objc.ID](objc.ID(xhciCls), objc.Sel("superclass"))
	t.Logf("VZXHCIController superclass: %v", xhciSuper)

	usbCtrlCls := objc.GetClass("VZUSBController")
	if usbCtrlCls == 0 {
		t.Fatal("VZUSBController ObjC class not found")
	}
	t.Logf("VZUSBController class: %v", usbCtrlCls)
}

// TestStorageDeviceConfigMakeDevice tests MakeStorageDeviceForVirtualMachine.
func TestStorageDeviceConfigMakeDevice(t *testing.T) {
	t.Skip("framework crashes (SIGSEGV) when calling MakeStorageDevice with nil VM")
	imgPath := createTempDiskImage(t, 1*1024*1024)
	url := foundation.NewURLFileURLWithPath(imgPath)
	url.Retain()

	attachment, err := vz.NewDiskImageStorageDeviceAttachmentWithURLReadOnlyError(url, true)
	if err != nil {
		t.Fatalf("create attachment: %v", err)
	}
	attachment.Retain()

	usbConfig := vz.NewUSBMassStorageDeviceConfigurationWithAttachment(&attachment.VZStorageDeviceAttachment)
	usbConfig.Retain()

	pvzConfig := pvz.VZStorageDeviceConfigurationFromID(usbConfig.ID)

	// Without a real VM, pass nil
	result := pvzConfig.MakeStorageDeviceForVirtualMachineStorageDeviceIndex(
		objectivec.Object{ID: 0}, 0,
	)
	if result == nil || result.GetID() == 0 {
		t.Log("MakeStorageDeviceForVirtualMachine(nil, 0) returned nil (expected without VM)")
	} else {
		t.Logf("MakeStorageDeviceForVirtualMachine returned ID=%v", result.GetID())
	}
}

// createTempDiskImage creates a sparse disk image file for testing.
func createTempDiskImage(t *testing.T, size int64) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, fmt.Sprintf("test-%d.img", size))
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create temp disk image: %v", err)
	}
	if err := f.Truncate(size); err != nil {
		f.Close()
		t.Fatalf("truncate disk image: %v", err)
	}
	f.Close()
	return path
}
