// cove fleet control plane (paid layer) — see docs/designs/046-fleet-control-plane.md
package fleet

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// countingHandler records whether it was reached.
type countingHandler struct{ hits int32 }

func (h *countingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt32(&h.hits, 1)
	w.WriteHeader(http.StatusOK)
}

// newProtectedFixture wires a bearer authenticator over an RBAC store and an
// in-memory audit log, returning the AccessControl and the audit log so tests
// can assert recorded entries.
func newProtectedFixture(t *testing.T) (*AccessControl, *AuditLog, *RBACStore) {
	t.Helper()
	store, err := NewRBACStore("")
	if err != nil {
		t.Fatalf("NewRBACStore: %v", err)
	}
	audit, err := NewAuditLog("")
	if err != nil {
		t.Fatalf("NewAuditLog: %v", err)
	}
	auth := NewBearerAuthenticator(store, map[string]string{
		"tok-a-op":     "op-a",
		"tok-b-op":     "op-b",
		"tok-a-viewer": "viewer-a",
	})
	_ = store.Grant("op-a", RoleOperator, "team-a")
	_ = store.Grant("op-b", RoleOperator, "team-b")
	_ = store.Grant("viewer-a", RoleViewer, "team-a")
	return NewAccessControl(auth, store, audit), audit, store
}

func protectedRequest(method, path, token string) *http.Request {
	r, _ := http.NewRequest(method, path, nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	return r
}

func TestMiddlewareDeniesUnauthenticatedBeforeHandler(t *testing.T) {
	ac, audit, _ := newProtectedFixture(t)
	h := &countingHandler{}
	protected := ac.Protect(ac.SandboxResolver, h)

	rec := httptest.NewRecorder()
	protected.ServeHTTP(rec, protectedRequest(http.MethodPost, PathSandboxes, ""))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if atomic.LoadInt32(&h.hits) != 0 {
		t.Error("handler was reached on an unauthenticated request")
	}
	// Authentication failures are not audited (no identity to record).
	if n := len(audit.Entries()); n != 0 {
		t.Errorf("audit entries = %d, want 0 on auth failure", n)
	}
}

func TestMiddlewareNamespaceIsolation(t *testing.T) {
	ac, audit, store := newProtectedFixture(t)
	// sb-a belongs to team-a, sb-b to team-b.
	_ = store.SetResourceNamespace("sb-a", "team-a")
	_ = store.SetResourceNamespace("sb-b", "team-b")
	h := &countingHandler{}
	protected := ac.Protect(ac.SandboxResolver, h)

	tests := []struct {
		name       string
		token      string
		path       string
		wantStatus int
		wantHit    bool
	}{
		{name: "op-a stops own team-a sandbox", token: "tok-a-op", path: pathSandbox + "sb-a/stop", wantStatus: http.StatusOK, wantHit: true},
		{name: "op-a cannot stop team-b sandbox", token: "tok-a-op", path: pathSandbox + "sb-b/stop", wantStatus: http.StatusForbidden, wantHit: false},
		{name: "op-b cannot delete team-a sandbox", token: "tok-b-op", path: pathSandbox + "sb-a", wantStatus: http.StatusForbidden, wantHit: false},
		{name: "viewer-a cannot stop team-a sandbox", token: "tok-a-viewer", path: pathSandbox + "sb-a/stop", wantStatus: http.StatusForbidden, wantHit: false},
		{name: "viewer-a can view team-a sandbox", token: "tok-a-viewer", path: pathSandbox + "sb-a", wantStatus: http.StatusOK, wantHit: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := atomic.LoadInt32(&h.hits)
			rec := httptest.NewRecorder()
			method := http.MethodPost
			if tt.path == pathSandbox+"sb-a" || tt.path == pathSandbox+"sb-b" {
				// {id} root: GET view vs DELETE.
				if tt.wantStatus == http.StatusOK {
					method = http.MethodGet
				} else {
					method = http.MethodDelete
				}
			}
			protected.ServeHTTP(rec, protectedRequest(method, tt.path, tt.token))
			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			hit := atomic.LoadInt32(&h.hits) > before
			if hit != tt.wantHit {
				t.Errorf("handler reached = %v, want %v", hit, tt.wantHit)
			}
		})
	}

	// Every mutating decision (allowed or denied) should be audited; views are
	// not. The chain must remain intact.
	if idx := audit.Verify(); idx != -1 {
		t.Errorf("audit chain tampered at %d", idx)
	}
	var denied, allowed int
	for _, e := range audit.Entries() {
		if e.Action == ActionView {
			t.Errorf("view action %+v should not be audited", e)
		}
		switch e.Result {
		case AuditDenied:
			denied++
		case AuditAllowed:
			allowed++
		}
	}
	if denied == 0 {
		t.Error("expected at least one denied audit entry")
	}
	if allowed == 0 {
		t.Error("expected at least one allowed audit entry")
	}
}

func TestMiddlewareAuditsMutatingActions(t *testing.T) {
	ac, audit, store := newProtectedFixture(t)
	_ = store.SetResourceNamespace("sb-a", "team-a")
	h := &countingHandler{}
	protected := ac.Protect(ac.SandboxResolver, h)

	rec := httptest.NewRecorder()
	protected.ServeHTTP(rec, protectedRequest(http.MethodPost, pathSandbox+"sb-a/stop", "tok-a-op"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	entries := audit.Entries()
	if len(entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(entries))
	}
	e := entries[0]
	if e.Subject != "op-a" || e.Action != ActionStop || e.Resource != "team-a/sb-a" || e.Result != AuditAllowed {
		t.Errorf("audit entry = %+v", e)
	}
}

func TestOperatorResolverPushPolicy(t *testing.T) {
	ac, audit, store := newProtectedFixture(t)
	_ = store.Grant("admin", RoleAdmin, NamespaceWildcard)
	auth := NewBearerAuthenticator(store, map[string]string{"tok-admin": "admin", "tok-op": "op-a"})
	ac = NewAccessControl(auth, store, audit)
	h := &countingHandler{}
	protected := ac.Protect(ac.OperatorResolver, h)

	// Admin (wildcard) may push policy.
	rec := httptest.NewRecorder()
	protected.ServeHTTP(rec, protectedRequest(http.MethodPost, PathPushPolicy, "tok-admin"))
	if rec.Code != http.StatusOK {
		t.Errorf("admin push status = %d, want 200", rec.Code)
	}

	// Operator may not.
	rec = httptest.NewRecorder()
	protected.ServeHTTP(rec, protectedRequest(http.MethodPost, PathPushPolicy, "tok-op"))
	if rec.Code != http.StatusForbidden {
		t.Errorf("operator push status = %d, want 403", rec.Code)
	}
}

func TestSplitSandboxPath(t *testing.T) {
	tests := []struct {
		path    string
		wantID  string
		wantVrb string
	}{
		{PathSandboxes, "", ""},
		{PathSandboxes + "/", "", ""},
		{pathSandbox + "sb-1", "sb-1", ""},
		{pathSandbox + "sb-1/stop", "sb-1", "stop"},
		{pathSandbox + "sb-1/wait", "sb-1", "wait"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			id, verb := splitSandboxPath(tt.path)
			if id != tt.wantID || verb != tt.wantVrb {
				t.Errorf("splitSandboxPath(%q) = (%q,%q), want (%q,%q)", tt.path, id, verb, tt.wantID, tt.wantVrb)
			}
		})
	}
}
