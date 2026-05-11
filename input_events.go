// input_events.go — CGEvent keyboard and mouse event helpers.
//
// Wraps CoreGraphics CGEvent functions via purego (cgo-free) for creating
// and posting keyboard and mouse events. Events can be posted to a specific
// process (a VM window) or to the system HID event tap.
package main

import (
	"fmt"
	"sync"

	"github.com/ebitengine/purego"
	"github.com/tmc/apple/corefoundation"
)

// CGEvent mouse and keyboard event types.
const (
	cgEventLeftMouseDown  = 1
	cgEventLeftMouseUp    = 2
	cgEventRightMouseDown = 3
	cgEventRightMouseUp   = 4
	cgEventMouseMoved     = 5
	cgHIDEventTap         = 0
)

var (
	cgEventCreateMouseEvent         func(source uintptr, mouseType uint32, mouseCursorPosition corefoundation.CGPoint, mouseButton uint32) corefoundation.CFTypeRef
	cgEventCreateKeyboardEvent      func(source uintptr, virtualKey uint16, keyDown bool) corefoundation.CFTypeRef
	cgEventPost                     func(tap uint32, event corefoundation.CFTypeRef)
	cgEventSetFlags                 func(event corefoundation.CFTypeRef, flags uint64)
	cgEventKeyboardSetUnicodeString func(event corefoundation.CFTypeRef, stringLength uint64, unicodeString *uint16)

	inputInitOnce sync.Once
	inputInitErr  error
)

func inputEnsureInit() error {
	inputInitOnce.Do(func() {
		appServices, err := purego.Dlopen("/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			inputInitErr = fmt.Errorf("load ApplicationServices: %w", err)
			return
		}
		purego.RegisterLibFunc(&cgEventCreateMouseEvent, appServices, "CGEventCreateMouseEvent")
		purego.RegisterLibFunc(&cgEventCreateKeyboardEvent, appServices, "CGEventCreateKeyboardEvent")

		coreGraphics, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			inputInitErr = fmt.Errorf("load CoreGraphics: %w", err)
			return
		}
		purego.RegisterLibFunc(&cgEventPost, coreGraphics, "CGEventPost")
		purego.RegisterLibFunc(&cgEventSetFlags, coreGraphics, "CGEventSetFlags")
		purego.RegisterLibFunc(&cgEventKeyboardSetUnicodeString, coreGraphics, "CGEventKeyboardSetUnicodeString")
	})
	return inputInitErr
}

// createMouseEvent creates a mouse event at the given position.
// mouseType is one of the cgEvent* mouse constants; mouseButton is 0 for left, 1 for right.
func createMouseEvent(source uintptr, mouseType uint32, position corefoundation.CGPoint, mouseButton uint32) (corefoundation.CFTypeRef, error) {
	if err := inputEnsureInit(); err != nil {
		return nil, err
	}
	return cgEventCreateMouseEvent(source, mouseType, position, mouseButton), nil
}

// createKeyboardEvent creates a keyboard event. virtualKey is the macOS
// virtual key code (e.g. 36=Return, 48=Tab).
func createKeyboardEvent(source uintptr, virtualKey uint16, keyDown bool) (corefoundation.CFTypeRef, error) {
	if err := inputEnsureInit(); err != nil {
		return nil, err
	}
	return cgEventCreateKeyboardEvent(source, virtualKey, keyDown), nil
}

// postEvent posts an event to the given HID event tap location.
func postEvent(tap uint32, event corefoundation.CFTypeRef) error {
	if err := inputEnsureInit(); err != nil {
		return err
	}
	cgEventPost(tap, event)
	return nil
}

// setEventFlags sets the modifier flags (shift, control, etc.) on an event.
func setEventFlags(event corefoundation.CFTypeRef, flags uint64) error {
	if err := inputEnsureInit(); err != nil {
		return err
	}
	cgEventSetFlags(event, flags)
	return nil
}

// setEventUnicodeString sets the Unicode string on a keyboard event,
// enabling arbitrary characters that don't have a direct keycode.
func setEventUnicodeString(event corefoundation.CFTypeRef, s string) error {
	if err := inputEnsureInit(); err != nil {
		return err
	}
	if len(s) == 0 {
		return nil
	}
	runes := []rune(s)
	utf16 := make([]uint16, len(runes))
	for i, r := range runes {
		utf16[i] = uint16(r)
	}
	cgEventKeyboardSetUnicodeString(event, uint64(len(utf16)), &utf16[0])
	return nil
}
