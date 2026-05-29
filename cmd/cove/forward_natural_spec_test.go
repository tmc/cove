package main

import (
	"strings"
	"testing"
)

func TestParseNaturalForwardSpecRejections(t *testing.T) {
	tests := []struct {
		name    string
		mapping string
		wantSub string
	}{
		{"missingArrow", "host:80 vm:8080", "expected vm:port->host:port"},
		{"badLeft", ":80->vm:8080", "invalid endpoint"},
		{"badRight", "host:80->garbage:8080", "invalid endpoint"},
		{"hostHost", "host:80->host:8080", "expected host:port->vm:port"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseNaturalForwardSpec("vm1", tt.mapping)
			if err == nil || !strings.Contains(err.Error(), tt.wantSub) {
				t.Fatalf("err = %v, want %q", err, tt.wantSub)
			}
		})
	}
}
