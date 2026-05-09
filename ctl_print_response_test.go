package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

func TestCtlPrintResponseRawEmitsJSON(t *testing.T) {
	resp := &controlpb.ControlResponse{Success: true, Data: "hello"}
	out := captureCommandStdout(func() {
		if err := ctlPrintResponse(resp, "ping", true, ""); err != nil {
			t.Fatalf("ctlPrintResponse: %v", err)
		}
	})
	if !strings.Contains(out, "\"success\":true") || !strings.Contains(out, "\"data\":\"hello\"") {
		t.Errorf("raw output missing fields: %q", out)
	}
}

func TestCtlPrintResponseFailureReturnsError(t *testing.T) {
	resp := &controlpb.ControlResponse{Success: false, Error: "boom"}
	err := ctlPrintResponse(resp, "ping", false, "")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("err = %v, want substring 'boom'", err)
	}
}

func TestCtlPrintResponseEmptyDataPrintsOK(t *testing.T) {
	resp := &controlpb.ControlResponse{Success: true}
	out := captureCommandStdout(func() {
		if err := ctlPrintResponse(resp, "ping", false, ""); err != nil {
			t.Fatalf("ctlPrintResponse: %v", err)
		}
	})
	if strings.TrimSpace(out) != "OK" {
		t.Errorf("output = %q, want OK", out)
	}
}

func TestCtlPrintResponsePrettyPrintsJSONData(t *testing.T) {
	resp := &controlpb.ControlResponse{Success: true, Data: `{"k":1}`}
	out := captureCommandStdout(func() {
		if err := ctlPrintResponse(resp, "ping", false, ""); err != nil {
			t.Fatalf("ctlPrintResponse: %v", err)
		}
	})
	// JSON pretty-print indents the value on its own line.
	if !strings.Contains(out, "\"k\": 1") {
		t.Errorf("output = %q, want pretty-printed JSON", out)
	}
}

func TestCtlPrintResponseScreenshotWritesFile(t *testing.T) {
	imgData := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}
	resp := &controlpb.ControlResponse{Success: true, Data: base64.StdEncoding.EncodeToString(imgData)}
	out := filepath.Join(t.TempDir(), "shot.png")
	stdout := captureCommandStdout(func() {
		if err := ctlPrintResponse(resp, "screenshot", false, out); err != nil {
			t.Fatalf("ctlPrintResponse: %v", err)
		}
	})
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(imgData) {
		t.Errorf("file bytes = %x, want %x", got, imgData)
	}
	if !strings.Contains(stdout, "Screenshot saved to") {
		t.Errorf("stdout = %q, want 'Screenshot saved to'", stdout)
	}
}

func TestCtlPrintResponseScreenshotInvalidBase64Errors(t *testing.T) {
	resp := &controlpb.ControlResponse{Success: true, Data: "!!!not-base64!!!"}
	err := ctlPrintResponse(resp, "screenshot", false, filepath.Join(t.TempDir(), "x.png"))
	if err == nil || !strings.Contains(err.Error(), "decode image") {
		t.Errorf("err = %v, want 'decode image' error", err)
	}
}
