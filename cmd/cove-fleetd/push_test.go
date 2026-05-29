// cove fleet control plane (paid layer) — see docs/designs/046-fleet-control-plane.md
package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/tmc/cove/internal/fleet"
	"github.com/tmc/cove/internal/fleet/fleetproto"
)

func TestSplitHosts(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"spaces only", "  ", nil},
		{"single", "h1", []string{"h1"}},
		{"multi", "h1,h2,h3", []string{"h1", "h2", "h3"}},
		{"trims and skips blanks", " h1 , ,h2,", []string{"h1", "h2"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := splitHosts(tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("splitHosts(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestDurString(t *testing.T) {
	if got := durString(0); got != "" {
		t.Fatalf("durString(0) = %q, want empty", got)
	}
	if got := durString(-time.Second); got != "" {
		t.Fatalf("durString(-1s) = %q, want empty", got)
	}
	if got := durString(90 * time.Minute); got != "1h30m0s" {
		t.Fatalf("durString(90m) = %q, want 1h30m0s", got)
	}
}

// TestRunPushPolicyEndToEnd drives runPush against a live in-process controller
// and confirms the assignment lands on the registry.
func TestRunPushPolicyEndToEnd(t *testing.T) {
	reg, err := fleet.NewHostRegistry("", "")
	if err != nil {
		t.Fatalf("NewHostRegistry: %v", err)
	}
	if _, err := reg.Register(fleetproto.Register{HostID: "h1"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	srv := httptest.NewServer(fleet.NewController(reg).Handler())
	t.Cleanup(srv.Close)

	args := []string{
		"-controller", srv.URL,
		"-kind", "policy",
		"-hosts", "h1",
		"-idle-timeout", "20m",
		"-run-budget", "4",
	}
	if err := runPush(args); err != nil {
		t.Fatalf("runPush: %v", err)
	}

	lease := reg.List()[0].LeaseID
	got, err := reg.Assignments("h1", lease)
	if err != nil {
		t.Fatalf("assignments: %v", err)
	}
	if len(got) != 1 || got[0].Kind != fleetproto.KindPolicy {
		t.Fatalf("assignments = %+v, want one policy", got)
	}
}

func TestRunPushImageGCEndToEnd(t *testing.T) {
	reg, err := fleet.NewHostRegistry("", "")
	if err != nil {
		t.Fatalf("NewHostRegistry: %v", err)
	}
	if _, err := reg.Register(fleetproto.Register{HostID: "h1"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	srv := httptest.NewServer(fleet.NewController(reg).Handler())
	t.Cleanup(srv.Close)

	if err := runPush([]string{"-controller", srv.URL, "-kind", "image-gc"}); err != nil {
		t.Fatalf("runPush image-gc: %v", err)
	}
	lease := reg.List()[0].LeaseID
	got, _ := reg.Assignments("h1", lease)
	if len(got) != 1 || got[0].Kind != fleetproto.KindImageGC {
		t.Fatalf("assignments = %+v, want one image-gc", got)
	}
}

func TestRunPushUnknownKind(t *testing.T) {
	// An unknown kind fails before any HTTP call, so no server is needed.
	if err := runPush([]string{"-controller", "http://127.0.0.1:0", "-kind", "bogus"}); err == nil {
		t.Fatal("expected unknown kind to error")
	}
}

func TestPostJSONNon200(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux()) // empty mux -> 404 on any path
	t.Cleanup(srv.Close)
	var out fleet.PushResult
	if err := postJSON(context.Background(), srv.URL+"/nope", "", fleet.PushImageGCRequest{}, &out); err == nil {
		t.Fatal("expected non-200 to error")
	}
}
