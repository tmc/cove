package utils

import (
	"github.com/ebitengine/purego"
	"github.com/tmc/appledocs/generated/corefoundation"
)

// Manual bindings for CoreGraphics functions missing from generated code
// The generated bindings have incorrect types for some CGEvent functions

var (
	cgEventCreateMouseEvent         func(source uintptr, mouseType uint32, mouseCursorPosition corefoundation.CGPoint, mouseButton uint32) uintptr
	cgEventCreateKeyboardEvent      func(source uintptr, virtualKey uint16, keyDown bool) uintptr
	cgEventPost                     func(tap uint32, event uintptr)
	cgEventSetFlags                 func(event uintptr, flags uint64)
	cgEventKeyboardSetUnicodeString func(event uintptr, stringLength uint64, unicodeString *uint16)
)

func init() {
	appServices, err := purego.Dlopen("/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		panic("failed to load ApplicationServices: " + err.Error())
	}
	purego.RegisterLibFunc(&cgEventCreateMouseEvent, appServices, "CGEventCreateMouseEvent")
	purego.RegisterLibFunc(&cgEventCreateKeyboardEvent, appServices, "CGEventCreateKeyboardEvent")
	purego.RegisterLibFunc(&cgEventPost, appServices, "CGEventPostToPid")

	coreGraphics, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		panic("failed to load CoreGraphics: " + err.Error())
	}
	purego.RegisterLibFunc(&cgEventPost, coreGraphics, "CGEventPost")
	purego.RegisterLibFunc(&cgEventSetFlags, coreGraphics, "CGEventSetFlags")
	purego.RegisterLibFunc(&cgEventKeyboardSetUnicodeString, coreGraphics, "CGEventKeyboardSetUnicodeString")
}

// Create a key down event with a dummy keycode (0)
// The actual character comes from the Unicode string

// Create key up event

// CGEvent type constants (exported for cross-package use)
const (
	KCGHIDEventTap         = 0
	KCGEventNull           = 0
	KCGEventLeftMouseDown  = 1
	KCGEventLeftMouseUp    = 2
	KCGEventRightMouseDown = 3
	KCGEventRightMouseUp   = 4
	KCGEventMouseMoved     = 5
	KCGEventKeyDown        = 10
	KCGEventKeyUp          = 11
)

// Keep unexported aliases for internal use
const (
	kCGHIDEventTap = KCGHIDEventTap
)
