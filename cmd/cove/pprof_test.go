package main

import "testing"

func TestNormalizePprofAddr(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"port", "6060", "127.0.0.1:6060"},
		{"colon port", ":6060", "127.0.0.1:6060"},
		{"host port", "localhost:6060", "localhost:6060"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizePprofAddr(tt.in); got != tt.want {
				t.Fatalf("normalizePprofAddr(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
