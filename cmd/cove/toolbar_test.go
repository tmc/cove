package main

import (
	"testing"

	"github.com/tmc/apple/appkit"
)

func TestIsModalResponseOK(t *testing.T) {
	tests := []struct {
		name string
		in   appkit.NSModalResponse
		want bool
	}{
		{"ok", modalResponseOKCode, true},
		{"cancel", 0, false},
		{"abort", -1000, false},
		{"alt", 1001, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isModalResponseOK(tt.in); got != tt.want {
				t.Errorf("isModalResponseOK(%d) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestToolbarItemIDs(t *testing.T) {
	got := toolbarItemIDs()
	if len(got) == 0 {
		t.Fatal("toolbarItemIDs empty")
	}
	want := []string{
		toolbarIDStop, toolbarIDStartPause, toolbarIDRestart, toolbarIDBootOptions,
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("position %d: got %q, want %q", i, got[i], w)
		}
	}
	seen := map[string]bool{}
	for _, id := range got {
		if id == "" {
			t.Error("empty identifier in toolbarItemIDs")
		}
		if seen[id] {
			t.Errorf("duplicate identifier %q", id)
		}
		seen[id] = true
	}
	for _, id := range []string{toolbarIDCaptureInput, toolbarIDScreenshot, toolbarIDSharedFolder} {
		if !seen[id] {
			t.Errorf("missing trailing identifier %q", id)
		}
	}
}
