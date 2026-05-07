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

	"github.com/tmc/vz-macos/internal/controlserver"
)

func init() {
	controlserver.SetInputBridgeRuntime(controlserver.InputBridgeRuntime{
		CreateMouseEvent: func(source uint64, eventType uint32, position corefoundation.CGPoint, mouseButton uint32) (uintptr, error) {
			return createMouseEvent(uintptr(source), eventType, position, mouseButton)
		},
		CreateKeyboardEvent: func(source uint64, keyCode uint16, keyDown bool) (uintptr, error) {
			return createKeyboardEvent(uintptr(source), keyCode, keyDown)
		},
		PostEvent: postEvent,
		SetEventUnicodeString: func(event uintptr, s string) {
			_ = setEventUnicodeString(event, s)
		},
		SetEventFlags: func(event uintptr, flags uint64) {
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
