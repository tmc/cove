package fleetproto

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestKnownKind(t *testing.T) {
	tests := []struct {
		name string
		kind string
		want bool
	}{
		{"fork-run", KindForkRun, true},
		{"stop-vm", KindStopVM, true},
		{"image-sync", KindImageSync, true},
		{"policy", KindPolicy, true},
		{"image-gc", KindImageGC, true},
		{"host-shell", "host-shell", false},
		{"empty", "", false},
		{"exec", "exec", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := KnownKind(tt.kind); got != tt.want {
				t.Fatalf("KnownKind(%q) = %v, want %v", tt.kind, got, tt.want)
			}
		})
	}
}

func TestBearerToken(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string
	}{
		{"bearer", "Bearer abc123", "abc123"},
		{"raw", "abc123", "abc123"},
		{"empty", "", ""},
		{"bearer spaces", "Bearer   tok  ", "tok"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/", nil)
			if tt.header != "" {
				r.Header.Set(AuthHeader, tt.header)
			}
			if got := BearerToken(r); got != tt.want {
				t.Fatalf("BearerToken(%q) = %q, want %q", tt.header, got, tt.want)
			}
		})
	}
}

func TestDecodeJSON(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"host_id":"h1","token":"t"}`))
	got, err := DecodeJSON[Register](r)
	if err != nil {
		t.Fatalf("DecodeJSON: %v", err)
	}
	if got.HostID != "h1" || got.Token != "t" {
		t.Fatalf("decoded = %+v, want h1/t", got)
	}
}

func TestDecodeJSONUnknownField(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"host_id":"h1","bogus":1}`))
	if _, err := DecodeJSON[Register](r); err == nil {
		t.Fatal("expected unknown field to be rejected")
	}
}
