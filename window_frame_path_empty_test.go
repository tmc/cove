package main

import "testing"

func TestWindowDisplayPlacementPathEmptyVMDir(t *testing.T) {
	prev := vmDir
	t.Cleanup(func() { vmDir = prev })
	vmDir = ""

	got := windowDisplayPlacementPath("autosave-name")
	want := "window-display-autosave-name.json"
	if got != want {
		t.Fatalf("got = %q, want %q", got, want)
	}
}

func TestWindowDisplayPlacementPathWhitespaceVMDir(t *testing.T) {
	prev := vmDir
	t.Cleanup(func() { vmDir = prev })
	vmDir = "   "

	got := windowDisplayPlacementPath("autosave-name")
	want := "window-display-autosave-name.json"
	if got != want {
		t.Fatalf("got = %q, want %q", got, want)
	}
}
