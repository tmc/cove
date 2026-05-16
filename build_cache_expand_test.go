package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("UserHomeDir: %v", err)
	}
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"tilde alone", "~", filepath.Join(home, "~")},
		{"tilde slash", "~/foo/bar", filepath.Join(home, "foo", "bar")},
		{"absolute unchanged", "/etc/hosts", "/etc/hosts"},
		{"relative unchanged", "foo/bar", "foo/bar"},
		{"empty unchanged", "", ""},
		{"tilde no slash unchanged", "~user/foo", "~user/foo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := expandHome(tt.in)
			if got != tt.want {
				t.Errorf("expandHome(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestLocalBuildBaseDir(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		in     string
		want   string
		wantOK bool
	}{
		{"existing dir", sub, sub, true},
		{"file is not dir", file, file, false},
		{"missing path", filepath.Join(dir, "nope"), filepath.Join(dir, "nope"), false},
		{"empty string", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := localBuildBaseDir(tt.in)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("localBuildBaseDir(%q) = (%q, %v), want (%q, %v)", tt.in, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}
