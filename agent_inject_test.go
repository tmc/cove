package main

import "testing"

func TestAgentVersionsEqual(t *testing.T) {
	tests := []struct {
		name  string
		host  string
		guest string
		want  bool
	}{
		{"identical release", "v0.2.3", "v0.2.3", true},
		{"identical commit", "abc12345", "abc12345", true},
		{"mismatch release", "v0.2.3", "v0.2.4", false},
		{"mismatch commit", "abc12345", "def67890", false},
		{"host empty", "", "v0.2.3", false},
		{"guest empty", "v0.2.3", "", false},
		{"both empty", "", "", false},
		{"host dev", "dev", "v0.2.3", false},
		{"guest dev", "v0.2.3", "dev", false},
		{"both dev", "dev", "dev", false},
		{"host unknown", "unknown", "v0.2.3", false},
		{"guest unknown", "v0.2.3", "unknown", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := agentVersionsEqual(tt.host, tt.guest); got != tt.want {
				t.Errorf("agentVersionsEqual(%q, %q) = %v, want %v", tt.host, tt.guest, got, tt.want)
			}
		})
	}
}
