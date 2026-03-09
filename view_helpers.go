package main

import (
	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/objc"
	vz "github.com/tmc/apple/virtualization"
)

// vmViewAsNSView casts a VZVirtualMachineView to appkit.NSView.
// VZVirtualMachineView inherits from NSView in Objective-C, but the generated
// Go bindings embed objectivec.Object instead of appkit.NSView.
func vmViewAsNSView(v vz.VZVirtualMachineView) appkit.NSView {
	return appkit.NSViewFromID(v.ID)
}

// getSharedApp returns the shared NSApplication instance.
func getSharedApp() appkit.NSApplication {
	nsAppID := objc.Send[objc.ID](
		objc.ID(objc.GetClass("NSApplication")),
		objc.Sel("sharedApplication"),
	)
	return appkit.NSApplicationFromID(nsAppID)
}
