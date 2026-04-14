package main

import "testing"

func TestAllowExperimentalHIDKeyboard(t *testing.T) {
	t.Setenv("VZ_MACOS_EXPERIMENTAL_HID_KEYBOARD", "")
	if allowExperimentalHIDKeyboard() {
		t.Fatal("HID keyboard path should be disabled by default")
	}

	t.Setenv("VZ_MACOS_EXPERIMENTAL_HID_KEYBOARD", "1")
	if !allowExperimentalHIDKeyboard() {
		t.Fatal("HID keyboard path should be enabled when explicitly requested")
	}
}
