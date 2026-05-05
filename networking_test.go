package main

import "testing"

func TestParseNetworkModeExplicitModes(t *testing.T) {
	tests := []struct {
		name string
		in   string
		mode string
	}{
		{name: "empty default", in: "", mode: string(NetworkModeNAT)},
		{name: "nat", in: "nat", mode: string(NetworkModeNAT)},
		{name: "net spelling", in: "HOST-ONLY", mode: string(NetworkModeHostOnly)},
		{name: "none", in: "none", mode: string(NetworkModeNone)},
		{name: "filehandle", in: "filehandle", mode: string(NetworkModeFileHandle)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseNetworkMode(tt.in)
			if err != nil {
				t.Fatalf("ParseNetworkMode(%q): %v", tt.in, err)
			}
			if string(got.Mode) != tt.mode {
				t.Fatalf("mode = %q, want %q", got.Mode, tt.mode)
			}
		})
	}
}

func TestParseNetworkModeRejectsBareBridged(t *testing.T) {
	if _, err := ParseNetworkMode("bridged"); err == nil {
		t.Fatal("ParseNetworkMode(bridged) succeeded, want error")
	}
}
