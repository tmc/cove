package main

import (
	"strings"
	"testing"
)

func TestRuntimeDiskRequestedSizeBytesAlts(t *testing.T) {
	tests := []struct {
		name string
		req  RuntimeDiskActionRequest
		want uint64
	}{
		{
			name: "bytesAlt",
			req:  RuntimeDiskActionRequest{SizeBytesAlt: uint64Ptr(2048)},
			want: 2048,
		},
		{
			name: "mbAlt",
			req:  RuntimeDiskActionRequest{SizeMBAlt: uint64Ptr(8)},
			want: 8 * 1024 * 1024,
		},
		{
			name: "gbAlt",
			req:  RuntimeDiskActionRequest{SizeGBAlt: float64Ptr(0.5)},
			want: uint64(0.5 * 1024 * 1024 * 1024),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.req.requestedSizeBytes()
			if err != nil {
				t.Fatalf("requested size: %v", err)
			}
			if got != tt.want {
				t.Fatalf("requested size = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestRuntimeDiskRequestedSizeBytesUnset(t *testing.T) {
	_, err := RuntimeDiskActionRequest{}.requestedSizeBytes()
	if err == nil {
		t.Fatal("expected error when no size provided")
	}
	if !strings.Contains(err.Error(), "size required") {
		t.Fatalf("error = %q, want 'size required'", err)
	}
}

func TestRuntimeDiskReadOnlyValue(t *testing.T) {
	tt := true
	ff := false
	cases := []struct {
		name string
		req  RuntimeDiskActionRequest
		want bool
	}{
		{"unset", RuntimeDiskActionRequest{}, false},
		{"primary-true", RuntimeDiskActionRequest{ReadOnly: &tt}, true},
		{"primary-false", RuntimeDiskActionRequest{ReadOnly: &ff}, false},
		{"alt-true", RuntimeDiskActionRequest{ReadOnlyAlt: &tt}, true},
		{"alt-false", RuntimeDiskActionRequest{ReadOnlyAlt: &ff}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.req.readOnlyValue(); got != c.want {
				t.Fatalf("readOnlyValue = %v, want %v", got, c.want)
			}
		})
	}
}

func TestParseRuntimeDiskActionRequestEnvelope(t *testing.T) {
	// Envelope-wrapped request unwraps and resolves the inner action.
	req, err := parseRuntimeDiskActionRequest([]byte(`{"data":{"action":"swap","index":2,"path":"/tmp/x.img"}}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if req.actionName() != "swap" {
		t.Fatalf("action = %q, want swap", req.actionName())
	}
	idx, ok := req.targetIndex()
	if !ok || idx != 2 {
		t.Fatalf("targetIndex = (%d, %v), want (2, true)", idx, ok)
	}
	if req.targetPath() != "/tmp/x.img" {
		t.Fatalf("targetPath = %q, want /tmp/x.img", req.targetPath())
	}
}
