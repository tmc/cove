// cove fleet control plane (paid layer) — see docs/designs/046-fleet-control-plane.md
package main

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/tmc/cove/internal/fleet"
)

func TestParsePairs(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want map[string]string
	}{
		{name: "empty", in: "", want: nil},
		{name: "single", in: "tok=svc", want: map[string]string{"tok": "svc"}},
		{name: "multiple with spaces", in: " tok1=svc1 , tok2=svc2 ", want: map[string]string{"tok1": "svc1", "tok2": "svc2"}},
		{name: "drops malformed", in: "tok=svc,nopair,=empty,tok2=", want: map[string]string{"tok": "svc"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parsePairs(tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parsePairs(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestBuildAccessControlDisabledByDefault(t *testing.T) {
	ac, err := buildAccessControl("", "", "", "")
	if err != nil {
		t.Fatalf("buildAccessControl: %v", err)
	}
	if ac != nil {
		t.Error("access control should be nil when no service accounts or OIDC secret are configured")
	}
}

func TestMountProtectedEnforcesRBAC(t *testing.T) {
	dir := t.TempDir()
	// Configure a single operator service account scoped to team-a.
	ac, err := buildAccessControl(dir+"/rbac.json", dir+"/audit.jsonl", "tok-op=op-a", "")
	if err != nil {
		t.Fatalf("buildAccessControl: %v", err)
	}
	if ac == nil {
		t.Fatal("access control should be built when service accounts are configured")
	}

	reg, err := fleet.NewHostRegistry("", "")
	if err != nil {
		t.Fatalf("NewHostRegistry: %v", err)
	}
	controller := fleet.NewController(reg)
	store := fleet.NewSandboxStore()
	hosted := fleet.NewHostedAPI(reg, store, fleet.NewUsageLedger(), reg, nil)

	mux := http.NewServeMux()
	controller.RegisterWorkerHandlers(mux)
	mountProtected(mux, ac, controller, hosted)

	srv := httptest.NewServer(mux)
	defer srv.Close()

	// No credential: the hosted surface rejects before the handler runs.
	resp, err := http.Get(srv.URL + fleet.PathSandboxes)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("unauthenticated GET status = %d, want 401", resp.StatusCode)
	}

	// Operator (not admin) cannot push fleet policy.
	req, _ := http.NewRequest(http.MethodPost, srv.URL+fleet.PathPushPolicy, http.NoBody)
	req.Header.Set("Authorization", "Bearer tok-op")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("push policy: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("operator push-policy status = %d, want 403", resp.StatusCode)
	}
}
