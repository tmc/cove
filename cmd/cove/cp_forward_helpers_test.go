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
