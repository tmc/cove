package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/tmc/cove/internal/sckit"
)

func TestReportSCKitProbeTableAuthorized(t *testing.T) {
	var buf bytes.Buffer
	p := sckit.Probe{SCKitAvailable: true, ScreenRecordingAuthorized: true, MacOSVersion: "14.5"}
	if err := reportSCKitProbe(&buf, p, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"ScreenCaptureKit", "14.5", "SCKit available", "yes"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n%s", want, out)
		}
	}
}

func TestReportSCKitProbeTableUnauthorized(t *testing.T) {
	var buf bytes.Buffer
	p := sckit.Probe{SCKitAvailable: true, ScreenRecordingAuthorized: false, MacOSVersion: "14.5"}
	err := reportSCKitProbe(&buf, p, false)
	if err == nil {
		t.Fatalf("expected error when not authorized")
	}
	if !strings.Contains(err.Error(), "System Settings") {
		t.Errorf("error missing remediation hint: %v", err)
	}
}

func TestReportSCKitProbeJSON(t *testing.T) {
	var buf bytes.Buffer
	p := sckit.Probe{SCKitAvailable: true, ScreenRecordingAuthorized: true, MacOSVersion: "15.1"}
	if err := reportSCKitProbe(&buf, p, true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got sckit.Probe
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v\n%s", err, buf.String())
	}
	if got != p {
		t.Errorf("round-trip = %+v want %+v", got, p)
	}
}

func TestReportSCKitProbeUnknownVersion(t *testing.T) {
	var buf bytes.Buffer
	p := sckit.Probe{}
	_ = reportSCKitProbe(&buf, p, false)
	if !strings.Contains(buf.String(), "(unknown)") {
		t.Errorf("expected (unknown) for empty version, got:\n%s", buf.String())
	}
}
