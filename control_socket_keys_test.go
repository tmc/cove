package main

import (
	"strings"
	"testing"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

func TestModifierKeySequenceOrder(t *testing.T) {
	flags := uint32(0)
	flags |= 1 << 18 // control
	flags |= 1 << 19 // option
	flags |= 1 << 17 // shift
	flags |= 1 << 20 // command

	got := modifierKeySequence(flags)
	want := []uint32{59, 58, 56, 55}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got[%d] = %d, want %d", i, got[i], want[i])
		}
	}
}

func TestModifierKeySequenceCommandShift(t *testing.T) {
	flags := uint32(ModifierShift | ModifierCommand)
	got := modifierKeySequence(flags)
	want := []uint32{56, 55}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got[%d] = %d, want %d", i, got[i], want[i])
		}
	}
}

func TestKeyboardEventUnicodeString(t *testing.T) {
	tests := []struct {
		name string
		cmd  *controlpb.KeyCommand
		want string
	}{
		{
			name: "return",
			cmd:  &controlpb.KeyCommand{KeyCode: 36},
			want: "\r",
		},
		{
			name: "tab",
			cmd:  &controlpb.KeyCommand{KeyCode: 48},
			want: "\t",
		},
		{
			name: "delete",
			cmd:  &controlpb.KeyCommand{KeyCode: 51},
			want: "\x7f",
		},
		{
			name: "escape",
			cmd:  &controlpb.KeyCommand{KeyCode: 53},
			want: "\x1b",
		},
		{
			name: "space",
			cmd:  &controlpb.KeyCommand{KeyCode: 49},
			want: " ",
		},
		{
			name: "explicit character wins",
			cmd:  &controlpb.KeyCommand{KeyCode: 36, Character: "\r"},
			want: "\r",
		},
		{
			name: "printable character uses explicit rune",
			cmd:  &controlpb.KeyCommand{KeyCode: 0, Character: "a"},
			want: "a",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := keyboardEventUnicodeString(tc.cmd); got != tc.want {
				t.Fatalf("keyboardEventUnicodeString() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTypeTextRequiresGUIMode(t *testing.T) {
	// Headless VMs have no vmView/window; typeText must fail fast rather
	// than silently consume the characters (ROADMAP bug #24).
	cs := &ControlServer{}
	resp := cs.typeText(&controlpb.TextCommand{Text: "hi"})
	if resp == nil || resp.Success {
		t.Fatalf("typeText() = %+v, want GUI-mode error", resp)
	}
	if !strings.Contains(resp.Error, "requires GUI mode") {
		t.Fatalf("error = %q, want GUI-mode guard", resp.Error)
	}
}

func TestSendKeyEventPrimitiveFramebufferDefaultNoHostFallback(t *testing.T) {
	t.Setenv("VZ_MACOS_EXPERIMENTAL_HID_KEYBOARD", "")
	t.Setenv("VZ_MACOS_DISABLE_HID_KEYBOARD", "")
	called := false
	sendKeyEventPrivateHook = func(_ *ControlServer, cmd *controlpb.KeyCommand) *controlpb.ControlResponse {
		called = true
		if cmd.KeyCode != 17 || !cmd.KeyDown || cmd.Character != "t" {
			t.Fatalf("private hook cmd = %+v", cmd)
		}
		return &controlpb.ControlResponse{Success: true}
	}
	defer func() { sendKeyEventPrivateHook = nil }()

	cs := &ControlServer{}
	cs.setInputBackend(automationBackendFramebuffer)

	resp := cs.sendKeyEventPrimitive(&controlpb.KeyCommand{KeyCode: 17, KeyDown: true, Character: "t"})
	if resp == nil {
		t.Fatalf("sendKeyEventPrimitive() = nil, want response")
	}
	if !resp.Success {
		t.Fatalf("sendKeyEventPrimitive() success = false, error = %q", resp.Error)
	}
	if !called {
		t.Fatalf("private keyboard path was not called")
	}
	if strings.Contains(resp.Error, "refusing host window-server fallback") {
		t.Fatalf("error = %q, want no host fallback refusal", resp.Error)
	}
	if strings.Contains(resp.Error, "VZ_MACOS_EXPERIMENTAL_HID_KEYBOARD"+"=1") {
		t.Fatalf("error = %q, want no legacy enable instruction", resp.Error)
	}
}

func TestSendKeyEventPrimitiveFramebufferOptOutRefuses(t *testing.T) {
	t.Setenv("VZ_MACOS_EXPERIMENTAL_HID_KEYBOARD", "")
	t.Setenv("VZ_MACOS_DISABLE_HID_KEYBOARD", "1")
	sendKeyEventPrivateHook = func(_ *ControlServer, _ *controlpb.KeyCommand) *controlpb.ControlResponse {
		t.Fatalf("private keyboard path called despite opt-out")
		return nil
	}
	defer func() { sendKeyEventPrivateHook = nil }()

	cs := &ControlServer{}
	cs.setInputBackend(automationBackendFramebuffer)

	resp := cs.sendKeyEventPrimitive(&controlpb.KeyCommand{KeyCode: 17, KeyDown: true, Character: "t"})
	if resp == nil {
		t.Fatalf("sendKeyEventPrimitive() = nil, want error response")
	}
	if resp.Success {
		t.Fatalf("sendKeyEventPrimitive() unexpectedly succeeded")
	}
	if !strings.Contains(resp.Error, "disabled by VZ_MACOS_DISABLE_HID_KEYBOARD") {
		t.Fatalf("error = %q, want HID keyboard disabled error", resp.Error)
	}
	if strings.Contains(resp.Error, "refusing host window-server fallback") {
		t.Fatalf("error = %q, want no host fallback refusal", resp.Error)
	}
}
