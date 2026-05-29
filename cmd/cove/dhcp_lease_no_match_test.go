package main

import "testing"

func TestParseDHCPLeaseTimeSecsNoMatch(t *testing.T) {
	tests := []string{
		"",
		"unrelated output",
		"DHCPLeaseTime = 600", // missing 'Secs'
	}
	for _, s := range tests {
		t.Run(s, func(t *testing.T) {
			if got, ok := parseDHCPLeaseTimeSecs(s); ok {
				t.Fatalf("parseDHCPLeaseTimeSecs(%q) = (%d, true), want (_, false)", s, got)
			}
		})
	}
}
