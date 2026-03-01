package main

import (
	"fmt"
	"os"
	"sync"

	"github.com/ebitengine/purego"
	"github.com/tmc/apple/corefoundation"
)

// Manual bindings for CoreGraphics functions missing from generated code
// The generated bindings have incorrect types for some CGEvent functions

var (
	cgEventCreateMouseEvent         func(source uintptr, mouseType uint32, mouseCursorPosition corefoundation.CGPoint, mouseButton uint32) uintptr
	cgEventCreateKeyboardEvent      func(source uintptr, virtualKey uint16, keyDown bool) uintptr
	cgEventPost                     func(tap uint32, event uintptr)
	cgEventPostToPid                func(pid int32, event uintptr)
	cgEventSetFlags                 func(event uintptr, flags uint64)
	cgEventKeyboardSetUnicodeString func(event uintptr, stringLength uint64, unicodeString *uint16)

	cgInitOnce sync.Once
	cgInitErr  error
)

func ensureCGInit() error {
	cgInitOnce.Do(func() {
		appServices, err := purego.Dlopen("/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			cgInitErr = fmt.Errorf("load ApplicationServices: %w", err)
			return
		}
		purego.RegisterLibFunc(&cgEventCreateMouseEvent, appServices, "CGEventCreateMouseEvent")
		purego.RegisterLibFunc(&cgEventCreateKeyboardEvent, appServices, "CGEventCreateKeyboardEvent")

		coreGraphics, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			cgInitErr = fmt.Errorf("load CoreGraphics: %w", err)
			return
		}
		purego.RegisterLibFunc(&cgEventPost, coreGraphics, "CGEventPost")
		purego.RegisterLibFunc(&cgEventPostToPid, coreGraphics, "CGEventPostToPid")
		purego.RegisterLibFunc(&cgEventSetFlags, coreGraphics, "CGEventSetFlags")
		purego.RegisterLibFunc(&cgEventKeyboardSetUnicodeString, coreGraphics, "CGEventKeyboardSetUnicodeString")
	})
	return cgInitErr
}

func CGEventCreateMouseEvent(source uintptr, mouseType uint32, position corefoundation.CGPoint, mouseButton uint32) (uintptr, error) {
	if err := ensureCGInit(); err != nil {
		return 0, err
	}
	return cgEventCreateMouseEvent(source, mouseType, position, mouseButton), nil
}

// CGEventCreateKeyboardEvent creates a keyboard event. virtualKey is the macOS virtual key code (e.g. 36=Return, 48=Tab)
func CGEventCreateKeyboardEvent(source uintptr, virtualKey uint16, keyDown bool) (uintptr, error) {
	if err := ensureCGInit(); err != nil {
		return 0, err
	}
	return cgEventCreateKeyboardEvent(source, virtualKey, keyDown), nil
}

// CGEventPostToSelf posts an event to this process.
// Keystrokes go to the VM window, not whatever app the user has focused.
func CGEventPostToSelf(event uintptr) error {
	if err := ensureCGInit(); err != nil {
		return err
	}
	cgEventPostToPid(int32(os.Getpid()), event)
	return nil
}

// CGEventSetFlags sets the flags (modifiers) on an event
func CGEventSetFlags(event uintptr, flags uint64) error {
	if err := ensureCGInit(); err != nil {
		return err
	}
	cgEventSetFlags(event, flags)
	return nil
}

// CGEventKeyboardSetUnicodeString sets the Unicode string on a keyboard event.
// This allows typing arbitrary characters that don't have a direct keycode.
func CGEventKeyboardSetUnicodeString(event uintptr, s string) error {
	if err := ensureCGInit(); err != nil {
		return err
	}
	if len(s) == 0 {
		return nil
	}
	// Convert string to UTF-16
	runes := []rune(s)
	utf16 := make([]uint16, len(runes))
	for i, r := range runes {
		utf16[i] = uint16(r)
	}
	cgEventKeyboardSetUnicodeString(event, uint64(len(utf16)), &utf16[0])
	return nil
}

// TypeCharacter types a single character using CGEvent with Unicode string support.
// Events are posted to our own process so they reach the VM window.
func TypeCharacter(char rune) error {
	if err := ensureCGInit(); err != nil {
		return err
	}
	// Create a key down event with a dummy keycode (0)
	// The actual character comes from the Unicode string
	eventDown, err := CGEventCreateKeyboardEvent(0, 0, true)
	if err != nil {
		return err
	}
	if eventDown == 0 {
		return fmt.Errorf("create key down event")
	}
	CGEventKeyboardSetUnicodeString(eventDown, string(char))
	CGEventPostToSelf(eventDown)

	// Create key up event
	eventUp, err := CGEventCreateKeyboardEvent(0, 0, false)
	if err != nil {
		return err
	}
	if eventUp == 0 {
		return fmt.Errorf("create key up event")
	}
	CGEventKeyboardSetUnicodeString(eventUp, string(char))
	CGEventPostToSelf(eventUp)
	return nil
}

// typeCharacterToSystem types a single character by posting CGEvents through
// the system-level HID event tap (kCGHIDEventTap). Unlike CGEventPostToPid,
// this routes events through the window server to the focused window, which
// is required for VZVirtualMachineView to properly handle keyboard input.
func typeCharacterToSystem(char rune) error {
	if err := ensureCGInit(); err != nil {
		return err
	}
	eventDown, err := CGEventCreateKeyboardEvent(0, 0, true)
	if err != nil {
		return err
	}
	if eventDown == 0 {
		return fmt.Errorf("create key down event")
	}
	CGEventKeyboardSetUnicodeString(eventDown, string(char))
	cgEventPost(kCGHIDEventTap, eventDown)

	eventUp, err := CGEventCreateKeyboardEvent(0, 0, false)
	if err != nil {
		return err
	}
	if eventUp == 0 {
		return fmt.Errorf("create key up event")
	}
	CGEventKeyboardSetUnicodeString(eventUp, string(char))
	cgEventPost(kCGHIDEventTap, eventUp)
	return nil
}

const (
	kCGHIDEventTap         = 0
	kCGEventNull           = 0
	kCGEventLeftMouseDown  = 1
	kCGEventLeftMouseUp    = 2
	kCGEventRightMouseDown = 3
	kCGEventRightMouseUp   = 4
	kCGEventMouseMoved     = 5
	kCGEventKeyDown        = 10
	kCGEventKeyUp          = 11
)
