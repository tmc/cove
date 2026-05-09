package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestCleanHostPath(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr string
	}{
		{name: "empty rejects", input: "", wantErr: "empty host path"},
		{name: "whitespace rejects", input: "   ", wantErr: "empty host path"},
		{name: "colon rejects", input: "/tmp/foo:bar", wantErr: "contains ':'"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := cleanHostPath(tt.input)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("cleanHostPath(%q) error = %v, want substring %q", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestCleanHostPathAbsolute(t *testing.T) {
	got, err := cleanHostPath("/tmp/foo/../bar/")
	if err != nil {
		t.Fatalf("cleanHostPath: %v", err)
	}
	if got != "/tmp/bar" {
		t.Errorf("cleanHostPath(/tmp/foo/../bar/) = %q, want /tmp/bar", got)
	}
}

func TestCleanHostPathRelativeJoinsCWD(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	got, err := cleanHostPath("sub/file.txt")
	if err != nil {
		t.Fatalf("cleanHostPath: %v", err)
	}
	want := filepath.Join(dir, "sub/file.txt")
	if got != want {
		t.Errorf("cleanHostPath relative = %q, want %q", got, want)
	}
}

func TestParseForwardEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantName string
		wantPort string
		wantErr  string
	}{
		{name: "host", input: "host:8080", wantName: "host", wantPort: "8080"},
		{name: "vm", input: "vm:22", wantName: "vm", wantPort: "22"},
		{name: "uppercase normalized", input: "HOST:80", wantName: "host", wantPort: "80"},
		{name: "leading whitespace trimmed", input: "  vm:1234", wantName: "vm", wantPort: "1234"},
		{name: "missing colon", input: "host", wantErr: "invalid endpoint"},
		{name: "empty name", input: ":80", wantErr: "invalid endpoint"},
		{name: "empty port", input: "host:", wantErr: "invalid endpoint"},
		{name: "extra colon", input: "host:80:90", wantErr: "invalid endpoint"},
		{name: "unknown name", input: "guest:22", wantErr: "invalid endpoint"},
		{name: "empty input", input: "", wantErr: "invalid endpoint"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotName, gotPort, err := parseForwardEndpoint(tt.input)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("parseForwardEndpoint(%q) error = %v, want %q", tt.input, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseForwardEndpoint(%q) error = %v", tt.input, err)
			}
			if gotName != tt.wantName || gotPort != tt.wantPort {
				t.Errorf("parseForwardEndpoint(%q) = (%q, %q), want (%q, %q)", tt.input, gotName, gotPort, tt.wantName, tt.wantPort)
			}
		})
	}
}
