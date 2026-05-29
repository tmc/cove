// cove fleet control plane (paid layer) — see docs/designs/046-fleet-control-plane.md
package fleet

import (
	"path/filepath"
	"testing"
)

func TestPolicyCanRolesAndNamespaces(t *testing.T) {
	tests := []struct {
		name    string
		subject Subject
		action  Action
		res     Resource
		want    bool
	}{
		{
			name:    "viewer can view own namespace",
			subject: Subject{ID: "v", Grants: []Grant{{Namespace: "team-a", Role: RoleViewer}}},
			action:  ActionView,
			res:     Resource{Namespace: "team-a"},
			want:    true,
		},
		{
			name:    "viewer cannot stop",
			subject: Subject{ID: "v", Grants: []Grant{{Namespace: "team-a", Role: RoleViewer}}},
			action:  ActionStop,
			res:     Resource{Namespace: "team-a"},
			want:    false,
		},
		{
			name:    "viewer cannot see other namespace",
			subject: Subject{ID: "v", Grants: []Grant{{Namespace: "team-a", Role: RoleViewer}}},
			action:  ActionView,
			res:     Resource{Namespace: "team-b"},
			want:    false,
		},
		{
			name:    "operator can stop own namespace",
			subject: Subject{ID: "o", Grants: []Grant{{Namespace: "team-a", Role: RoleOperator}}},
			action:  ActionStop,
			res:     Resource{Namespace: "team-a"},
			want:    true,
		},
		{
			name:    "operator cannot stop other namespace",
			subject: Subject{ID: "o", Grants: []Grant{{Namespace: "team-a", Role: RoleOperator}}},
			action:  ActionStop,
			res:     Resource{Namespace: "team-b"},
			want:    false,
		},
		{
			name:    "operator cannot push policy",
			subject: Subject{ID: "o", Grants: []Grant{{Namespace: NamespaceWildcard, Role: RoleOperator}}},
			action:  ActionPushPolicy,
			res:     Resource{Namespace: NamespaceWildcard},
			want:    false,
		},
		{
			name:    "admin wildcard can push policy",
			subject: Subject{ID: "a", Grants: []Grant{{Namespace: NamespaceWildcard, Role: RoleAdmin}}},
			action:  ActionPushPolicy,
			res:     Resource{Namespace: NamespaceWildcard},
			want:    true,
		},
		{
			name:    "namespaced admin cannot push fleet policy",
			subject: Subject{ID: "a", Grants: []Grant{{Namespace: "team-a", Role: RoleAdmin}}},
			action:  ActionPushPolicy,
			res:     Resource{Namespace: "team-a"},
			want:    false,
		},
		{
			name:    "wildcard grant covers any namespace",
			subject: Subject{ID: "a", Grants: []Grant{{Namespace: NamespaceWildcard, Role: RoleOperator}}},
			action:  ActionDelete,
			res:     Resource{Namespace: "team-z"},
			want:    true,
		},
		{
			name:    "namespaced grant does not cover wildcard resource",
			subject: Subject{ID: "o", Grants: []Grant{{Namespace: "team-a", Role: RoleOperator}}},
			action:  ActionDelete,
			res:     Resource{Namespace: NamespaceWildcard},
			want:    false,
		},
		{
			name:    "no grants denies",
			subject: Subject{ID: "x"},
			action:  ActionView,
			res:     Resource{Namespace: "team-a"},
			want:    false,
		},
		{
			name:    "multiple grants pick the right namespace",
			subject: Subject{ID: "m", Grants: []Grant{{Namespace: "team-a", Role: RoleViewer}, {Namespace: "team-b", Role: RoleOperator}}},
			action:  ActionStop,
			res:     Resource{Namespace: "team-b"},
			want:    true,
		},
		{
			name:    "operator shell allowed in namespace",
			subject: Subject{ID: "o", Grants: []Grant{{Namespace: "team-a", Role: RoleOperator}}},
			action:  ActionShell,
			res:     Resource{Namespace: "team-a", ID: "sb-1"},
			want:    true,
		},
	}
	var p Policy
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := p.Can(tt.subject, tt.action, tt.res); got != tt.want {
				t.Errorf("Can(%s, %s, %+v) = %v, want %v", tt.subject.ID, tt.action, tt.res, got, tt.want)
			}
		})
	}
}

func TestRBACStoreGrantAndPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rbac.json")
	store, err := NewRBACStore(path)
	if err != nil {
		t.Fatalf("NewRBACStore: %v", err)
	}
	if err := store.Grant("alice", RoleOperator, "team-a"); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	// Regrant a different role in the same namespace replaces, not appends.
	if err := store.Grant("alice", RoleAdmin, "team-a"); err != nil {
		t.Fatalf("Grant regrant: %v", err)
	}
	if err := store.Grant("alice", RoleViewer, "team-b"); err != nil {
		t.Fatalf("Grant team-b: %v", err)
	}

	subj, ok := store.Subject("alice")
	if !ok {
		t.Fatal("Subject(alice) not found")
	}
	if len(subj.Grants) != 2 {
		t.Fatalf("grants = %d, want 2 (regrant must replace): %+v", len(subj.Grants), subj.Grants)
	}

	// Reload from disk and confirm grants survived.
	reopened, err := NewRBACStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	rs, ok := reopened.Subject("alice")
	if !ok {
		t.Fatal("reopened Subject(alice) not found")
	}
	var p Policy
	if !p.Can(rs, ActionStop, Resource{Namespace: "team-a"}) {
		t.Error("reloaded alice should be admin in team-a")
	}
	if p.Can(rs, ActionStop, Resource{Namespace: "team-b"}) {
		t.Error("reloaded alice should be viewer-only in team-b")
	}
}

func TestRBACStoreGrantErrors(t *testing.T) {
	store, err := NewRBACStore("")
	if err != nil {
		t.Fatalf("NewRBACStore: %v", err)
	}
	if err := store.Grant("", RoleAdmin, "ns"); err == nil {
		t.Error("Grant with empty subject should error")
	}
	if err := store.Grant("bob", Role("superuser"), "ns"); err == nil {
		t.Error("Grant with unknown role should error")
	}
}

func TestRBACStoreResourceNamespace(t *testing.T) {
	store, err := NewRBACStore(filepath.Join(t.TempDir(), "rbac.json"))
	if err != nil {
		t.Fatalf("NewRBACStore: %v", err)
	}
	if err := store.SetResourceNamespace("sb-1", "team-a"); err != nil {
		t.Fatalf("SetResourceNamespace: %v", err)
	}
	if got := store.ResourceNamespace("sb-1"); got != "team-a" {
		t.Errorf("ResourceNamespace(sb-1) = %q, want team-a", got)
	}
	// Unknown resource resolves to wildcard (only wildcard grants cover it).
	if got := store.ResourceNamespace("sb-unknown"); got != NamespaceWildcard {
		t.Errorf("ResourceNamespace(unknown) = %q, want %q", got, NamespaceWildcard)
	}
	if err := store.SetResourceNamespace("", "team-a"); err == nil {
		t.Error("SetResourceNamespace with empty id should error")
	}
}

func TestSubjectCopyIsolation(t *testing.T) {
	store, _ := NewRBACStore("")
	_ = store.Grant("eve", RoleViewer, "team-a")
	subj, _ := store.Subject("eve")
	subj.Grants[0].Role = RoleAdmin // mutate the returned copy

	again, _ := store.Subject("eve")
	if again.Grants[0].Role != RoleViewer {
		t.Error("Subject must return a copy; caller mutation leaked into the store")
	}
}
