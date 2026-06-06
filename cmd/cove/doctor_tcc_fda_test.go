package main

import (
	"strings"
	"testing"
	"time"
)

func TestParseTCCFDAArgs(t *testing.T) {
	opts, err := parseTCCFDAArgs([]string{
		"-vm", "ftue",
		"-tcc-path", "/Volumes/ml-explore",
		"-password", "covetest123",
		"-timeout", "12s",
		"-upgrade-agent",
	})
	if err != nil {
		t.Fatalf("parseTCCFDAArgs: %v", err)
	}
	if opts.vmName != "ftue" || opts.path != "/Volumes/ml-explore" || opts.password != "covetest123" {
		t.Fatalf("opts = %+v", opts)
	}
	if opts.timeout != 12*time.Second {
		t.Fatalf("timeout = %s, want 12s", opts.timeout)
	}
	if !opts.upgradeAgent {
		t.Fatalf("upgradeAgent = false, want true")
	}
}

func TestParseTCCFDAArgsRejectsPositional(t *testing.T) {
	_, err := parseTCCFDAArgs([]string{"-tcc-path", "/Volumes/ml-explore", "extra"})
	if err == nil || !strings.Contains(err.Error(), "unexpected arguments") {
		t.Fatalf("err = %v, want unexpected arguments", err)
	}
}

func TestTCCFDAGuidanceTokens(t *testing.T) {
	out := captureStdout(t, func() error {
		fmtTCCFDAPreAuthUnavailable()
		return nil
	})
	if !strings.Contains(out, "COVE_TCC_FDA_PREAUTH_UNAVAILABLE") {
		t.Fatalf("output missing preauth token:\n%s", out)
	}
}
