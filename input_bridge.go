// input_bridge.go - Pointer/keyboard input dispatch sub-component of ControlServer.
//
// inputBridge owns the mouse and keyboard delivery paths used by the
// control socket: the direct VM input path
// (sendPointerNSEvent:pointingDeviceIndex: / _VZKeyboard.sendKeyEvents:),
// the AppKit NSEvent path delivered through VZVirtualMachineView, and
// the Quartz CGEvent fallback. The bridge does not own any view state;
// the cached vmView, window, windowNum and viewContentHeight live on
// ControlServer because screen capture (slice 6.2) and lifecycle
// (slice 6d) read the same fields.
//
// Per design 039 §7 (facade-late rule), the bridge stays in package
// main until all five ControlServer sub-slices have been extracted.
package main

// inputBridge holds a back-reference to its parent ControlServer. The
// zero value is unusable; ControlServer wires cs in NewControlServer.
//
// Two invariants must survive every refactor of this file:
//
//  1. Mouse Y mapping must use the cached viewContentHeight (the VM
//     content area, e.g. 768px) rather than the NSView bounds height
//     (which includes the 32px title bar). OCR coordinates are taken
//     from the cropped capture, so flipping against the bounds height
//     would push every click 32 pixels off.
//
//  2. Keyboard input must travel through CGEventCreateKeyboardEvent →
//     +[NSEvent eventWithCGEvent:] → keyDown:/keyUp: on the
//     VZVirtualMachineView. purego's objc.Send on ARM64 corrupts uint16
//     parameters past argument position 8 (NSEvent
//     keyEventWithType:...keyCode: places keyCode at position 10), so
//     the CGEvent → NSEvent path is the only one that delivers a
//     non-zero keyCode to the guest.
type inputBridge struct {
	cs *ControlServer
}
