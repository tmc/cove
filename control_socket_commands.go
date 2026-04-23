// control_socket_commands.go - Key code constants and modifier flags for VM control socket.
//
// Message types are defined in proto/controlpb/control.proto.
package main

// Common macOS virtual key codes
const (
	KeyCodeA          uint16 = 0
	KeyCodeQ          uint16 = 12
	KeyCodeReturn     uint16 = 36
	KeyCodeTab        uint16 = 48
	KeyCodeSpace      uint16 = 49
	KeyCodeDelete     uint16 = 51
	KeyCodeEscape     uint16 = 53
	KeyCodeCommand    uint16 = 55
	KeyCodeShift      uint16 = 56
	KeyCodeCapsLock   uint16 = 57
	KeyCodeOption     uint16 = 58
	KeyCodeControl    uint16 = 59
	KeyCodeF5         uint16 = 96
	KeyCodeLeftArrow  uint16 = 123
	KeyCodeRightArrow uint16 = 124
	KeyCodeDownArrow  uint16 = 125
	KeyCodeUpArrow    uint16 = 126
)

// Modifier flags for keyboard events
const (
	ModifierShift   uint = 1 << 17 // NSEventModifierFlagShift
	ModifierControl uint = 1 << 18 // NSEventModifierFlagControl
	ModifierOption  uint = 1 << 19 // NSEventModifierFlagOption
	ModifierCommand uint = 1 << 20 // NSEventModifierFlagCommand
)
