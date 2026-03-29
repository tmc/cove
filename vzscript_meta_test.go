package main

import (
	"os"
	"testing"
)

func TestParseScriptMetaInject(t *testing.T) {
	input := []byte(`# test-recipe — Test recipe with inject
# requires: login
# inject: Library/LaunchDaemons/com.test.plist test.plist 0644 root:wheel
# inject: usr/local/bin/setup.sh setup.sh 0755

guest-ping
`)
	meta := parseScriptMeta(input)
	if meta.name != "test-recipe" {
		t.Errorf("name = %q, want %q", meta.name, "test-recipe")
	}
	if len(meta.inject) != 2 {
		t.Fatalf("got %d inject directives, want 2", len(meta.inject))
	}
	// First directive: all fields.
	if meta.inject[0].guestPath != "Library/LaunchDaemons/com.test.plist" {
		t.Errorf("inject[0].guestPath = %q", meta.inject[0].guestPath)
	}
	if meta.inject[0].txtarFile != "test.plist" {
		t.Errorf("inject[0].txtarFile = %q", meta.inject[0].txtarFile)
	}
	if meta.inject[0].mode != "0644" {
		t.Errorf("inject[0].mode = %q", meta.inject[0].mode)
	}
	if meta.inject[0].owner != "root:wheel" {
		t.Errorf("inject[0].owner = %q", meta.inject[0].owner)
	}
	// Second directive: no owner.
	if meta.inject[1].guestPath != "usr/local/bin/setup.sh" {
		t.Errorf("inject[1].guestPath = %q", meta.inject[1].guestPath)
	}
	if meta.inject[1].txtarFile != "setup.sh" {
		t.Errorf("inject[1].txtarFile = %q", meta.inject[1].txtarFile)
	}
	if meta.inject[1].mode != "0755" {
		t.Errorf("inject[1].mode = %q", meta.inject[1].mode)
	}
	if meta.inject[1].owner != "" {
		t.Errorf("inject[1].owner = %q, want empty", meta.inject[1].owner)
	}
}

func TestParseFileMode(t *testing.T) {
	tests := []struct {
		input string
		want  os.FileMode
	}{
		{"0755", 0755},
		{"0644", 0644},
		{"0700", 0700},
		{"", 0644},      // empty -> default
		{"bogus", 0644}, // unparseable -> default
	}
	for _, tt := range tests {
		got := parseFileMode(tt.input, 0644)
		if got != tt.want {
			t.Errorf("parseFileMode(%q, 0644) = %04o, want %04o", tt.input, got, tt.want)
		}
	}
}
