// Pointer/keyboard input dispatch sub-component of the control
// server. InputBridge owns the mouse and keyboard delivery paths used
// by the control socket: the direct VM input path
// (sendPointerNSEvent:pointingDeviceIndex: / _VZKeyboard.sendKeyEvents:),
// the AppKit NSEvent path delivered through VZVirtualMachineView, and
// the Quartz CGEvent fallback. The bridge does not own any view state;
// the cached vmView, window and viewContentHeight live on
// ControlServer (read through the InputHost back-channel) because
// screen capture and lifecycle read the same fields.
package controlserver

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

// CGEvent / keyboard primitives the bridge calls into. These are the
// thin C-binding helpers that live in package main alongside the rest
// of the CGEvent surface; the bridge takes function-typed back-channels
// so the controlserver package stays free of cgo build tags and the
// Quartz frame-of-reference.
//
// Package main installs these at init time. They are unexported because
// nothing outside this package should reach into the bridge through
// these hooks.
var (
	createMouseEventFn       func(uint64, uint32, corefoundation.CGPoint, uint32) (uintptr, error)
	createKeyboardEventFn    func(uint64, uint16, bool) (uintptr, error)
	postEventFn              func(uint32, uintptr) error
	setEventUnicodeStringFn  func(uintptr, string)
	setEventFlagsFn          func(uintptr, uint64)
	runOnUIThreadSyncFn      func(func())
	allowHIDKeyboardFn       func() bool
	modifierKeySequenceFn    func(uint32) []uint32
	modifierShiftMask        uint32

	cgEventMouseMoved     uint32
	cgEventLeftMouseDown  uint32
	cgEventRightMouseDown uint32
	cgEventLeftMouseUp    uint32
	cgEventRightMouseUp   uint32
	cgHIDEventTap         uint32
)

// InputBridgeRuntime collects the package-main-side primitives the
// bridge depends on. Package main calls SetInputBridgeRuntime in init
// order before any input event is dispatched.
type InputBridgeRuntime struct {
	CreateMouseEvent      func(eventSource uint64, eventType uint32, position corefoundation.CGPoint, mouseButton uint32) (uintptr, error)
	CreateKeyboardEvent   func(eventSource uint64, keyCode uint16, keyDown bool) (uintptr, error)
	PostEvent             func(tap uint32, event uintptr) error
	SetEventUnicodeString func(event uintptr, s string)
	SetEventFlags         func(event uintptr, flags uint64)
	RunOnUIThreadSync     func(func())
	AllowHIDKeyboard      func() bool
	ModifierKeySequence   func(flags uint32) []uint32
	ModifierShift         uint32

	CGEventMouseMoved     uint32
	CGEventLeftMouseDown  uint32
	CGEventRightMouseDown uint32
	CGEventLeftMouseUp    uint32
	CGEventRightMouseUp   uint32
	CGHIDEventTap         uint32
}

// SetInputBridgeRuntime installs the package-main runtime hooks the
// input bridge calls into. Must be called once at process start before
// any input event is dispatched.
func SetInputBridgeRuntime(rt InputBridgeRuntime) {
	createMouseEventFn = rt.CreateMouseEvent
	createKeyboardEventFn = rt.CreateKeyboardEvent
	postEventFn = rt.PostEvent
	setEventUnicodeStringFn = rt.SetEventUnicodeString
	setEventFlagsFn = rt.SetEventFlags
	runOnUIThreadSyncFn = rt.RunOnUIThreadSync
	allowHIDKeyboardFn = rt.AllowHIDKeyboard
	modifierKeySequenceFn = rt.ModifierKeySequence
	modifierShiftMask = rt.ModifierShift

	cgEventMouseMoved = rt.CGEventMouseMoved
	cgEventLeftMouseDown = rt.CGEventLeftMouseDown
	cgEventRightMouseDown = rt.CGEventRightMouseDown
	cgEventLeftMouseUp = rt.CGEventLeftMouseUp
	cgEventRightMouseUp = rt.CGEventRightMouseUp
	cgHIDEventTap = rt.CGHIDEventTap
}

// InputBridge owns the mouse and keyboard delivery paths used by
// the control socket. The host injects an InputHost back-channel
// for VM/view/window handles and configuration; the bridge stays
// free of package-main globals.
//
// Two invariants must survive every refactor:
//
//  1. Mouse Y mapping uses the cached ViewContentHeight, never the
//     NSView bounds height (32px title bar would offset every click).
//
//  2. Keyboard input travels through CGEventCreateKeyboardEvent →
//     +[NSEvent eventWithCGEvent:] → keyDown:/keyUp: on the
//     VZVirtualMachineView. purego's objc.Send on ARM64 corrupts
//     uint16 parameters past argument position 8 (NSEvent
//     keyEventWithType:...keyCode: places keyCode at position 10).
type InputBridge struct {
	host InputHost
}

// SetHost wires (or rewires) the host back-channel. Mirrors the
// pattern used by AgentBridge so ControlServer can keep embedding
// the bridge by value with a zero-usable struct.
func (b *InputBridge) SetHost(host InputHost) { b.host = host }

// SendMouse sends a mouse event to the VM. Uses either the direct VM
// input path or the host window-server CGEvent path, depending on the
// configured automation backend.
func (b *InputBridge) SendMouse(cmd *controlpb.MouseCommand) *controlpb.ControlResponse {
	h := b.host
	if h.VMView().ID == 0 {
		return &controlpb.ControlResponse{Error: "mouse input requires GUI mode (run with -gui)"}
	}
	if h.Window().ID == 0 {
		return &controlpb.ControlResponse{Error: "mouse input requires GUI mode (run with -gui)"}
	}

	// Handle click as move+down+up sequence.
	if cmd.Action == "click" {
		moveCmd := &controlpb.MouseCommand{
			X: cmd.X, Y: cmd.Y, Button: cmd.Button, Action: "move", Absolute: cmd.Absolute,
		}
		moveResp := b.SendMouse(moveCmd)
		if !moveResp.Success {
			return moveResp
		}
		time.Sleep(20 * time.Millisecond)

		downCmd := &controlpb.MouseCommand{
			X: cmd.X, Y: cmd.Y, Button: cmd.Button, Action: "down", Absolute: cmd.Absolute,
		}
		downResp := b.SendMouse(downCmd)
		if !downResp.Success {
			return downResp
		}
		time.Sleep(50 * time.Millisecond)
		upCmd := &controlpb.MouseCommand{
			X: cmd.X, Y: cmd.Y, Button: cmd.Button, Action: "up", Absolute: cmd.Absolute,
		}
		return b.SendMouse(upCmd)
	}

	switch h.InputBackend() {
	case BackendWindow:
		if cmd.Absolute && h.CaptureBackend() == BackendWindow {
			return b.sendMouseVMDirect(cmd)
		}
		return b.sendMouseCGEvent(cmd)
	case BackendFramebuffer:
		return b.sendMouseVMDirect(cmd)
	}

	// Try the direct VM input path first (sendPointerNSEvent:pointingDeviceIndex:).
	if h.VM().ID != 0 {
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
// Y mapping uses the cached ViewContentHeight (the VM content area)
// rather than the NSView bounds height, which includes the title bar.
func (b *InputBridge) sendMouseVMDirect(cmd *controlpb.MouseCommand) *controlpb.ControlResponse {
	h := b.host
	var resp *controlpb.ControlResponse
	runOnUIThreadSyncFn(func() {
		bounds := VMViewAsNSView(h.VMView()).Bounds()

		// Calculate view-local coordinates.
		// Callers use top-left origin (x=0,y=0 is top-left, normalized 0-1).
		// NSEvent locationInWindow uses bottom-left origin, so we flip Y.
		//
		// The NSView bounds height (800) includes the title bar area, but
		// callers send normalized coordinates relative to the VM content area
		// (768px, matching the cropped screenshot). Map Y against the content
		// height so OCR coordinates translate correctly to click targets.
		contentH := float64(h.ViewContentHeight())
		if contentH <= 0 {
			contentH = bounds.Size.Height // fallback if not set
		}
		var viewX, viewY float64
		captureW, captureH := h.LastCaptureBounds()
		useWindowMapping := NeedsWindowCapturePointMapping(h.CaptureBackend(), captureW, captureH, bounds.Size.Width, contentH)

		if cmd.Absolute {
			if useWindowMapping {
				viewX, viewY = MapWindowCapturePointToViewPoint(cmd.X, cmd.Y, captureW, captureH, bounds.Size.Width, contentH)
			} else {
				viewX = cmd.X
				viewY = contentH - cmd.Y
			}
		} else {
			if useWindowMapping {
				viewX, viewY = MapNormalizedWindowCapturePointToViewPoint(cmd.X, cmd.Y, captureW, captureH, bounds.Size.Width, contentH)
			} else {
				viewX = cmd.X * bounds.Size.Width
				viewY = (1.0 - cmd.Y) * contentH
			}
		}

		if h.Verbose() {
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

		windowNumber := h.Window().WindowNumber()

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

		vmHandle := h.VM()
		if vmHandle.ID != 0 && objc.Send[bool](vmHandle.ID, objc.Sel("respondsToSelector:"), objc.Sel("sendPointerNSEvent:pointingDeviceIndex:")) {
			if h.Verbose() {
				fmt.Printf("[mouse-hid] action=%s point=(%.1f,%.1f)\n", cmd.Action, viewX, viewY)
			}
			objc.Send[struct{}](vmHandle.ID, objc.Sel("sendPointerNSEvent:pointingDeviceIndex:"), event.ID, uint32(0))
			resp = &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_Empty{Empty: &controlpb.EmptyResponse{}}}
			return
		}

		// Fall back to routing through VZVirtualMachineView's own event methods.
		vmViewHandle := h.VMView()
		switch cmd.Action {
		case "down":
			if cmd.Button == 1 {
				objc.Send[struct{}](vmViewHandle.ID, objc.Sel("rightMouseDown:"), event.ID)
			} else {
				objc.Send[struct{}](vmViewHandle.ID, objc.Sel("mouseDown:"), event.ID)
			}
		case "up":
			if cmd.Button == 1 {
				objc.Send[struct{}](vmViewHandle.ID, objc.Sel("rightMouseUp:"), event.ID)
			} else {
				objc.Send[struct{}](vmViewHandle.ID, objc.Sel("mouseUp:"), event.ID)
			}
		case "move":
			objc.Send[struct{}](vmViewHandle.ID, objc.Sel("mouseMoved:"), event.ID)
		}
		resp = &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_Empty{Empty: &controlpb.EmptyResponse{}}}
	})
	return resp
}

// sendMouseCGEvent sends a mouse event using CGEvent (legacy path).
func (b *InputBridge) sendMouseCGEvent(cmd *controlpb.MouseCommand) *controlpb.ControlResponse {
	h := b.host
	// Read window/view geometry on the main thread.
	var windowFrame corefoundation.CGRect
	var bounds corefoundation.CGRect
	var screenHeight float64
	runOnUIThreadSyncFn(func() {
		w := h.Window()
		w.MakeKeyAndOrderFront(nil)
		windowFrame = w.Frame()
		bounds = VMViewAsNSView(h.VMView()).Bounds()
		mainScreen := appkit.GetNSScreenClass().MainScreen()
		screenHeight = mainScreen.Frame().Size.Height
	})

	var viewX, viewY float64
	if cmd.Absolute {
		viewX = cmd.X
		viewY = cmd.Y
		if h.CaptureBackend() == BackendWindow {
			captureW, captureH := h.LastCaptureBounds()
			if captureW > 0 && captureH > 0 {
				viewX = cmd.X * (windowFrame.Size.Width / float64(captureW))
				viewY = cmd.Y * (windowFrame.Size.Height / float64(captureH))
			}
		}
	} else if h.CaptureBackend() == BackendWindow {
		captureW, captureH := h.LastCaptureBounds()
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
	if !cmd.Absolute && h.CaptureBackend() != BackendWindow {
		screenY += 22
	}

	if h.Verbose() {
		captureW, captureH := h.LastCaptureBounds()
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
	event, err := createMouseEventFn(0, eventType, position, mouseButton)
	if err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("init CGEvent: %v", err)}
	}
	if event == 0 {
		return &controlpb.ControlResponse{Error: "failed to create CGEvent"}
	}
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(event))

	// Post through the system HID event tap so events travel the same
	// path as real mouse input (window server → focused app → key window).
	if err := postEventFn(cgHIDEventTap, event); err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("init CG: %v", err)}
	}

	return &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_Empty{Empty: &controlpb.EmptyResponse{}}}
}

// SendKey is the top-level entry point for keyboard input. Modifier
// chords are synthesized as real modifier-key presses; bare keys go
// through the configured automation backend.
func (b *InputBridge) SendKey(cmd *controlpb.KeyCommand) *controlpb.ControlResponse {
	h := b.host
	if h.VMView().ID == 0 {
		return &controlpb.ControlResponse{Error: "keyboard input requires GUI mode (run with -gui)"}
	}

	// Modifier chords are fragile in Recovery UI when represented only as
	// event flags. Synthesize real modifier-key presses instead.
	if cmd.Modifiers != 0 {
		return b.sendKeyChord(cmd)
	}

	// For non-modifier keys, choose the configured automation backend unless
	// the caller explicitly asks for CGEvent-style fallback behavior.
	return b.SendKeyPrimitive(cmd)
}

// SendKeyPrimitive dispatches a single (non-chord) key event through
// the configured automation input backend.
func (b *InputBridge) SendKeyPrimitive(cmd *controlpb.KeyCommand) *controlpb.ControlResponse {
	h := b.host
	if h.InputBackend() == BackendWindow {
		return b.SendKeyCGEvent(cmd)
	}
	if h.InputBackend() == BackendFramebuffer {
		if !allowHIDKeyboardFn() {
			return &controlpb.ControlResponse{Error: "framebuffer keyboard input disabled by VZ_MACOS_DISABLE_HID_KEYBOARD"}
		}
		return b.SendKeyPrivate(cmd)
	}
	if cmd.UseCgEvent {
		return b.sendKeyMultiPath(cmd, false)
	}
	return b.SendKeyNSEvent(cmd)
}

func (b *InputBridge) sendKeyChord(cmd *controlpb.KeyCommand) *controlpb.ControlResponse {
	mods := modifierKeySequenceFn(cmd.Modifiers)
	if len(mods) == 0 {
		// Unknown modifier bit pattern; fall back to flag-based injection.
		return b.sendKeyMultiPath(cmd, true)
	}

	var errs []string
	send := func(name string, c *controlpb.KeyCommand) bool {
		resp := b.SendKeyPrimitive(c)
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

func (b *InputBridge) sendKeyMultiPath(cmd *controlpb.KeyCommand, fanout bool) *controlpb.ControlResponse {
	type keyInjector struct {
		name string
		fn   func(*controlpb.KeyCommand) *controlpb.ControlResponse
	}
	paths := []keyInjector{
		{name: "nsevent", fn: b.SendKeyNSEvent},
		{name: "cgevent", fn: b.SendKeyCGEvent},
	}
	if allowHIDKeyboardFn() {
		paths = append(paths, keyInjector{name: "private", fn: b.SendKeyPrivate})
	}
	if cmd.UseCgEvent {
		// For shortcut-style input, prefer lower-level injectors first.
		paths = []keyInjector{
			{name: "cgevent", fn: b.SendKeyCGEvent},
			{name: "nsevent", fn: b.SendKeyNSEvent},
		}
		if allowHIDKeyboardFn() {
			paths = append([]keyInjector{{name: "private", fn: b.SendKeyPrivate}}, paths...)
		}
	}

	var errs []string
	succeeded := false
	for _, p := range paths {
		resp := p.fn(cmd)
		if resp != nil && resp.Success {
			succeeded = true
			if b.host.Verbose() {
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
		if b.host.Verbose() {
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

// SendKeyPrivate sends a keyboard event through the typed private
// _VZKeyboard.sendKeyEvents: path using _VZKeyEvent objects.
//
// The framebuffer backend uses this VM-local path by default. It still
// depends on private Virtualization selectors, so
// VZ_MACOS_DISABLE_HID_KEYBOARD=1 can temporarily disable it if a
// host/framework regression appears.
//
// A test override registered with the host (PrivateKeyHook) short-
// circuits the path so unit tests can assert reachability without a
// live VM.
func (b *InputBridge) SendKeyPrivate(cmd *controlpb.KeyCommand) *controlpb.ControlResponse {
	h := b.host
	if hook := h.PrivateKeyHook(); hook != nil {
		return hook(cmd)
	}
	event, err := b.newKeyboardNSEvent(cmd)
	if err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}
	sender := vminput.NewSender(vm.WrapQueue(h.VMQueue()), h.VM())
	if err := sender.SendKeyboardNSEvent(event, 0); err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("private keyboard inject: %v", err)}
	}
	if h.Verbose() {
		fmt.Printf("[key-private] sent keyCode=%d down=%v mods=%d chars=%q\n",
			cmd.KeyCode, cmd.KeyDown, cmd.Modifiers, KeyboardEventUnicodeString(cmd))
	}
	return &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_Empty{Empty: &controlpb.EmptyResponse{}}}
}

// SendKeyCGEvent uses Quartz CGEvent for keyboard injection.
// Events are posted through the system HID event tap so they travel
// through the window server to VZVirtualMachineView (the same path
// as real keyboard input). The VM window must be key and frontmost.
func (b *InputBridge) SendKeyCGEvent(cmd *controlpb.KeyCommand) *controlpb.ControlResponse {
	h := b.host
	// Activate and focus the VM window on the main thread first.
	runOnUIThreadSyncFn(func() {
		appkit.GetNSApplicationClass().SharedApplication().Activate()
		w := h.Window()
		w.MakeKeyAndOrderFront(nil)
		w.MakeFirstResponder(VMViewAsNSView(h.VMView()).NSResponder)
	})

	event, err := createKeyboardEventFn(0, uint16(cmd.KeyCode), cmd.KeyDown)
	if err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("init CGEvent: %v", err)}
	}
	if event == 0 {
		return &controlpb.ControlResponse{Error: "failed to create CGEvent"}
	}
	defer corefoundation.CFRelease(corefoundation.CFTypeRef(event))

	chars := KeyboardEventUnicodeString(cmd)
	if chars != "" {
		setEventUnicodeStringFn(event, chars)
	}
	if cmd.Modifiers != 0 {
		setEventFlagsFn(event, uint64(cmd.Modifiers))
	}

	// Try both delivery methods: first activate the app, then post through
	// the HID event tap (window server path). Also post to PID as fallback.
	if err := postEventFn(cgHIDEventTap, event); err != nil {
		return &controlpb.ControlResponse{Error: fmt.Sprintf("init CG: %v", err)}
	}
	if h.Verbose() {
		fmt.Printf("[key-cgevent] posted keyCode=%d down=%v chars=%q via HID event tap\n", cmd.KeyCode, cmd.KeyDown, chars)
	}

	return &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_Empty{Empty: &controlpb.EmptyResponse{}}}
}

// SendKeyNSEvent uses AppKit NSEvent for view-level keyboard injection.
// All AppKit calls are dispatched to the main thread.
//
// The CGEvent → +[NSEvent eventWithCGEvent:] → keyDown:/keyUp: chain
// is the only path that survives purego's ARM64 stack-passing bug for
// uint16 parameters.
func (b *InputBridge) SendKeyNSEvent(cmd *controlpb.KeyCommand) *controlpb.ControlResponse {
	h := b.host
	event, err := b.newKeyboardNSEvent(cmd)
	if err != nil {
		return &controlpb.ControlResponse{Error: err.Error()}
	}

	// Convert CGEvent to NSEvent and deliver to VZVirtualMachineView on the main thread.
	var resp *controlpb.ControlResponse
	runOnUIThreadSyncFn(func() {
		// Make sure the VM view is first responder.
		w := h.Window()
		vmView := h.VMView()
		w.MakeKeyAndOrderFront(nil)
		w.MakeFirstResponder(VMViewAsNSView(vmView).NSResponder)

		actualKeyCode := event.KeyCode()
		chars := KeyboardEventUnicodeString(cmd)
		if h.Verbose() {
			fmt.Printf("[key-nsevent] vmView=%x keyCode=%d actualKeyCode=%d down=%v chars=%q\n",
				vmView.ID, cmd.KeyCode, actualKeyCode, cmd.KeyDown, chars)
		}

		// Route through VZVirtualMachineView's keyDown:/keyUp: directly,
		// matching the pattern used for mouse events (mouseDown:/mouseUp:).
		if cmd.KeyDown {
			objc.Send[struct{}](vmView.ID, objc.Sel("keyDown:"), event.ID)
		} else {
			objc.Send[struct{}](vmView.ID, objc.Sel("keyUp:"), event.ID)
		}

		if h.Verbose() {
			fmt.Printf("[key-nsevent] sent successfully\n")
		}

		resp = &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_Empty{Empty: &controlpb.EmptyResponse{}}}
	})
	return resp
}

// TypeText types text into the VM character by character. Each
// character is mapped to its macOS virtual keycode and delivered
// through the configured automation backend. The caller must not hold
// the host RPC mutex; TypeText acquires it per key event so each send
// observes the same control-server state as a fresh key RPC.
func (b *InputBridge) TypeText(cmd *controlpb.TextCommand) *controlpb.ControlResponse {
	h := b.host
	if h.VMView().ID == 0 || h.Window().ID == 0 {
		return &controlpb.ControlResponse{Error: "text input requires GUI mode (run with -gui)"}
	}

	if h.Verbose() {
		fmt.Printf("[typeText] typing %d chars: %q\n", len([]rune(cmd.Text)), cmd.Text)
	}

	send := func(kc *controlpb.KeyCommand) *controlpb.ControlResponse {
		h.Lock()
		defer h.Unlock()
		return b.SendKey(kc)
	}

	for _, ch := range cmd.Text {
		info, ok := CharToKeyCode[ch]
		if !ok {
			if h.Verbose() {
				fmt.Printf("[typeText] no keycode for %q, skipping\n", ch)
			}
			continue
		}

		var mods uint32
		if info.Shift {
			mods = modifierShiftMask
		}

		downCmd := &controlpb.KeyCommand{
			KeyCode:   uint32(info.KeyCode),
			KeyDown:   true,
			Modifiers: mods,
			Character: string(ch),
		}
		if resp := send(downCmd); resp.Error != "" {
			return resp
		}
		time.Sleep(30 * time.Millisecond)

		upCmd := &controlpb.KeyCommand{
			KeyCode:   uint32(info.KeyCode),
			KeyDown:   false,
			Modifiers: mods,
			Character: string(ch),
		}
		if resp := send(upCmd); resp.Error != "" {
			return resp
		}
		time.Sleep(50 * time.Millisecond)
	}

	if h.Verbose() {
		fmt.Printf("[typeText] done typing %q\n", cmd.Text)
	}
	return &controlpb.ControlResponse{Success: true, Result: &controlpb.ControlResponse_Empty{Empty: &controlpb.EmptyResponse{}}}
}

func (b *InputBridge) newKeyboardNSEvent(cmd *controlpb.KeyCommand) (appkit.NSEvent, error) {
	cgEvent, err := createKeyboardEventFn(0, uint16(cmd.KeyCode), cmd.KeyDown)
	if err != nil {
		return appkit.NSEvent{}, fmt.Errorf("create CGEvent: %v", err)
	}
	if cgEvent == 0 {
		return appkit.NSEvent{}, fmt.Errorf("create CGEvent returned nil")
	}

	chars := KeyboardEventUnicodeString(cmd)
	if chars != "" {
		setEventUnicodeStringFn(cgEvent, chars)
	}
	if cmd.Modifiers != 0 {
		setEventFlagsFn(cgEvent, uint64(cmd.Modifiers))
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
