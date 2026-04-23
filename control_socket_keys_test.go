package main

import (
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
