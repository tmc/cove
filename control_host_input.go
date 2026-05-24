// control_host_input.go — runtime-hook wiring for the
// controlserver.InputBridge.
//
// The controlserver package's input bridge stays free of package-main
// globals; the back-channels it needs (CGEvent helpers, UI-thread
// dispatch, modifier helpers) are installed once at process start
// through SetInputBridgeRuntime.

package main

import (
	"github.com/tmc/apple/corefoundation"

	"github.com/tmc/cove/internal/controlserver"
)

func init() {
	controlserver.SetInputBridgeRuntime(controlserver.InputBridgeRuntime{
		CreateMouseEvent: func(source uint64, eventType uint32, position corefoundation.CGPoint, mouseButton uint32) (corefoundation.CFTypeRef, error) {
			return createMouseEvent(uintptr(source), eventType, position, mouseButton)
		},
		CreateKeyboardEvent: func(source uint64, keyCode uint16, keyDown bool) (corefoundation.CFTypeRef, error) {
			return createKeyboardEvent(uintptr(source), keyCode, keyDown)
		},
		PostEvent: postEvent,
		SetEventUnicodeString: func(event corefoundation.CFTypeRef, s string) {
			_ = setEventUnicodeString(event, s)
		},
		SetEventFlags: func(event corefoundation.CFTypeRef, flags uint64) {
			_ = setEventFlags(event, flags)
		},
		RunOnUIThreadSync:   runOnUIThreadSync,
		AllowHIDKeyboard:    allowHIDKeyboard,
		ModifierKeySequence: modifierKeySequence,
		ModifierShift:       uint32(ModifierShift),

		CGEventMouseMoved:     cgEventMouseMoved,
		CGEventLeftMouseDown:  cgEventLeftMouseDown,
		CGEventRightMouseDown: cgEventRightMouseDown,
		CGEventLeftMouseUp:    cgEventLeftMouseUp,
		CGEventRightMouseUp:   cgEventRightMouseUp,
		CGHIDEventTap:         cgHIDEventTap,
	})
}
