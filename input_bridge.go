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

import (
	"fmt"
	"time"

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/objc"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

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

// sendMouse sends a mouse event to the VM. Uses either the direct VM
// input path or the host window-server CGEvent path, depending on the
// configured automation backend.
func (b *inputBridge) sendMouse(cmd *controlpb.MouseCommand) *controlpb.ControlResponse {
	s := b.cs
	if s.vmView.ID == 0 {
		return &controlpb.ControlResponse{Error: "mouse input requires GUI mode (run with -gui)"}
	}
	if s.window.ID == 0 {
		return &controlpb.ControlResponse{Error: "mouse input requires GUI mode (run with -gui)"}
	}

	// Handle click as move+down+up sequence.
	if cmd.Action == "click" {
		moveCmd := &controlpb.MouseCommand{
			X: cmd.X, Y: cmd.Y, Button: cmd.Button, Action: "move", Absolute: cmd.Absolute,
		}
		moveResp := b.sendMouse(moveCmd)
		if !moveResp.Success {
			return moveResp
		}
		time.Sleep(20 * time.Millisecond)

		downCmd := &controlpb.MouseCommand{
			X: cmd.X, Y: cmd.Y, Button: cmd.Button, Action: "down", Absolute: cmd.Absolute,
		}
		downResp := b.sendMouse(downCmd)
		if !downResp.Success {
			return downResp
		}
		time.Sleep(50 * time.Millisecond)
		upCmd := &controlpb.MouseCommand{
			X: cmd.X, Y: cmd.Y, Button: cmd.Button, Action: "up", Absolute: cmd.Absolute,
		}
		return b.sendMouse(upCmd)
	}

	switch s.inputBackend() {
	case automationBackendWindow:
		if cmd.Absolute && s.captureBackend() == automationBackendWindow {
			return b.sendMouseVMDirect(cmd)
		}
		return b.sendMouseCGEvent(cmd)
	case automationBackendFramebuffer:
		return b.sendMouseVMDirect(cmd)
	}

	// Try the direct VM input path first (sendPointerNSEvent:pointingDeviceIndex:).
	if s.vm.ID != 0 {
		resp := b.sendMouseVMDirect(cmd)
		if resp != nil {
			return resp
		}
	}

	// Fall back to the CGEvent path.
	return b.sendMouseCGEvent(cmd)
}

// sendMouseVMDirect creates an NSEvent and sends it directly to the
// VZVirtualMachine via the private sendPointerNSEvent:pointingDeviceIndex:.
// If that selector is unavailable, it falls back to routing through
// VZVirtualMachineView's mouse event handlers.
//
// Y mapping uses the cached viewContentHeight (the VM content area)
// rather than the NSView bounds height, which includes the title bar.
// See the package-level note on inputBridge.
func (b *inputBridge) sendMouseVMDirect(cmd *controlpb.MouseCommand) *controlpb.ControlResponse {
	s := b.cs
	var resp *controlpb.ControlResponse
	runOnUIThreadSync(func() {
		bounds := vmViewAsNSView(s.vmView).Bounds()

		// Calculate view-local coordinates.
		// Callers use top-left origin (x=0,y=0 is top-left, normalized 0-1).
		// NSEvent locationInWindow uses bottom-left origin, so we flip Y.
		//
		// The NSView bounds height (800) includes the title bar area, but
		// callers send normalized coordinates relative to the VM content area
		// (768px, matching the cropped screenshot). Map Y against the content
		// height so OCR coordinates translate correctly to click targets.
		contentH := float64(s.viewContentHeight)
		if contentH <= 0 {
			contentH = bounds.Size.Height // fallback if not set
		}
		var viewX, viewY float64
		captureW, captureH := s.lastCaptureBounds()
		useWindowMapping := needsWindowCapturePointMapping(s.captureBackend(), captureW, captureH, bounds.Size.Width, contentH)

		if cmd.Absolute {
			if useWindowMapping {
				viewX, viewY = mapWindowCapturePointToViewPoint(cmd.X, cmd.Y, captureW, captureH, bounds.Size.Width, contentH)
			} else {
				viewX = cmd.X
				viewY = contentH - cmd.Y
			}
		} else {
			if useWindowMapping {
				viewX, viewY = mapNormalizedWindowCapturePointToViewPoint(cmd.X, cmd.Y, captureW, captureH, bounds.Size.Width, contentH)
			} else {
				viewX = cmd.X * bounds.Size.Width
				viewY = (1.0 - cmd.Y) * contentH
			}
		}

		if verbose {
			fmt.Printf("[mouse-direct] bounds=%.0fx%.0f contentH=%.0f input=(%.3f,%.3f) view=(%.1f,%.1f) action=%s\n",
				bounds.Size.Width, bounds.Size.Height, contentH, cmd.X, cmd.Y, viewX, viewY, cmd.Action)
		}

		location := corefoundation.CGPoint{X: viewX, Y: viewY}

		var eventType appkit.NSEventType
		switch cmd.Action {
		case "down":
			if cmd.Button == 1 {
				eventType = appkit.NSEventTypeRightMouseDown
			} else {
				eventType = appkit.NSEventTypeLeftMouseDown
			}
		case "up":
			if cmd.Button == 1 {
				eventType = appkit.NSEventTypeRightMouseUp
			} else {
				eventType = appkit.NSEventTypeLeftMouseUp
			}
		case "move":
			eventType = appkit.NSEventTypeMouseMoved
		default:
			resp = &controlpb.ControlResponse{Error: fmt.Sprintf("unknown mouse action: %s", cmd.Action)}
			return
		}

		windowNumber := s.window.WindowNumber()

		// Create NSEvent for mouse
		iEvent := appkit.GetNSEventClass().MouseEventWithTypeLocationModifierFlagsTimestampWindowNumberContextEventNumberClickCountPressure(
			eventType,
			location,
			0, // modifiers
			0, // timestamp (0 = now)
			int(windowNumber),
			nil,          // context
			0,            // eventNumber
			1,            // clickCount
			float32(1.0), // pressure
		)
		event := appkit.NSEventFromID(iEvent.GetID())
		if event.ID == 0 {
			resp = &controlpb.ControlResponse{Error: "failed to create mouse NSEvent"}
			return
		}

		if s.vm.ID != 0 && objc.Send[bool](s.vm.ID, objc.Sel("respondsToSelector:"), objc.Sel("sendPointerNSEvent:pointingDeviceIndex:")) {
			if verbose {
				fmt.Printf("[mouse-hid] action=%s point=(%.1f,%.1f)\n", cmd.Action, viewX, viewY)
			}
			objc.Send[struct{}](s.vm.ID, objc.Sel("sendPointerNSEvent:pointingDeviceIndex:"), event.ID, uint32(0))
			resp = &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_Empty{Empty: &controlpb.EmptyResponse{}}}
			return
		}

		// Fall back to routing through VZVirtualMachineView's own event methods.
		switch cmd.Action {
		case "down":
			if cmd.Button == 1 {
				objc.Send[struct{}](s.vmView.ID, objc.Sel("rightMouseDown:"), event.ID)
			} else {
				objc.Send[struct{}](s.vmView.ID, objc.Sel("mouseDown:"), event.ID)
			}
		case "up":
			if cmd.Button == 1 {
				objc.Send[struct{}](s.vmView.ID, objc.Sel("rightMouseUp:"), event.ID)
			} else {
				objc.Send[struct{}](s.vmView.ID, objc.Sel("mouseUp:"), event.ID)
			}
		case "move":
			objc.Send[struct{}](s.vmView.ID, objc.Sel("mouseMoved:"), event.ID)
		}
		resp = &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_Empty{Empty: &controlpb.EmptyResponse{}}}
	})
	return resp
}

// sendMouseCGEvent sends a mouse event using CGEvent (legacy path).
func (b *inputBridge) sendMouseCGEvent(cmd *controlpb.MouseCommand) *controlpb.ControlResponse {
	s := b.cs
	// Read window/view geometry on the main thread.
	var windowFrame corefoundation.CGRect
	var bounds corefoundation.CGRect
	var screenHeight float64
	runOnUIThreadSync(func() {
		s.window.MakeKeyAndOrderFront(nil)
		windowFrame = s.window.Frame()
		bounds = vmViewAsNSView(s.vmView).Bounds()
		mainScreen := appkit.GetNSScreenClass().MainScreen()
		screenHeight = mainScreen.Frame().Size.Height
	})

	var viewX, viewY float64
	if cmd.Absolute {
		viewX = cmd.X
		viewY = cmd.Y
		if s.captureBackend() == automationBackendWindow {
			captureW, captureH := s.lastCaptureBounds()
			if captureW > 0 && captureH > 0 {
				viewX = cmd.X * (windowFrame.Size.Width / float64(captureW))
				viewY = cmd.Y * (windowFrame.Size.Height / float64(captureH))
			}
		}
	} else if s.captureBackend() == automationBackendWindow {
		captureW, captureH := s.lastCaptureBounds()
		if captureW > 0 && captureH > 0 {
			viewX = cmd.X * float64(captureW)
			viewY = cmd.Y * float64(captureH)
		} else {
			viewX = cmd.X * windowFrame.Size.Width
			viewY = cmd.Y * windowFrame.Size.Height
		}
	} else {
		viewX = cmd.X * bounds.Size.Width
		viewY = cmd.Y * bounds.Size.Height
	}

	screenX := windowFrame.Origin.X + viewX
	windowTop := screenHeight - (windowFrame.Origin.Y + windowFrame.Size.Height)
	screenY := windowTop + viewY
	if !cmd.Absolute && s.captureBackend() != automationBackendWindow {
		screenY += 22
	}

	if verbose {
		captureW, captureH := s.lastCaptureBounds()
		fmt.Printf("[mouse-cgevent] absolute=%v action=%s input=(%.1f,%.1f) view=(%.1f,%.1f) window=(x=%.1f y=%.1f w=%.1f h=%.1f) capture=%dx%d screen=(%.1f,%.1f)\n",
			cmd.Absolute, cmd.Action, cmd.X, cmd.Y, viewX, viewY,
			windowFrame.Origin.X, windowFrame.Origin.Y, windowFrame.Size.Width, windowFrame.Size.Height,
			captureW, captureH, screenX, screenY)
	}

	position := corefoundation.CGPoint{X: screenX, Y: screenY}

	// Map action to CGEvent mouse type
	var eventType uint32
	var mouseButton uint32 = uint32(cmd.Button)
	switch cmd.Action {
	case "move":
		eventType = cgEventMouseMoved
	case "down":
		switch cmd.Button {
		case 0:
			eventType = cgEventLeftMouseDown
		case 1:
			eventType = cgEventRightMouseDown
		default:
			return &controlpb.ControlResponse{Error: "only left (0) and right (1) buttons supported"}
		}
	case "up":
		switch cmd.Button {
		case 0:
			eventType = cgEventLeftMouseUp
		case 1:
			eventType = cgEventRightMouseUp
		default:
			return &controlpb.ControlResponse{Error: "only left (0) and right (1) buttons supported"}
		}
	default:
		return &controlpb.ControlResponse{Error: fmt.Sprintf("unknown mouse action: %s", cmd.Action)}
	}

	// Create and post CGEvent
	event, err := createMouseEvent(0, eventType, position, mouseButton)
	if err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("init CGEvent: %v", err)}
	}
	if event == 0 {
		return &controlpb.ControlResponse{Error: "failed to create CGEvent"}
	}
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(event))

	// Post through the system HID event tap so events travel the same
	// path as real mouse input (window server → focused app → key window).
	if err := postEvent(cgHIDEventTap, event); err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("init CG: %v", err)}
	}

	return &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_Empty{Empty: &controlpb.EmptyResponse{}}}
}
