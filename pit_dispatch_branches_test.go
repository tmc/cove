package main

import (
	"strings"
	"testing"
)

func TestHandlePITJSONRequestBranches(t *testing.T) {
	s := NewControlServerWithVMDir("", t.TempDir())

	tests := []struct {
		name    string
		raw     string
		wantSub string
	}{
		{"parseError", `{not-json`, "parse pit request"},
		{"emptyAction", `{}`, "pit action required"},
		{"unknownAction", `{"action":"frobnicate"}`, "unknown pit action"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := s.handlePITJSONRequest([]byte(tt.raw))
			if resp == nil {
				t.Fatal("nil response")
			}
			if !strings.Contains(resp.Error, tt.wantSub) {
				t.Fatalf("Error = %q, want substring %q", resp.Error, tt.wantSub)
			}
		})
	}
}
