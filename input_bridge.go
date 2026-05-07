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
	"strings"
	"time"

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/objc"
	"github.com/tmc/apple/x/vzkit/vm"
	"github.com/tmc/apple/x/vzkit/vminput"

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

// sendKey is the top-level entry point for keyboard input. Modifier
// chords are synthesized as real modifier-key presses; bare keys go
// through the configured automation backend.
func (b *inputBridge) sendKey(cmd *controlpb.KeyCommand) *controlpb.ControlResponse {
	s := b.cs
	if s.vmView.ID == 0 {
		return &controlpb.ControlResponse{Error: "keyboard input requires GUI mode (run with -gui)"}
	}

	// Modifier chords are fragile in Recovery UI when represented only as
	// event flags. Synthesize real modifier-key presses instead.
	if cmd.Modifiers != 0 {
		return b.sendKeyChord(cmd)
	}

	// For non-modifier keys, choose the configured automation backend unless
	// the caller explicitly asks for CGEvent-style fallback behavior.
	return b.sendKeyPrimitive(cmd)
}

func (b *inputBridge) sendKeyPrimitive(cmd *controlpb.KeyCommand) *controlpb.ControlResponse {
	s := b.cs
	if s.inputBackend() == automationBackendWindow {
		return b.sendKeyCGEvent(cmd)
	}
	if s.inputBackend() == automationBackendFramebuffer {
		if !allowHIDKeyboard() {
			return &controlpb.ControlResponse{Error: "framebuffer keyboard input disabled by VZ_MACOS_DISABLE_HID_KEYBOARD"}
		}
		return b.sendKeyPrivate(cmd)
	}
	if cmd.UseCgEvent {
		return b.sendKeyMultiPath(cmd, false)
	}
	return b.sendKeyNSEvent(cmd)
}

func (b *inputBridge) sendKeyChord(cmd *controlpb.KeyCommand) *controlpb.ControlResponse {
	mods := modifierKeySequence(cmd.Modifiers)
	if len(mods) == 0 {
		// Unknown modifier bit pattern; fall back to flag-based injection.
		return b.sendKeyMultiPath(cmd, true)
	}

	var errs []string
	send := func(name string, c *controlpb.KeyCommand) bool {
		resp := b.sendKeyPrimitive(c)
		if resp != nil && resp.Success {
			return true
		}
		msg := "unknown error"
		if resp != nil && resp.Error != "" {
			msg = resp.Error
		}
		errs = append(errs, name+": "+msg)
		return false
	}

	if cmd.KeyDown {
		for _, keyCode := range mods {
			send(fmt.Sprintf("modifier-down-%d", keyCode), &controlpb.KeyCommand{
				KeyCode:    keyCode,
				KeyDown:    true,
				UseCgEvent: cmd.UseCgEvent,
			})
		}
		if send("chord-key-down", &controlpb.KeyCommand{
			KeyCode:    cmd.KeyCode,
			Character:  cmd.Character,
			KeyDown:    true,
			UseCgEvent: cmd.UseCgEvent,
		}) {
			return &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_Empty{Empty: &controlpb.EmptyResponse{}}}
		}
		return &controlpb.ControlResponse{Error: "keyboard chord down failed: " + strings.Join(errs, "; ")}
	}

	// Key up: release primary key first, then modifiers in reverse order.
	send("chord-key-up", &controlpb.KeyCommand{
		KeyCode:    cmd.KeyCode,
		Character:  cmd.Character,
		KeyDown:    false,
		UseCgEvent: cmd.UseCgEvent,
	})
	for i := len(mods) - 1; i >= 0; i-- {
		keyCode := mods[i]
		send(fmt.Sprintf("modifier-up-%d", keyCode), &controlpb.KeyCommand{
			KeyCode:    keyCode,
			KeyDown:    false,
			UseCgEvent: cmd.UseCgEvent,
		})
	}
	if len(errs) == 0 {
		return &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_Empty{Empty: &controlpb.EmptyResponse{}}}
	}
	return &controlpb.ControlResponse{Error: "keyboard chord up failed: " + strings.Join(errs, "; ")}
}

func (b *inputBridge) sendKeyMultiPath(cmd *controlpb.KeyCommand, fanout bool) *controlpb.ControlResponse {
	type keyInjector struct {
		name string
		fn   func(*controlpb.KeyCommand) *controlpb.ControlResponse
	}
	paths := []keyInjector{
		{name: "nsevent", fn: b.sendKeyNSEvent},
		{name: "cgevent", fn: b.sendKeyCGEvent},
	}
	if allowHIDKeyboard() {
		paths = append(paths, keyInjector{name: "private", fn: b.sendKeyPrivate})
	}
	if cmd.UseCgEvent {
		// For shortcut-style input, prefer lower-level injectors first.
		paths = []keyInjector{
			{name: "cgevent", fn: b.sendKeyCGEvent},
			{name: "nsevent", fn: b.sendKeyNSEvent},
		}
		if allowHIDKeyboard() {
			paths = append([]keyInjector{{name: "private", fn: b.sendKeyPrivate}}, paths...)
		}
	}

	var errs []string
	succeeded := false
	for _, p := range paths {
		resp := p.fn(cmd)
		if resp != nil && resp.Success {
			succeeded = true
			if verbose {
				fmt.Printf("[key-fallback] %s ok keyCode=%d down=%v mods=%d\n",
					p.name, cmd.KeyCode, cmd.KeyDown, cmd.Modifiers)
			}
			if !fanout {
				return resp
			}
			continue
		}

		msg := "unknown error"
		if resp != nil && resp.Error != "" {
			msg = resp.Error
		}
		errs = append(errs, p.name+": "+msg)
		if verbose {
			fmt.Printf("[key-fallback] %s failed: %s\n", p.name, msg)
		}
	}

	if succeeded {
		return &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_Empty{Empty: &controlpb.EmptyResponse{}}}
	}
	if len(errs) == 0 {
		return &controlpb.ControlResponse{Error: "keyboard injection failed"}
	}
	return &controlpb.ControlResponse{Error: "keyboard injection failed: " + strings.Join(errs, "; ")}
}

// sendKeyPrivate sends a keyboard event through the typed private
// _VZKeyboard.sendKeyEvents: path using _VZKeyEvent objects.
//
// The framebuffer backend uses this VM-local path by default. It still depends
// on private Virtualization selectors, so VZ_MACOS_DISABLE_HID_KEYBOARD=1 can
// temporarily disable it if a host/framework regression appears.
func (b *inputBridge) sendKeyPrivate(cmd *controlpb.KeyCommand) *controlpb.ControlResponse {
	if sendKeyEventPrivateHook != nil {
		return sendKeyEventPrivateHook(b.cs, cmd)
	}
	s := b.cs
	event, err := b.newKeyboardNSEvent(cmd)
	if err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}
	sender := vminput.NewSender(vm.WrapQueue(s.vmQueue), s.vm)
	if err := sender.SendKeyboardNSEvent(event, 0); err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("private keyboard inject: %v", err)}
	}
	if verbose {
		fmt.Printf("[key-private] sent keyCode=%d down=%v mods=%d chars=%q\n",
			cmd.KeyCode, cmd.KeyDown, cmd.Modifiers, keyboardEventUnicodeString(cmd))
	}
	return &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_Empty{Empty: &controlpb.EmptyResponse{}}}
}

// sendKeyCGEvent uses Quartz CGEvent for keyboard injection.
// Events are posted through the system HID event tap so they travel
// through the window server to VZVirtualMachineView (the same path
// as real keyboard input). The VM window must be key and frontmost.
func (b *inputBridge) sendKeyCGEvent(cmd *controlpb.KeyCommand) *controlpb.ControlResponse {
	s := b.cs
	// Activate and focus the VM window on the main thread first.
	runOnUIThreadSync(func() {
		appkit.GetNSApplicationClass().SharedApplication().Activate()
		s.window.MakeKeyAndOrderFront(nil)
		s.window.MakeFirstResponder(vmViewAsNSView(s.vmView).NSResponder)
	})

	event, err := createKeyboardEvent(0, uint16(cmd.KeyCode), cmd.KeyDown)
	if err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("init CGEvent: %v", err)}
	}
	if event == 0 {
		return &controlpb.ControlResponse{Error: "failed to create CGEvent"}
	}
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(event))

	chars := keyboardEventUnicodeString(cmd)
	if chars != "" {
		setEventUnicodeString(event, chars)
	}
	if cmd.Modifiers != 0 {
		setEventFlags(event, uint64(cmd.Modifiers))
	}

	// Try both delivery methods: first activate the app, then post through
	// the HID event tap (window server path). Also post to PID as fallback.
	if err := postEvent(cgHIDEventTap, event); err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("init CG: %v", err)}
	}
	if verbose {
		fmt.Printf("[key-cgevent] posted keyCode=%d down=%v chars=%q via HID event tap\n", cmd.KeyCode, cmd.KeyDown, chars)
	}

	return &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_Empty{Empty: &controlpb.EmptyResponse{}}}
}

// sendKeyNSEvent uses AppKit NSEvent for view-level keyboard injection.
// All AppKit calls are dispatched to the main thread.
//
// The CGEvent → +[NSEvent eventWithCGEvent:] → keyDown:/keyUp: chain
// is the only path that survives purego's ARM64 stack-passing bug for
// uint16 parameters (NSEvent keyEventWithType:...keyCode: would corrupt
// the keyCode). See the package-level note on inputBridge.
func (b *inputBridge) sendKeyNSEvent(cmd *controlpb.KeyCommand) *controlpb.ControlResponse {
	s := b.cs
	event, err := b.newKeyboardNSEvent(cmd)
	if err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}

	// Convert CGEvent to NSEvent and deliver to VZVirtualMachineView on the main thread.
	var resp *controlpb.ControlResponse
	runOnUIThreadSync(func() {
		// Make sure the VM view is first responder.
		s.window.MakeKeyAndOrderFront(nil)
		s.window.MakeFirstResponder(vmViewAsNSView(s.vmView).NSResponder)

		actualKeyCode := event.KeyCode()
		chars := keyboardEventUnicodeString(cmd)
		if verbose {
			fmt.Printf("[key-nsevent] vmView=%x keyCode=%d actualKeyCode=%d down=%v chars=%q\n",
				s.vmView.ID, cmd.KeyCode, actualKeyCode, cmd.KeyDown, chars)
		}

		// Route through VZVirtualMachineView's keyDown:/keyUp: directly,
		// matching the pattern used for mouse events (mouseDown:/mouseUp:).
		if cmd.KeyDown {
			objc.Send[struct{}](s.vmView.ID, objc.Sel("keyDown:"), event.ID)
		} else {
			objc.Send[struct{}](s.vmView.ID, objc.Sel("keyUp:"), event.ID)
		}

		if verbose {
			fmt.Printf("[key-nsevent] sent successfully\n")
		}

		resp = &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_Empty{Empty: &controlpb.EmptyResponse{}}}
	})
	return resp
}

// typeText types text into the VM character by character. Each character is
// mapped to its macOS virtual keycode and delivered through the configured
// automation backend. The caller must not hold b.cs.mu; typeText acquires it
// per key event so each send observes the same state as a fresh key RPC.
func (b *inputBridge) typeText(cmd *controlpb.TextCommand) *controlpb.ControlResponse {
	if b.cs.vmView.ID == 0 || b.cs.window.ID == 0 {
		return &controlpb.ControlResponse{Error: "text input requires GUI mode (run with -gui)"}
	}

	if verbose {
		fmt.Printf("[typeText] typing %d chars: %q\n", len([]rune(cmd.Text)), cmd.Text)
	}

	send := func(kc *controlpb.KeyCommand) *controlpb.ControlResponse {
		b.cs.mu.Lock()
		defer b.cs.mu.Unlock()
		return b.sendKey(kc)
	}

	for _, ch := range cmd.Text {
		info, ok := charToKeyCode[ch]
		if !ok {
			if verbose {
				fmt.Printf("[typeText] no keycode for %q, skipping\n", ch)
			}
			continue
		}

		var mods uint32
		if info.shift {
			mods = uint32(ModifierShift)
		}

		downCmd := &controlpb.KeyCommand{
			KeyCode:   uint32(info.keyCode),
			KeyDown:   true,
			Modifiers: mods,
			Character: string(ch),
		}
		if resp := send(downCmd); resp.Error != "" {
			return resp
		}
		time.Sleep(30 * time.Millisecond)

		upCmd := &controlpb.KeyCommand{
			KeyCode:   uint32(info.keyCode),
			KeyDown:   false,
			Modifiers: mods,
			Character: string(ch),
		}
		if resp := send(upCmd); resp.Error != "" {
			return resp
		}
		time.Sleep(50 * time.Millisecond)
	}

	if verbose {
		fmt.Printf("[typeText] done typing %q\n", cmd.Text)
	}
	return &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_Empty{Empty: &controlpb.EmptyResponse{}}}
}

func (b *inputBridge) newKeyboardNSEvent(cmd *controlpb.KeyCommand) (appkit.NSEvent, error) {
	cgEvent, err := createKeyboardEvent(0, uint16(cmd.KeyCode), cmd.KeyDown)
	if err != nil {
		return appkit.NSEvent{}, fmt.Errorf("create CGEvent: %v", err)
	}
	if cgEvent == 0 {
		return appkit.NSEvent{}, fmt.Errorf("create CGEvent returned nil")
	}

	chars := keyboardEventUnicodeString(cmd)
	if chars != "" {
		setEventUnicodeString(cgEvent, chars)
	}
	if cmd.Modifiers != 0 {
		setEventFlags(cgEvent, uint64(cmd.Modifiers))
	}

	eventID := objc.Send[objc.ID](
		objc.ID(objc.GetClass("NSEvent")),
		objc.Sel("eventWithCGEvent:"),
		uintptr(cgEvent),
	)
	if eventID == 0 {
		return appkit.NSEvent{}, fmt.Errorf("NSEvent eventWithCGEvent: returned nil")
	}
	return appkit.NSEventFromID(eventID), nil
}
