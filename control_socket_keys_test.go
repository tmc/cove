package main

import "testing"

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
