package main

import (
	"strings"
	"testing"
)

func TestParsePortSpec(t *testing.T) {
	tests := []struct {
		name, spec, wantErr string
		want                uint16
	}{
		{"empty", "", "", 0},
		{"bare", "5900", "", 5900},
		{"colon prefix", ":5900", "", 5900},
		{"zero", "0", "out of range", 0},
		{"too large", "65536", "out of range", 0},
		{"non-numeric", "abc", "parse port", 0},
		{"slash", "tcp/5900", "expected port", 0},
		{"host qualified", "127.0.0.1:5900", "host-qualified", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parsePortSpec(tt.spec)
			checkErr(t, err, tt.wantErr)
			if tt.wantErr == "" && got != tt.want {
				t.Fatalf("got %d, want %d", got, tt.want)
			}
		})
	}
}

func checkErr(t *testing.T, err error, want string) {
	t.Helper()
	switch {
	case want == "" && err != nil:
		t.Fatalf("unexpected: %v", err)
	case want != "" && (err == nil || !strings.Contains(err.Error(), want)):
		t.Fatalf("got %v, want %q", err, want)
	}
}

func TestActionSourceLabel(t *testing.T) {
	if got := actionSourceLabel(""); got != "VM" {
		t.Errorf("empty: %q", got)
	}
	if got := actionSourceLabel("ctl-shutdown"); got != "ctl-shutdown" {
		t.Errorf("non-empty source: got %q, want verbatim", got)
	}
}

func TestValidatePrivateRuntimeOptions(t *testing.T) {
	oVNC, oPw, oBonjour, oGDB, oGAll := vncAddress, vncPassword, vncBonjourService, gdbAddress, gdbListenAll
	oR, oL, oW := recoveryMode, linuxMode, windowsMode
	oF, oS1, oS2 := forceDFU, stopInIBootStage1, stopInIBootStage2
	t.Cleanup(func() {
		vncAddress, vncPassword, vncBonjourService, gdbAddress, gdbListenAll = oVNC, oPw, oBonjour, oGDB, oGAll
		recoveryMode, linuxMode, windowsMode = oR, oL, oW
		forceDFU, stopInIBootStage1, stopInIBootStage2 = oF, oS1, oS2
	})
	tests := []struct {
		name, wantErr string
		setup         func()
	}{
		{"defaults", "", func() {}},
		{"bad vnc", "invalid -vnc", func() { vncAddress = "abc" }},
		{"pw without vnc", "-vnc-password requires", func() { vncPassword = "x" }},
		{"bonjour requires password", "-vnc-bonjour requires -vnc-password", func() { vncBonjourService = "cove-vm" }},
		{"recovery linux", "only valid for macOS", func() { linuxMode, recoveryMode = true, true }},
		{"both iboot", "mutually exclusive", func() { stopInIBootStage1, stopInIBootStage2 = true, true }},
		{"recovery dfu", "recovery mode cannot", func() { recoveryMode, forceDFU = true, true }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vncAddress, vncPassword, vncBonjourService, gdbAddress, gdbListenAll = "", "", "", "", false
			recoveryMode, linuxMode, windowsMode = false, false, false
			forceDFU, stopInIBootStage1, stopInIBootStage2 = false, false, false
			tt.setup()
			checkErr(t, validatePrivateRuntimeOptionsForOptions(currentRuntimeOptions()), tt.wantErr)
		})
	}
}
