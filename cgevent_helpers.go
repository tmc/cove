// cgevent_helpers.go - CGEvent input helpers.
//
// Delegates to github.com/tmc/apple/x/vzkit/input for the implementation.
// Provides backwards-compatible wrappers for existing callsites.
package main

import (
	"github.com/tmc/apple/corefoundation"
	"github.com/tmc/apple/x/vzkit/input"
)

// CGEvent constants for backwards compatibility.
const (
	kCGHIDEventTap         = input.HIDEventTap
	kCGEventNull           = input.EventNull
	kCGEventLeftMouseDown  = input.EventLeftMouseDown
	kCGEventLeftMouseUp    = input.EventLeftMouseUp
	kCGEventRightMouseDown = input.EventRightMouseDown
	kCGEventRightMouseUp   = input.EventRightMouseUp
	kCGEventMouseMoved     = input.EventMouseMoved
	kCGEventKeyDown        = input.EventKeyDown
	kCGEventKeyUp          = input.EventKeyUp
)

func ensureCGInit() error {
	// Trigger lazy init in the input package by creating a throwaway event.
	_, err := input.CreateKeyboardEvent(0, 0, false)
	return err
}

func CGEventCreateMouseEvent(source uintptr, mouseType uint32, position corefoundation.CGPoint, mouseButton uint32) (uintptr, error) {
	return input.CreateMouseEvent(source, mouseType, position, mouseButton)
}

func CGEventCreateKeyboardEvent(source uintptr, virtualKey uint16, keyDown bool) (uintptr, error) {
	return input.CreateKeyboardEvent(source, virtualKey, keyDown)
}

func CGEventPostToSelf(event uintptr) error {
	return input.PostToSelf(event)
}

func CGEventSetFlags(event uintptr, flags uint64) error {
	return input.SetFlags(event, flags)
}

func CGEventKeyboardSetUnicodeString(event uintptr, s string) error {
	return input.SetUnicodeString(event, s)
}

func TypeCharacter(char rune) error {
	return input.TypeCharacter(char)
}
