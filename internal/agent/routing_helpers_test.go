package agent

import "testing"

func TestFlagConsumesNext(t *testing.T) {
	tests := []struct {
		flag string
		want bool
	}{
		{"-C", true},
		{"--directory", true},
		{"-f", true},
		{"--file", true},
		{"-o", true},
		{"--output", true},
		{"-t", true},
		{"--target-directory", true},
		{"--reference", true},
		{"--owner", true},
		{"--group", true},
		{"--exclude", true},
		{"--exclude-from", true},
		{"--transform", true},
		{"--unknown", false},
		{"-v", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.flag, func(t *testing.T) {
			if got := flagConsumesNext(tt.flag); got != tt.want {
				t.Errorf("flagConsumesNext(%q) = %v, want %v", tt.flag, got, tt.want)
			}
		})
	}
}

func TestIsUnaryFileTest(t *testing.T) {
	for _, op := range []string{"-e", "-f", "-d", "-r", "-w", "-x", "-L", "-S", "-b", "-c", "-p"} {
		t.Run(op, func(t *testing.T) {
			if !isUnaryFileTest(op) {
				t.Errorf("isUnaryFileTest(%q) = false, want true", op)
			}
		})
	}
	for _, op := range []string{"-z", "-n", "", "-eq", "--file", "f"} {
		t.Run("not "+op, func(t *testing.T) {
			if isUnaryFileTest(op) {
				t.Errorf("isUnaryFileTest(%q) = true, want false", op)
			}
		})
	}
}

func TestTarPathOperand(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"no args", nil, ""},
		{"-C separate", []string{"-C", "/tmp/work", "-cf", "/out.tar", "."}, "/tmp/work"},
		{"-C joined", []string{"-Cdummy/path", "-cf", "/out.tar"}, "dummy/path"},
		{"-f with path next", []string{"-cf", "/tmp/out.tar"}, "/tmp/out.tar"},
		{"--file= equals form", []string{"--file=/tmp/eq.tar"}, "/tmp/eq.tar"},
		{"-- terminator picks first path", []string{"--", "skip-me", "/real/path"}, "/real/path"},
		{"-- terminator no path", []string{"--", "ignored"}, ""},
		{"first positional", []string{"/some/file"}, "/some/file"},
		{"--file separate", []string{"--file", "/tmp/sep.tar"}, "/tmp/sep.tar"},
		{"non-path positional ignored", []string{"justaword", "/abs/path"}, "/abs/path"},
		{"file beats positional", []string{"-cf", "/from/file.tar", "/positional"}, "/from/file.tar"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tarPathOperand(tt.args); got != tt.want {
				t.Errorf("tarPathOperand(%v) = %q, want %q", tt.args, got, tt.want)
			}
		})
	}
}
