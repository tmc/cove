package main

import (
	"strings"
	"testing"
)

func TestRuntimePrivatePredicates(t *testing.T) {
	oVNC, oVB, oGDB := vncAddress, vncBonjourService, gdbAddress
	oF, oS1, oS2, oC, oE := forceDFU, stopInIBootStage1, stopInIBootStage2, saveCompress, saveEncrypt
	t.Cleanup(func() {
		vncAddress, vncBonjourService, gdbAddress = oVNC, oVB, oGDB
		forceDFU, stopInIBootStage1, stopInIBootStage2, saveCompress, saveEncrypt = oF, oS1, oS2, oC, oE
	})
	reset := func() {
		vncAddress, vncBonjourService, gdbAddress = "", "", ""
		forceDFU, stopInIBootStage1, stopInIBootStage2, saveCompress, saveEncrypt = false, false, false, false, false
	}

	reset()
	if vncEnabled() || debugStubEnabled() || privateMacStartOptionsEnabled() || privateSaveOptionsEnabled() {
		t.Fatal("predicates should be false on zero state")
	}

	reset()
	vncAddress = ":5901"
	if !vncEnabled() {
		t.Error("vncEnabled() = false, want true with vncAddress set")
	}

	reset()
	vncBonjourService = "myvnc"
	if !vncEnabled() {
		t.Error("vncEnabled() = false, want true with bonjour set")
	}

	reset()
	gdbAddress = ":1234"
	if !debugStubEnabled() {
		t.Error("debugStubEnabled() = false, want true")
	}

	reset()
	stopInIBootStage1 = true
	if !privateMacStartOptionsEnabled() {
		t.Error("privateMacStartOptionsEnabled() = false, want true")
	}

	reset()
	saveEncrypt = true
	if !privateSaveOptionsEnabled() {
		t.Error("privateSaveOptionsEnabled() = false, want true")
	}
}

func TestPrivateRuntimeSummary(t *testing.T) {
	oR, oF, oS1, oS2, oG, oV := recoveryMode, forceDFU, stopInIBootStage1, stopInIBootStage2, gdbAddress, vncAddress
	t.Cleanup(func() {
		recoveryMode, forceDFU, stopInIBootStage1, stopInIBootStage2, gdbAddress, vncAddress = oR, oF, oS1, oS2, oG, oV
	})
	reset := func() {
		recoveryMode, forceDFU, stopInIBootStage1, stopInIBootStage2, gdbAddress, vncAddress = false, false, false, false, "", ""
	}

	reset()
	if got := privateRuntimeSummary(); got != "" {
		t.Errorf("zero state = %q, want empty", got)
	}

	reset()
	recoveryMode = true
	gdbAddress = ":1234"
	vncAddress = ":5900"
	got := privateRuntimeSummary()
	for _, want := range []string{"recovery", "gdb", "vnc"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary %q missing %q", got, want)
		}
	}

	reset()
	stopInIBootStage2 = true
	if got := privateRuntimeSummary(); !strings.Contains(got, "iboot-stage2") {
		t.Errorf("iboot-stage2 summary = %q", got)
	}

	reset()
	forceDFU = true
	if got := privateRuntimeSummary(); !strings.Contains(got, "dfu") {
		t.Errorf("dfu summary = %q", got)
	}
}

func TestValidatePrivateRuntimeOptionsExtraBranches(t *testing.T) {
	oVNC, oPw, oGDB, oGAll := vncAddress, vncPassword, gdbAddress, gdbListenAll
	oR, oL, oW := recoveryMode, linuxMode, windowsMode
	oF, oS1, oS2 := forceDFU, stopInIBootStage1, stopInIBootStage2
	t.Cleanup(func() {
		vncAddress, vncPassword, gdbAddress, gdbListenAll = oVNC, oPw, oGDB, oGAll
		recoveryMode, linuxMode, windowsMode = oR, oL, oW
		forceDFU, stopInIBootStage1, stopInIBootStage2 = oF, oS1, oS2
	})
	tests := []struct {
		name, wantErr string
		setup         func()
	}{
		{"bad gdb", "invalid -gdb", func() { gdbAddress = "abc" }},
		{"listen-all without gdb", "-gdb-listen-all requires -gdb", func() { gdbListenAll = true }},
		{"windows recovery", "only valid for macOS", func() { windowsMode, recoveryMode = true, true }},
		{"linux private mac", "macOS-only start options", func() { linuxMode, forceDFU = true, true }},
		{"dfu plus iboot1", "-force-dfu cannot be combined", func() { forceDFU, stopInIBootStage1 = true, true }},
		{"recovery plus iboot", "recovery mode cannot", func() { recoveryMode, stopInIBootStage1 = true, true }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vncAddress, vncPassword, gdbAddress, gdbListenAll = "", "", "", false
			recoveryMode, linuxMode, windowsMode = false, false, false
			forceDFU, stopInIBootStage1, stopInIBootStage2 = false, false, false
			tt.setup()
			checkErr(t, validatePrivateRuntimeOptions(), tt.wantErr)
		})
	}
}
