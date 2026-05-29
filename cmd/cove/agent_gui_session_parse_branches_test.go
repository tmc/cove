package main

import "testing"

func TestParseLoginctlShowGUISessionRejections(t *testing.T) {
	tests := []struct {
		name string
		out  string
	}{
		{"inactive", "Name=desk\nState=closing\nType=wayland\n"},
		{"unsupportedType", "Name=desk\nState=active\nType=tty\n"},
		{"missingUser", "State=active\nType=x11\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, ok := parseLoginctlShowGUISession("1", tt.out); ok {
				t.Fatalf("session unexpectedly accepted")
			}
		})
	}
}

func TestParseLoginctlShowGUISessionFallsBackToUser(t *testing.T) {
	got, ok := parseLoginctlShowGUISession("7", "User=1000\nSeat=seat0\nState=active\nType=x11\n")
	if !ok {
		t.Fatal("no session found")
	}
	if got.User != "1000" || got.Kind != "x11" {
		t.Fatalf("got = %#v, want User=1000 Kind=x11", got)
	}
}
