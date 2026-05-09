package main

import (
	"testing"
	"time"
)

func TestParseDisposableCloneNameRejectsAndAccepts(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		wantOK   bool
		wantBase string
	}{
		{name: "empty", in: "", wantOK: false},
		{name: "no -d- separator", in: "myvm-12345678-123456", wantOK: false},
		{name: "leading -d- (idx=0)", in: "-d-20240101-120000", wantOK: false},
		{name: "stamp too short", in: "vm-d-2024", wantOK: false},
		{name: "stamp wrong length", in: "vm-d-20240101-1200", wantOK: false},
		{name: "stamp unparseable", in: "vm-d-XXXXXXXX-XXXXXX", wantOK: false},
		{name: "happy path with base", in: "myvm-d-20240315-103045", wantOK: true, wantBase: "myvm"},
		{name: "happy path with whitespace base", in: "  spaced  -d-20240315-103045", wantOK: true, wantBase: "spaced"},
		{name: "blank base falls back to vm", in: "   -d-20240315-103045", wantOK: true, wantBase: "vm"},
		{name: "base with dashes is preserved", in: "my-vm-d-20240315-103045", wantOK: true, wantBase: "my-vm"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base, createdAt, ok := parseDisposableCloneName(tt.in)
			if ok != tt.wantOK {
				t.Fatalf("parseDisposableCloneName(%q) ok = %v, want %v", tt.in, ok, tt.wantOK)
			}
			if !ok {
				if !createdAt.IsZero() {
					t.Errorf("createdAt should be zero on failure, got %v", createdAt)
				}
				return
			}
			if base != tt.wantBase {
				t.Errorf("base = %q, want %q", base, tt.wantBase)
			}
			if createdAt.IsZero() {
				t.Errorf("createdAt should not be zero on success")
			}
		})
	}
}

func TestParseDisposableCloneNameRoundTripsWithCloneName(t *testing.T) {
	original := time.Date(2024, 3, 15, 10, 30, 45, 0, time.Local)
	now := func() time.Time { return original }
	name := disposableCloneName("workstation", now())
	base, createdAt, ok := parseDisposableCloneName(name)
	if !ok {
		t.Fatalf("parseDisposableCloneName(%q) ok=false", name)
	}
	if base != "workstation" {
		t.Errorf("base = %q, want workstation", base)
	}
	if !createdAt.Equal(original) {
		t.Errorf("createdAt = %v, want %v", createdAt, original)
	}
}
