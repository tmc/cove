package main

import "testing"

func TestControlServerWindowTitle(t *testing.T) {
	var s ControlServer
	s.SetWindowTitleBase("macOS VM - test")
	s.SetWindowTitleState("Running")
	s.SetWindowTitleLabel("SIP disable / Recovery")

	if got, want := s.WindowTitle(), "macOS VM - test — Running — SIP disable / Recovery"; got != want {
		t.Fatalf("WindowTitle() = %q, want %q", got, want)
	}

	s.SetWindowTitleLabel("")
	if got, want := s.WindowTitle(), "macOS VM - test — Running"; got != want {
		t.Fatalf("WindowTitle() after clear = %q, want %q", got, want)
	}
}
