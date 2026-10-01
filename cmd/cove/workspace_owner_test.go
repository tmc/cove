package main

import "testing"

func TestParseWorkspaceOwner(t *testing.T) {
	for _, tt := range []struct {
		name, value string
		want        uint32
	}{
		{"user", "502\n", 502},
		{"group", "20", 20},
		{"root", "0", 0},
		{"missing", "", 0},
		{"negative", "-1", 0},
		{"overflow", "4294967296", 0},
		{"extra output", "502\n20", 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseWorkspaceOwner(tt.value)
			if got != tt.want || (err != nil) != (tt.want == 0) {
				t.Fatalf("parse = %d, %v; want %d", got, err, tt.want)
			}
		})
	}
}
