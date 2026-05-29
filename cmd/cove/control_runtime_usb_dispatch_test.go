package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestHandleRuntimeUSBJSONRequestParseError(t *testing.T) {
	s := NewControlServerWithVMDir("", t.TempDir())
	resp := s.handleRuntimeUSBJSONRequest([]byte(`{not-json`))
	if resp.Success {
		t.Fatalf("success = true on bad JSON: %#v", resp)
	}
	if !strings.Contains(resp.Error, "parse runtime usb request") {
		t.Fatalf("error = %q, want 'parse runtime usb request'", resp.Error)
	}
	var got runtimeUSBResponse
	if err := json.Unmarshal([]byte(resp.Data), &got); err != nil {
		t.Fatalf("unmarshal data: %v", err)
	}
	if got.OK || got.Action != "" {
		t.Fatalf("response = %#v, want OK=false Action=\"\"", got)
	}
}

func TestHandleRuntimeUSBJSONRequestMissingAction(t *testing.T) {
	s := NewControlServerWithVMDir("", t.TempDir())
	resp := s.handleRuntimeUSBJSONRequest([]byte(`{}`))
	if resp.Success {
		t.Fatalf("success = true on empty action: %#v", resp)
	}
	if !strings.Contains(resp.Error, "action required") {
		t.Fatalf("error = %q, want 'action required'", resp.Error)
	}
}

func TestHandleRuntimeUSBJSONRequestValidateError(t *testing.T) {
	s := NewControlServerWithVMDir("", t.TempDir())
	resp := s.handleRuntimeUSBJSONRequest(
		[]byte(`{"action":"attach-mass-storage","controller_index":0}`),
	)
	if resp.Success {
		t.Fatalf("success = true on missing path: %#v", resp)
	}
	if !strings.Contains(resp.Error, "path required") {
		t.Fatalf("error = %q, want 'path required'", resp.Error)
	}
	var got runtimeUSBResponse
	if err := json.Unmarshal([]byte(resp.Data), &got); err != nil {
		t.Fatalf("unmarshal data: %v", err)
	}
	if got.Action != string(runtimeUSBActionAttachMassStorage) {
		t.Fatalf("Action = %q, want %q", got.Action, runtimeUSBActionAttachMassStorage)
	}
}

func TestHandleRuntimeUSBJSONRequestUnknownAction(t *testing.T) {
	s := NewControlServerWithVMDir("", t.TempDir())
	resp := s.handleRuntimeUSBJSONRequest([]byte(`{"action":"frobnicate"}`))
	if resp.Success {
		t.Fatalf("success = true on unknown action: %#v", resp)
	}
	if !strings.Contains(resp.Error, "unknown runtime usb action") {
		t.Fatalf("error = %q, want 'unknown runtime usb action'", resp.Error)
	}
}

func TestHandleRuntimeUSBJSONRequestEnvelopeUnwrap(t *testing.T) {
	s := NewControlServerWithVMDir("", t.TempDir())
	envelope := []byte(`{"data":{"action":"frobnicate"}}`)
	resp := s.handleRuntimeUSBJSONRequest(envelope)
	if resp.Success {
		t.Fatalf("success = true on envelope-wrapped unknown action: %#v", resp)
	}
	if !strings.Contains(resp.Error, "unknown runtime usb action") {
		t.Fatalf("error = %q, want 'unknown runtime usb action' after envelope unwrap", resp.Error)
	}
}
