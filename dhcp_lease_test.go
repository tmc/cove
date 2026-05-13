package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestParseDHCPLeaseTimeSecs(t *testing.T) {
	got, ok := parseDHCPLeaseTimeSecs("{\n    DHCPLeaseTimeSecs = 600;\n}\n")
	if !ok || got != 600 {
		t.Fatalf("parseDHCPLeaseTimeSecs() = %d, %v, want 600, true", got, ok)
	}
}

func TestWarnIfDHCPLeaseTimeLong(t *testing.T) {
	tests := []struct {
		name    string
		out     string
		err     error
		wantOut bool
	}{
		{name: "missing config uses default", wantOut: true},
		{name: "default one day warns", out: "{ DHCPLeaseTimeSecs = 86400; }", wantOut: true},
		{name: "short lease clean", out: "{ DHCPLeaseTimeSecs = 600; }"},
		{name: "read error skips", err: errors.New("boom")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			warnIfDHCPLeaseTimeLongFrom(&buf, func() (string, error) {
				return tt.out, tt.err
			})
			got := buf.String()
			if tt.wantOut {
				if !strings.Contains(got, "DHCP lease time") || !strings.Contains(got, "DHCPLeaseTimeSecs -int 600") {
					t.Fatalf("warning = %q, want lease warning and fix command", got)
				}
				return
			}
			if got != "" {
				t.Fatalf("warning = %q, want none", got)
			}
		})
	}
}

func TestIsLocalServeListenAddr(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		{"", true},
		{"127.0.0.1:7777", true},
		{"localhost:7777", true},
		{"[::1]:7777", true},
		{"unix:///tmp/cove.sock", true},
		{":7777", false},
		{"0.0.0.0:7777", false},
		{"192.168.1.10:7777", false},
		{"tcp://0.0.0.0:7777", false},
	}
	for _, tt := range tests {
		if got := isLocalServeListenAddr(tt.addr); got != tt.want {
			t.Fatalf("isLocalServeListenAddr(%q) = %v, want %v", tt.addr, got, tt.want)
		}
	}
}
