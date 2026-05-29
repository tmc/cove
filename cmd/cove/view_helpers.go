package main

import (
	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/objc"
	vz "github.com/tmc/apple/virtualization"

	"github.com/tmc/cove/internal/controlserver"
)

// vmViewAsNSView casts a VZVirtualMachineView to appkit.NSView.
// Thin forwarder; the canonical implementation lives in the
// controlserver package alongside the input bridge.
func vmViewAsNSView(v vz.VZVirtualMachineView) appkit.NSView {
	return controlserver.VMViewAsNSView(v)
}

// getSharedApp returns the shared NSApplication instance.
func getSharedApp() appkit.NSApplication {
	nsAppID := objc.Send[objc.ID](
		objc.ID(objc.GetClass("NSApplication")),
		objc.Sel("sharedApplication"),
	)
	return appkit.NSApplicationFromID(nsAppID)
}
