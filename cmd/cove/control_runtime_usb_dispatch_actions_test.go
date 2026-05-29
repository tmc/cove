package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandleRuntimeUSBJSONRequestActionDispatchNoVM(t *testing.T) {
	s := NewControlServerWithVMDir("", t.TempDir())

	dummy := filepath.Join(t.TempDir(), "disk.img")
	if err := os.WriteFile(dummy, []byte("x"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	tests := []struct {
		name string
		raw  string
	}{
		{"attachMassStorage", `{"action":"attach-mass-storage","controller_index":0,"path":"` + dummy + `"}`},
		{"detach", `{"action":"detach","controller_index":0,"device_index":0}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := s.handleRuntimeUSBJSONRequest([]byte(tt.raw))
			if resp == nil || resp.Error == "" {
				t.Fatalf("resp = %#v, want error", resp)
			}
			var got runtimeUSBResponse
			if err := json.Unmarshal([]byte(resp.Data), &got); err != nil {
				t.Fatalf("Unmarshal data: %v", err)
			}
			if got.OK {
				t.Fatalf("OK = true, want false")
			}
			if !strings.Contains(got.Message, "vm not configured") {
				t.Fatalf("Message = %q, want substring %q", got.Message, "vm not configured")
			}
		})
	}
}
