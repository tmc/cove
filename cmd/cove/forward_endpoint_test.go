package main

import (
	"context"
	"strings"
	"testing"
)

func TestParseForwardEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		wantName string
		wantPort string
		wantErr  bool
	}{
		{name: "host endpoint", in: "host:8080", wantName: "host", wantPort: "8080"},
		{name: "vm endpoint", in: "vm:22", wantName: "vm", wantPort: "22"},
		{name: "uppercase normalized", in: "HOST:80", wantName: "host", wantPort: "80"},
		{name: "trimmed whitespace", in: "  vm:443  ", wantName: "vm", wantPort: "443"},
		{name: "empty input", in: "", wantErr: true},
		{name: "missing colon", in: "host", wantErr: true},
		{name: "missing port", in: "host:", wantErr: true},
		{name: "missing name", in: ":8080", wantErr: true},
		{name: "extra colon", in: "host:8080:9090", wantErr: true},
		{name: "invalid name", in: "guest:22", wantErr: true},
		{name: "double-colon empty middle", in: "host::8080", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotName, gotPort, err := parseForwardEndpoint(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseForwardEndpoint(%q) = (%q, %q, nil), want error", tt.in, gotName, gotPort)
				}
				if !strings.Contains(err.Error(), "invalid endpoint") {
					t.Fatalf("err = %q, want substring 'invalid endpoint'", err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("parseForwardEndpoint(%q) = %v, want nil", tt.in, err)
			}
			if gotName != tt.wantName || gotPort != tt.wantPort {
				t.Fatalf("parseForwardEndpoint(%q) = (%q, %q), want (%q, %q)", tt.in, gotName, gotPort, tt.wantName, tt.wantPort)
			}
		})
	}
}

func TestRunForwardHelpArgsShortCircuit(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "short help", args: []string{"-h"}},
		{name: "long help", args: []string{"--help"}},
		{name: "help word", args: []string{"help"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := runForward(context.Background(), tt.args, func(string) forwardStarter {
				t.Fatal("starter should not be called for help args")
				return nil
			})
			if err != nil {
				t.Fatalf("runForward(%v) = %v, want nil", tt.args, err)
			}
		})
	}
}
