package main

import (
	"strings"
	"testing"
)

func TestSetVZScriptWindowLabelControlServerPath(t *testing.T) {
	srv := NewControlServerWithVMDir("", t.TempDir())
	cfg := vzscriptConfig{controlSrv: srv}
	setVZScriptWindowLabel(cfg, "Provisioning", nil)
	if !strings.Contains(srv.WindowTitle(), "Provisioning") {
		t.Fatalf("WindowTitle = %q, want contains %q", srv.WindowTitle(), "Provisioning")
	}
}

func TestSetVZScriptWindowLabelEmptySocketIsNoOp(t *testing.T) {
	cfg := vzscriptConfig{}
	setVZScriptWindowLabel(cfg, "ignored", nil)
}
