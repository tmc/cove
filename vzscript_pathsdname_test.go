package main

import "testing"

func TestPathsDName(t *testing.T) {
	tests := []struct {
		name string
		dir  string
		want string
	}{
		{name: "go bin uses parent", dir: "/usr/local/go/bin", want: "go"},
		{name: "homebrew bin uses parent", dir: "/opt/homebrew/bin", want: "homebrew"},
		{name: "sbin uses parent", dir: "/usr/local/sbin", want: "local"},
		{name: "libexec uses parent", dir: "/opt/foo/libexec", want: "foo"},
		{name: "non-generic uses base", dir: "/opt/tools", want: "tools"},
		{name: "single generic at root falls back to base", dir: "/bin", want: "bin"},
		{name: "deep non-generic", dir: "/opt/homebrew/opt/python", want: "python"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pathsDName(tt.dir); got != tt.want {
				t.Errorf("pathsDName(%q) = %q, want %q", tt.dir, got, tt.want)
			}
		})
	}
}
