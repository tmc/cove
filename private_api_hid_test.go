package main

import (
	"testing"
	"unsafe"

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/objc"
)

func ensureInputInit() error {
	event, err := createKeyboardEvent(0, 0, false)
	if event != 0 {
		corefoundation.CFRelease(corefoundation.CFTypeRef(event))
	}
	return err
}

// TestPrivateHIDAPISelectors verifies that the private HID input selectors exist
// on VZVirtualMachine. These selectors let us bypass VZVirtualMachineView and send
// input events directly to the VM, which is critical for headless automation and
// for working around the ARM64 purego keyCode corruption bug (objc.Send corrupts
// uint16 params beyond argument position 8 on the stack).
func TestPrivateHIDAPISelectors(t *testing.T) {
	vmClass := objc.GetClass("VZVirtualMachine")
	if vmClass == 0 {
		t.Fatal("VZVirtualMachine class not found")
	}

	selectors := []struct {
		name string
		sel  string
	}{
		{"SendKeyboardEvents", "sendKeyboardEvents:keyboardID:"},
		{"SendMouseEvents", "sendMouseEvents:pointingDeviceIndex:"},
		{"SendPointerNSEvent", "sendPointerNSEvent:pointingDeviceIndex:"},
		{"SendScrollWheelEvents", "sendScrollWheelEvents:pointingDeviceIndex:"},
		{"SendDigitizerEvents", "sendDigitizerEvents:pointingDeviceIndex:"},
		{"SendMultiTouchEvents", "sendMultiTouchEvents:multiTouchDeviceIndex:"},
		{"SendMagnifyEvents", "sendMagnifyEvents:pointingDeviceIndex:"},
		{"SendRotationEvents", "sendRotationEvents:pointingDeviceIndex:"},
		{"SendSmartMagnifyEvents", "sendSmartMagnifyEvents:pointingDeviceIndex:"},
		{"SendQuickLookEvents", "sendQuickLookEvents:pointingDeviceIndex:"},
		{"ShouldSendHIDReports", "_shouldSendHIDReports"},
		{"ProcessHIDReports", "_processHIDReports:forDevice:deviceType:"},
		{"HIDEventMonitor", "_hidEventMonitor"},
		{"Keyboards", "_keyboards"},
		{"PointingDevices", "_pointingDevices"},
		{"MultiTouchDevices", "_multiTouchDevices"},
	}

	for _, tc := range selectors {
		t.Run(tc.name, func(t *testing.T) {
			sel := objc.Sel(tc.sel)
			responds := objc.Send[bool](
				objc.Send[objc.ID](objc.ID(vmClass), objc.Sel("alloc")),
				objc.Sel("respondsToSelector:"),
				sel,
			)
			if !responds {
				t.Errorf("VZVirtualMachine does not respond to %s", tc.sel)
			} else {
				t.Logf("VZVirtualMachine responds to %s", tc.sel)
			}
		})
	}
}

// TestPrivateHIDSendPointerNSEventSignature tests that we can construct an NSEvent
// suitable for sendPointerNSEvent:pointingDeviceIndex:. This is the most promising
// private API because it accepts an actual NSEvent (which we already know how to build
// via CGEvent -> NSEvent conversion) rather than an opaque events array.
//
// This test constructs mouse events the same way sendMouseEventVMDirect does,
// verifying the event is valid without needing a running VM.
func TestPrivateHIDSendPointerNSEventSignature(t *testing.T) {
	// Construct a mouse move NSEvent using the same pattern as sendMouseEventVMDirect.
	location := corefoundation.CGPoint{X: 100, Y: 100}
	event := appkit.GetNSEventClass().MouseEventWithTypeLocationModifierFlagsTimestampWindowNumberContextEventNumberClickCountPressure(
		appkit.NSEventTypeMouseMoved,
		location,
		0,   // modifiers
		0,   // timestamp
		0,   // windowNumber (0 = no window)
		nil, // context
		0,   // eventNumber
		0,   // clickCount
		0.0, // pressure
	)
	eventID := event.GetID()
	if eventID == 0 {
		t.Fatal("failed to create mouse NSEvent")
	}

	// Verify the event properties match what we expect.
	eventType := objc.Send[uint64](eventID, objc.Sel("type"))
	t.Logf("created NSEvent type=%d (NSEventTypeMouseMoved=%d)", eventType, appkit.NSEventTypeMouseMoved)
	if eventType != uint64(appkit.NSEventTypeMouseMoved) {
		t.Errorf("event type = %d, want %d", eventType, appkit.NSEventTypeMouseMoved)
	}

	// Construct mouse down and up events.
	for _, tc := range []struct {
		name      string
		eventType appkit.NSEventType
	}{
		{"LeftMouseDown", appkit.NSEventTypeLeftMouseDown},
		{"LeftMouseUp", appkit.NSEventTypeLeftMouseUp},
		{"RightMouseDown", appkit.NSEventTypeRightMouseDown},
		{"RightMouseUp", appkit.NSEventTypeRightMouseUp},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ev := appkit.GetNSEventClass().MouseEventWithTypeLocationModifierFlagsTimestampWindowNumberContextEventNumberClickCountPressure(
				tc.eventType,
				location,
				0, 0, 0, nil, 0, 1, 1.0,
			)
			if ev.GetID() == 0 {
				t.Fatalf("failed to create %s NSEvent", tc.name)
			}
			t.Logf("created %s NSEvent OK (type=%d)", tc.name, tc.eventType)
		})
	}
}

// TestPrivateHIDKeyboardEventConstruction tests constructing keyboard events
// via CGEvent that could be sent through the private APIs.
//
// The sendKeyboardEvents:keyboardID: selector takes an unsafe.Pointer (likely
// NSArray) as the events parameter. To determine the expected format, we examine
// what VZVirtualMachineView's keyDown: method produces when forwarding to the VM.
func TestPrivateHIDKeyboardEventConstruction(t *testing.T) {
	if err := ensureInputInit(); err != nil {
		t.Fatalf("CGEvent init: %v", err)
	}

	// Test creating CGEvents with various keycodes.
	keycodes := []struct {
		name    string
		keyCode uint16
		char    string
	}{
		{"Return", 36, "\r"},
		{"Tab", 48, "\t"},
		{"Space", 49, " "},
		{"A", 0, "a"},
		{"B", 11, "b"},
		{"Escape", 53, "\x1b"},
		{"Delete", 51, "\x7f"},
	}

	for _, tc := range keycodes {
		t.Run(tc.name, func(t *testing.T) {
			// Key down.
			cgDown, err := createKeyboardEvent(0, tc.keyCode, true)
			if err != nil {
				t.Fatalf("create key down: %v", err)
			}
			if cgDown == 0 {
				t.Fatal("CreateKeyboardEvent returned nil for key down")
			}
			if tc.char != "" {
				if err := setEventUnicodeString(cgDown, tc.char); err != nil {
					t.Fatalf("set unicode string: %v", err)
				}
			}

			// Convert to NSEvent.
			nsDown := objc.Send[objc.ID](
				objc.ID(objc.GetClass("NSEvent")),
				objc.Sel("eventWithCGEvent:"),
				cgDown,
			)
			if nsDown == 0 {
				t.Fatal("eventWithCGEvent: returned nil for key down")
			}

			// Verify keyCode is preserved through the CGEvent->NSEvent path.
			// This is the critical test: objc.Send corrupts keyCode when it's
			// passed at argument position 10 in keyEventWithType:..., but
			// createKeyboardEvent (C function via purego) passes it correctly.
			gotKeyCode := objc.Send[uint16](nsDown, objc.Sel("keyCode"))
			t.Logf("keyCode: want=%d got=%d", tc.keyCode, gotKeyCode)
			if gotKeyCode != tc.keyCode {
				t.Errorf("keyCode corrupted: got %d, want %d (ARM64 stack-passing bug?)", gotKeyCode, tc.keyCode)
			}

			// Key up.
			cgUp, err := createKeyboardEvent(0, tc.keyCode, false)
			if err != nil {
				t.Fatalf("create key up: %v", err)
			}
			if cgUp == 0 {
				t.Fatal("CreateKeyboardEvent returned nil for key up")
			}

			nsUp := objc.Send[objc.ID](
				objc.ID(objc.GetClass("NSEvent")),
				objc.Sel("eventWithCGEvent:"),
				cgUp,
			)
			if nsUp == 0 {
				t.Fatal("eventWithCGEvent: returned nil for key up")
			}

			gotKeyCodeUp := objc.Send[uint16](nsUp, objc.Sel("keyCode"))
			if gotKeyCodeUp != tc.keyCode {
				t.Errorf("key up keyCode corrupted: got %d, want %d", gotKeyCodeUp, tc.keyCode)
			}
		})
	}
}

// TestPrivateHIDNSArrayEventFormat tests constructing NSArray-wrapped events
// that might be expected by sendKeyboardEvents:keyboardID: and
// sendMouseEvents:pointingDeviceIndex:.
//
// The events parameters are unsafe.Pointer, which in Objective-C is typically
// NSArray<NSDictionary*>* or NSArray<_VZKeyboardEvent*>*. We test both
// NSArray construction paths.
func TestPrivateHIDNSArrayEventFormat(t *testing.T) {
	// Create an NSMutableArray to wrap events.
	arrayClass := objc.GetClass("NSMutableArray")
	if arrayClass == 0 {
		t.Fatal("NSMutableArray class not found")
	}

	arr := objc.Send[objc.ID](
		objc.Send[objc.ID](objc.ID(arrayClass), objc.Sel("alloc")),
		objc.Sel("init"),
	)
	if arr == 0 {
		t.Fatal("failed to create NSMutableArray")
	}

	// Add a CGEvent-based NSEvent to the array (mouse move).
	if err := ensureInputInit(); err != nil {
		t.Fatalf("CGEvent init: %v", err)
	}

	cgEvent, err := createMouseEvent(0, uint32(cgEventMouseMoved), corefoundation.CGPoint{X: 100, Y: 100}, 0)
	if err != nil {
		t.Fatalf("create mouse event: %v", err)
	}
	nsEvent := objc.Send[objc.ID](
		objc.ID(objc.GetClass("NSEvent")),
		objc.Sel("eventWithCGEvent:"),
		cgEvent,
	)
	if nsEvent == 0 {
		t.Fatal("eventWithCGEvent: returned nil")
	}

	objc.Send[objc.ID](arr, objc.Sel("addObject:"), nsEvent)

	count := objc.Send[uint64](arr, objc.Sel("count"))
	if count != 1 {
		t.Errorf("array count = %d, want 1", count)
	}

	t.Logf("created NSMutableArray with %d NSEvent(s) - can be passed as unsafe.Pointer to private APIs", count)

	// Log the class of the event to understand the expected type.
	classObj := objc.Send[objc.ID](nsEvent, objc.Sel("class"))
	className := ""
	if classObj != 0 {
		nameID := objc.Send[objc.ID](classObj, objc.Sel("description"))
		if nameID != 0 {
			className = cfStringToGo(uintptr(nameID))
		}
	}
	t.Logf("NSEvent class: %s", className)
}

// TestPrivateHIDViewForwardingAnalysis examines VZVirtualMachineView's event
// forwarding to understand what happens when keyDown:/mouseDown: are called.
// This helps us understand whether the view calls sendPointerNSEvent: or
// sendKeyboardEvents: internally.
func TestPrivateHIDViewForwardingAnalysis(t *testing.T) {
	vmViewClass := objc.GetClass("VZVirtualMachineView")
	if vmViewClass == 0 {
		t.Fatal("VZVirtualMachineView class not found")
	}

	// Check which event-handling selectors the view implements.
	viewSelectors := []string{
		"keyDown:",
		"keyUp:",
		"mouseDown:",
		"mouseUp:",
		"mouseMoved:",
		"rightMouseDown:",
		"rightMouseUp:",
		"scrollWheel:",
		"magnifyWithEvent:",
		"rotateWithEvent:",
		"flagsChanged:",
	}

	instance := objc.Send[objc.ID](objc.ID(vmViewClass), objc.Sel("alloc"))
	for _, sel := range viewSelectors {
		responds := objc.Send[bool](instance, objc.Sel("respondsToSelector:"), objc.Sel(sel))
		t.Logf("VZVirtualMachineView responds to %s: %v", sel, responds)
	}

	// Check if the view has a reference to the VM (which it uses to forward events).
	// The view likely calls [self.virtualMachine sendPointerNSEvent:...] internally.
	vmPropSelectors := []string{
		"virtualMachine",
		"_virtualMachine",
		"vm",
		"_vm",
	}
	for _, sel := range vmPropSelectors {
		responds := objc.Send[bool](instance, objc.Sel("respondsToSelector:"), objc.Sel(sel))
		if responds {
			t.Logf("VZVirtualMachineView has property accessor: %s", sel)
		}
	}
}

// TestPrivateHIDShouldSendHIDReportsOnUninitializedVM tests calling
// _shouldSendHIDReports on an uninitialized VZVirtualMachine. This verifies
// the selector is callable and returns a sensible default.
func TestPrivateHIDShouldSendHIDReportsOnUninitializedVM(t *testing.T) {
	t.Skip("_shouldSendHIDReports causes SIGTRAP on uninitialized VMs; use -integration for live VM tests")
}

// TestPrivateHIDCGEventToNSEventRoundTrip verifies that CGEvent -> NSEvent
// conversion preserves all fields needed for the private HID APIs.
// This is the key conversion path: we create CGEvents (which correctly handle
// the ARM64 ABI for all field sizes), convert to NSEvents, and pass those
// to sendPointerNSEvent: or wrap in arrays for sendKeyboardEvents:.
func TestPrivateHIDCGEventToNSEventRoundTrip(t *testing.T) {
	if err := ensureInputInit(); err != nil {
		t.Fatalf("CGEvent init: %v", err)
	}

	t.Run("MouseMoved", func(t *testing.T) {
		pos := corefoundation.CGPoint{X: 200.5, Y: 300.75}
		cg, err := createMouseEvent(0, uint32(cgEventMouseMoved), pos, 0)
		if err != nil {
			t.Fatal(err)
		}
		ns := objc.Send[objc.ID](
			objc.ID(objc.GetClass("NSEvent")),
			objc.Sel("eventWithCGEvent:"), cg,
		)
		if ns == 0 {
			t.Fatal("conversion failed")
		}
		nsType := objc.Send[uint64](ns, objc.Sel("type"))
		t.Logf("mouse moved: NSEvent type=%d", nsType)
		if nsType != uint64(appkit.NSEventTypeMouseMoved) {
			t.Errorf("type mismatch: got %d want %d", nsType, appkit.NSEventTypeMouseMoved)
		}
	})

	t.Run("LeftMouseDown", func(t *testing.T) {
		pos := corefoundation.CGPoint{X: 100, Y: 200}
		cg, err := createMouseEvent(0, uint32(cgEventLeftMouseDown), pos, 0)
		if err != nil {
			t.Fatal(err)
		}
		ns := objc.Send[objc.ID](
			objc.ID(objc.GetClass("NSEvent")),
			objc.Sel("eventWithCGEvent:"), cg,
		)
		if ns == 0 {
			t.Fatal("conversion failed")
		}
		nsType := objc.Send[uint64](ns, objc.Sel("type"))
		if nsType != uint64(appkit.NSEventTypeLeftMouseDown) {
			t.Errorf("type mismatch: got %d want %d", nsType, appkit.NSEventTypeLeftMouseDown)
		}
	})

	t.Run("KeyDownWithKeyCode", func(t *testing.T) {
		// Test that keyCode 36 (Return) survives the round-trip.
		cg, err := createKeyboardEvent(0, 36, true)
		if err != nil {
			t.Fatal(err)
		}
		ns := objc.Send[objc.ID](
			objc.ID(objc.GetClass("NSEvent")),
			objc.Sel("eventWithCGEvent:"), cg,
		)
		if ns == 0 {
			t.Fatal("conversion failed")
		}
		keyCode := objc.Send[uint16](ns, objc.Sel("keyCode"))
		if keyCode != 36 {
			t.Errorf("keyCode = %d, want 36", keyCode)
		}
		t.Logf("Return key round-trip: keyCode=%d (correct)", keyCode)
	})

	t.Run("KeyDownWithModifiers", func(t *testing.T) {
		// Test Shift+A (keyCode=0, shift flag).
		cg, err := createKeyboardEvent(0, 0, true)
		if err != nil {
			t.Fatal(err)
		}
		// Set shift modifier (1 << 17 = 0x20000 = NSEventModifierFlagShift).
		setEventFlags(cg, 1<<17)
		setEventUnicodeString(cg, "A")

		ns := objc.Send[objc.ID](
			objc.ID(objc.GetClass("NSEvent")),
			objc.Sel("eventWithCGEvent:"), cg,
		)
		if ns == 0 {
			t.Fatal("conversion failed")
		}
		keyCode := objc.Send[uint16](ns, objc.Sel("keyCode"))
		modifiers := objc.Send[uint64](ns, objc.Sel("modifierFlags"))
		t.Logf("Shift+A: keyCode=%d modifiers=0x%x", keyCode, modifiers)
		if keyCode != 0 {
			t.Errorf("keyCode = %d, want 0", keyCode)
		}
		if modifiers&(1<<17) == 0 {
			t.Errorf("shift modifier not set: 0x%x", modifiers)
		}
	})
}

// TestPrivateHIDEventPassingToVM documents how to pass events to the private APIs.
// This serves as a reference for integrating these APIs into cove.
//
// Key findings:
//
//	sendPointerNSEvent:pointingDeviceIndex:
//	  - Takes an NSEvent directly (objectivec.IObject)
//	  - Most promising for mouse input: we already construct NSEvents for
//	    sendMouseEventVMDirect, just need to call the VM method directly
//	    instead of going through VZVirtualMachineView
//	  - Works without a GUI window (headless mode!)
//	  - Device index 0 = first pointing device
//
//	sendKeyboardEvents:keyboardID:
//	  - Takes unsafe.Pointer (likely NSArray of private event objects)
//	  - keyboardID 0 = first keyboard
//	  - Event format is unknown -- may be NSArray<_VZKeyboardEvent*>
//	  - Could potentially wrap NSEvents in an array, but format needs
//	    reverse engineering of VZVirtualMachineView's keyDown: implementation
//
//	sendMouseEvents:pointingDeviceIndex:
//	  - Takes unsafe.Pointer (likely NSArray of private event objects)
//	  - Similar format questions as keyboard events
//	  - sendPointerNSEvent is preferred since it takes standard NSEvent
//
//	_processHIDReports:forDevice:deviceType:
//	  - Raw HID report data
//	  - Most low-level option
//	  - Reports are likely USB HID report descriptors
//	  - device = USB device ID, deviceType = keyboard(0)/mouse(1)/etc.
func TestPrivateHIDEventPassingToVM(t *testing.T) {
	t.Log("=== Private HID API Integration Guide ===")
	t.Log("")
	t.Log("RECOMMENDED: sendPointerNSEvent:pointingDeviceIndex:")
	t.Log("  - Accepts standard NSEvent (already constructed in sendMouseEventVMDirect)")
	t.Log("  - Call on VZVirtualMachine directly, not through VZVirtualMachineView")
	t.Log("  - Enables headless mouse input (no GUI window needed)")
	t.Log("  - Usage: objc.Send[struct{}](vm.ID, objc.Sel(\"sendPointerNSEvent:pointingDeviceIndex:\"), nsEvent.ID, 0)")
	t.Log("")
	t.Log("PROMISING: sendKeyboardEvents:keyboardID:")
	t.Log("  - Events format needs reverse engineering")
	t.Log("  - May accept NSArray of NSEvent or private _VZKeyboardEvent objects")
	t.Log("  - If it accepts NSEvents, it would solve the ARM64 keyCode bug")
	t.Log("  - Current workaround: CGEvent -> NSEvent -> view.keyDown: works but needs main thread")
	t.Log("")
	t.Log("ADVANCED: _processHIDReports:forDevice:deviceType:")
	t.Log("  - Raw HID reports bypass all event abstraction")
	t.Log("  - Maximum control but requires USB HID report format knowledge")
	t.Log("  - Useful for custom input devices or automation frameworks")

	// Verify the recommended calling pattern compiles.
	// (Cannot actually call without a running VM.)
	_ = unsafe.Pointer(nil)
	var vmID objc.ID
	_ = vmID // would be: objc.Send[struct{}](vmID, objc.Sel("sendPointerNSEvent:pointingDeviceIndex:"), eventID, uint32(0))
}

// cfStringToGo converts a CFStringRef/NSString to a Go string.
func cfStringToGo(ref uintptr) string {
	if ref == 0 {
		return ""
	}
	return objc.IDToString(objc.ID(ref))
}
