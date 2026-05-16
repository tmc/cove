package buildpaths

import (
	"path/filepath"
	"testing"
)

func TestExpandHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	tests := []struct {
		in   string
		want string
	}{
		{in: "~", want: home},
		{in: "~/vm", want: filepath.Join(home, "vm")},
		{in: "/tmp/vm", want: "/tmp/vm"},
	}
	for _, tt := range tests {
		if got := ExpandHome(tt.in); got != tt.want {
			t.Fatalf("ExpandHome(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestLocalBaseDir(t *testing.T) {
	dir := t.TempDir()
	got, ok := LocalBaseDir(dir)
	if !ok || got != dir {
		t.Fatalf("LocalBaseDir(%q) = (%q, %v), want (%q, true)", dir, got, ok, dir)
	}
	missing := filepath.Join(dir, "missing")
	if _, ok := LocalBaseDir(missing); ok {
		t.Fatalf("LocalBaseDir(%q) ok = true, want false", missing)
	}
}
