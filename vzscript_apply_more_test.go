package main

import (
	"strings"
	"testing"
)

func TestVZScriptGuestOSDisplay(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"linux", "Linux"},
		{"darwin", "Darwin"},
		{"macos", "Darwin"},
		{"", "unknown"},
		{"both", "unknown"},
		{"windows", "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := vzscriptGuestOSDisplay(tt.in); got != tt.want {
				t.Fatalf("vzscriptGuestOSDisplay(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestCheckVZScriptGuestOS(t *testing.T) {
	tests := []struct {
		name      string
		recipeOS  string
		targetOS  string
		wantErr   bool
		wantMatch string
	}{
		{"empty recipe defaults darwin matches darwin", "", "darwin", false, ""},
		{"darwin recipe darwin target", "darwin", "darwin", false, ""},
		{"linux recipe linux target", "linux", "linux", false, ""},
		{"both recipe linux target", "both", "linux", false, ""},
		{"empty target accepted", "darwin", "", false, ""},
		{"linux recipe darwin target", "linux", "darwin", true, "is for linux guests only; this VM is Darwin"},
		{"darwin recipe linux target", "darwin", "linux", true, "is for darwin guests only; this VM is Linux"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkVZScriptGuestOS("recipeX", scriptMeta{guestOS: tt.recipeOS}, tt.targetOS)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("checkVZScriptGuestOS = nil, want error")
				}
				if !strings.Contains(err.Error(), tt.wantMatch) {
					t.Fatalf("checkVZScriptGuestOS err = %v, want substring %q", err, tt.wantMatch)
				}
				return
			}
			if err != nil {
				t.Fatalf("checkVZScriptGuestOS = %v, want nil", err)
			}
		})
	}
}
