package main

import (
	"reflect"
	"testing"
)

func TestGuestIPProbeArgsByGuestOS(t *testing.T) {
	if got, want := guestIPProbeArgs(false), []string{"ipconfig", "getifaddr", "en0"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("guestIPProbeArgs(false) = %v, want %v", got, want)
	}
	got := guestIPProbeArgs(true)
	if len(got) != 3 || got[0] != "sh" || got[1] != "-lc" {
		t.Fatalf("guestIPProbeArgs(true) = %v, want shell probe", got)
	}
}

func TestParseGuestIPStripsCIDR(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain", in: "192.168.64.5\n", want: "192.168.64.5"},
		{name: "cidr", in: "192.168.64.5/24\n", want: "192.168.64.5"},
		{name: "empty", in: "\n", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseGuestIP(tt.in); got != tt.want {
				t.Fatalf("parseGuestIP(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
