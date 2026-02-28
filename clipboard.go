// clipboard.go - Host↔guest clipboard sharing via SPICE agent.
//
// Uses VZSpiceAgentPortAttachment on a VZVirtioConsoleDeviceConfiguration
// port to enable bidirectional clipboard sync (text + images).
// Requires macOS 13+ on the host and spice-vdagent in the guest.
package main

import (
	"fmt"

	"github.com/tmc/apple/objc"
	vz "github.com/tmc/apple/virtualization"
)

// createClipboardConfig creates a Virtio console device with SPICE agent
// clipboard sharing enabled. Returns a zero-value config if creation fails.
func createClipboardConfig() vz.VZVirtioConsoleDeviceConfiguration {
	// Create SPICE agent attachment with clipboard sharing.
	spiceAgent := vz.NewVZSpiceAgentPortAttachment()
	if spiceAgent.ID == 0 {
		fmt.Println("  Warning: could not create SPICE agent port attachment")
		return vz.VZVirtioConsoleDeviceConfiguration{}
	}
	spiceAgent.SetSharesClipboard(true)

	// Get the well-known port name (e.g., "com.redhat.spice.0").
	portName := vz.GetVZSpiceAgentPortAttachmentClass().SpiceAgentPortName()

	// Create a Virtio console port for the SPICE agent.
	spicePort := vz.NewVZVirtioConsolePortConfiguration()
	if spicePort.ID == 0 {
		fmt.Println("  Warning: could not create console port configuration")
		return vz.VZVirtioConsoleDeviceConfiguration{}
	}
	spicePort.SetName(portName)
	spicePort.SetAttachment(&spiceAgent.VZSerialPortAttachment)

	// Create the console device and assign the port at index 0.
	consoleDevice := vz.NewVZVirtioConsoleDeviceConfiguration()
	if consoleDevice.ID == 0 {
		fmt.Println("  Warning: could not create console device configuration")
		return vz.VZVirtioConsoleDeviceConfiguration{}
	}

	// The ports array uses indexed subscript assignment.
	// Generated bindings have the getter but not the setter,
	// so we use raw objc.Send for setObject:atIndexedSubscript:.
	ports := consoleDevice.Ports()
	objc.Send[struct{}](ports.GetID(), objc.Sel("setObject:atIndexedSubscript:"), spicePort.ID, uint(0))

	return consoleDevice
}
