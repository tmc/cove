package main

import (
	"unsafe"

	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/objectivec"
	pvz "github.com/tmc/apple/private/virtualization"
)

// These probe private selectors whose generated wrappers were dropped from
// the bindings. The selectors themselves still exist at runtime, so the tests
// exercise them directly, preserving the old "unavailable selector -> error"
// contract that unavailablePrivateSelector checks for.

func createViewEndpointWithOptions(vm pvz.VZVirtualMachine, options uint64) (objectivec.IObject, error) {
	const sel = "_createViewEndpointWithOptions:"
	if !objc.RespondsToSelector(vm.ID, objc.Sel(sel)) {
		return nil, &objc.UnrecognizedSelectorError{Selector: sel}
	}
	rv := objc.SendIfResponds[objc.ID](vm.ID, objc.Sel(sel), options)
	return objectivec.Object{ID: rv}, nil
}

func isDuplicateUSBDeviceConfiguration(config pvz.VZVirtualMachineConfiguration, at, index uint64) (bool, error) {
	const sel = "_isDuplicateUSBDeviceConfigurationAt:usbDeviceIndex:"
	if !objc.RespondsToSelector(config.ID, objc.Sel(sel)) {
		return false, &objc.UnrecognizedSelectorError{Selector: sel}
	}
	return objc.SendIfResponds[bool](config.ID, objc.Sel(sel), at, index), nil
}

func newMacGraphicsDisplay(configuration objectivec.IObject) (pvz.VZMacGraphicsDisplay, error) {
	const sel = "initWithConfiguration:error:"
	class := objc.ID(objc.GetClass("VZMacGraphicsDisplay"))
	instance := objc.SendIfResponds[objc.ID](class, objc.Sel("alloc"))
	if instance == 0 {
		return pvz.VZMacGraphicsDisplay{}, objc.ErrInitFailed
	}
	if !objc.RespondsToSelector(instance, objc.Sel(sel)) {
		objc.SendIfResponds[objc.ID](instance, objc.Sel("release"))
		return pvz.VZMacGraphicsDisplay{}, &objc.UnrecognizedSelectorError{Selector: sel}
	}
	var errorID objc.ID
	display := objc.SendIfResponds[objc.ID](instance, objc.Sel(sel), configuration, unsafe.Pointer(&errorID))
	if errorID != 0 {
		objc.Send[objc.ID](errorID, objc.Sel("retain"))
		return pvz.VZMacGraphicsDisplay{}, foundation.NSErrorFrom(errorID)
	}
	if display == 0 {
		return pvz.VZMacGraphicsDisplay{}, objc.ErrInitFailed
	}
	return pvz.VZMacGraphicsDisplayFromID(display), nil
}
