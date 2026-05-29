// cove fleet control plane (paid layer) — see docs/designs/046-fleet-control-plane.md
package fleet

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// Role is a coarse capability tier. Roles compose with namespaces: a grant binds
// a role to a subject within one namespace (or the wildcard namespace), so a
// viewer in "team-a" cannot see "team-b" resources.
type Role string

// The three built-in roles, ordered by privilege. admin can do everything
// including push fleet policy; operator can place and control VMs; viewer is
// read-only.
const (
	RoleAdmin    Role = "admin"
	RoleOperator Role = "operator"
	RoleViewer   Role = "viewer"
)

// Action names a controller operation guarded by RBAC. The mutating actions
// (place, shell, stop, delete, push-policy) are also audit-logged.
type Action string

// The guarded actions. view is read-only; the rest mutate fleet or VM state.
const (
	ActionView       Action = "view"
	ActionPlace      Action = "place"
	ActionShell      Action = "shell"
	ActionStop       Action = "stop"
	ActionDelete     Action = "delete"
	ActionPushPolicy Action = "push-policy"
)

// NamespaceWildcard matches any namespace. A grant in this namespace applies
// fleet-wide; a resource in this namespace is visible to any subject with a
// matching action grant.
const NamespaceWildcard = "*"

// Subject is an authenticated identity (a human via SSO or a service account via
// a bearer key). Roles holds the per-namespace role grants resolved by the
// authenticator. The same subject may hold different roles in different
// namespaces.
type Subject struct {
	// ID is the stable identity string (e.g. "alice@example.com" or
	// "svc-ci"). It is recorded in the audit log.
	ID string `json:"id"`
	// Grants binds roles to namespaces. A grant in NamespaceWildcard applies to
	// every namespace.
	Grants []Grant `json:"grants,omitempty"`
}

// Grant binds a role to a namespace for a subject.
type Grant struct {
	Namespace string `json:"namespace"`
	Role      Role   `json:"role"`
}

// Resource is the target of an action. Namespace is the tenant boundary; ID is
// the optional sandbox/VM identifier (empty for fleet-wide actions like
// push-policy). A resource in NamespaceWildcard is unscoped.
type Resource struct {
	Namespace string
	ID        string
}

// roleActions lists the actions each role may perform. admin is a superset of
// operator, which is a superset of viewer. push-policy is admin-only.
var roleActions = map[Role]map[Action]bool{
	RoleViewer: {
		ActionView: true,
	},
	RoleOperator: {
		ActionView:   true,
		ActionPlace:  true,
		ActionShell:  true,
		ActionStop:   true,
		ActionDelete: true,
	},
	RoleAdmin: {
		ActionView:       true,
		ActionPlace:      true,
		ActionShell:      true,
		ActionStop:       true,
		ActionDelete:     true,
		ActionPushPolicy: true,
	},
}

// roleAllows reports whether role grants action.
func roleAllows(role Role, action Action) bool {
	return roleActions[role][action]
}

// Policy answers authorization questions against a subject's grants. It is a
// pure decision function with no state of its own; the durable grant store is
// RBACStore. The zero value is usable and denies everything.
type Policy struct{}

// Can reports whether subject may perform action on resource. The subject must
// hold a role that allows action in either the resource's namespace or the
// wildcard namespace. A push-policy action ignores the resource namespace and
// requires a wildcard (fleet-wide) admin grant, since fleet policy is not
// namespaced.
func (Policy) Can(subject Subject, action Action, resource Resource) bool {
	for _, g := range subject.Grants {
		if !roleAllows(g.Role, action) {
			continue
		}
		if action == ActionPushPolicy {
			// Fleet policy is global; only a wildcard grant authorizes it.
			if g.Namespace == NamespaceWildcard {
				return true
			}
			continue
		}
		if namespaceMatch(g.Namespace, resource.Namespace) {
			return true
		}
	}
	return false
}

// namespaceMatch reports whether a grant namespace covers a resource namespace.
// A wildcard grant covers any resource; a wildcard resource is covered only by a
// wildcard grant (it is unscoped and thus fleet-wide).
func namespaceMatch(grantNS, resourceNS string) bool {
	if grantNS == NamespaceWildcard {
		return true
	}
	if resourceNS == NamespaceWildcard {
		return false
	}
	return grantNS == resourceNS
}

// rbacState is the persisted shape of the RBAC store: subjects keyed by id and
// a record of which namespace each resource (sandbox) belongs to.
type rbacState struct {
	Subjects   map[string]*Subject `json:"subjects"`
	Namespaces map[string]string   `json:"resource_namespaces,omitempty"`
}

// RBACStore is the controller's in-memory grant store backed by the same
// JSON-file persistence pattern as HostRegistry: a mutex-guarded map written
// atomically via tmp+rename. It also records the namespace a resource belongs
// to so the middleware can resolve a sandbox id to its tenant. The zero value is
// not usable; build one with NewRBACStore.
type RBACStore struct {
	mu         sync.Mutex
	path       string
	subjects   map[string]*Subject
	namespaces map[string]string
}

// NewRBACStore opens (or creates) the store backed by statePath. An empty
// statePath keeps the store entirely in memory (no persistence).
func NewRBACStore(statePath string) (*RBACStore, error) {
	s := &RBACStore{
		path:       statePath,
		subjects:   make(map[string]*Subject),
		namespaces: make(map[string]string),
	}
	if statePath != "" {
		if err := s.load(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *RBACStore) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read rbac state: %w", err)
	}
	var st rbacState
	if err := json.Unmarshal(data, &st); err != nil {
		return fmt.Errorf("parse rbac state: %w", err)
	}
	if st.Subjects != nil {
		s.subjects = st.Subjects
	}
	if st.Namespaces != nil {
		s.namespaces = st.Namespaces
	}
	return nil
}

// persist writes the store atomically. The caller must hold s.mu.
func (s *RBACStore) persist() error {
	if s.path == "" {
		return nil
	}
	st := rbacState{Subjects: s.subjects, Namespaces: s.namespaces}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal rbac state: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return fmt.Errorf("create rbac state dir: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0600); err != nil {
		return fmt.Errorf("write rbac state: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename rbac state: %w", err)
	}
	return nil
}

// Grant binds role to subjectID within namespace, persisting the change. An
// empty namespace is treated as the wildcard.
func (s *RBACStore) Grant(subjectID string, role Role, namespace string) error {
	if subjectID == "" {
		return fmt.Errorf("grant: subject id required")
	}
	if _, ok := roleActions[role]; !ok {
		return fmt.Errorf("grant: unknown role %q", role)
	}
	if namespace == "" {
		namespace = NamespaceWildcard
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	subj := s.subjects[subjectID]
	if subj == nil {
		subj = &Subject{ID: subjectID}
		s.subjects[subjectID] = subj
	}
	for i, g := range subj.Grants {
		if g.Namespace == namespace {
			subj.Grants[i].Role = role
			return s.persist()
		}
	}
	subj.Grants = append(subj.Grants, Grant{Namespace: namespace, Role: role})
	return s.persist()
}

// Subject returns a copy of the stored subject for id. The boolean reports
// whether the subject is known.
func (s *RBACStore) Subject(id string) (Subject, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	subj := s.subjects[id]
	if subj == nil {
		return Subject{}, false
	}
	return subj.copy(), true
}

// copy returns a deep copy so callers cannot mutate the stored grants.
func (s *Subject) copy() Subject {
	out := Subject{ID: s.ID}
	if len(s.Grants) > 0 {
		out.Grants = append([]Grant(nil), s.Grants...)
	}
	return out
}

// Subjects returns every stored subject sorted by id.
func (s *RBACStore) Subjects() []Subject {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Subject, 0, len(s.subjects))
	for _, subj := range s.subjects {
		out = append(out, subj.copy())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// SetResourceNamespace records that a resource (sandbox/VM) id belongs to a
// namespace, so the middleware can resolve a bare id to its tenant for
// authorization. An empty namespace is treated as the wildcard.
func (s *RBACStore) SetResourceNamespace(resourceID, namespace string) error {
	if resourceID == "" {
		return fmt.Errorf("set resource namespace: resource id required")
	}
	if namespace == "" {
		namespace = NamespaceWildcard
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.namespaces[resourceID] = namespace
	return s.persist()
}

// ResourceNamespace returns the namespace a resource belongs to. Unknown
// resources resolve to the wildcard namespace, which only wildcard grants cover.
func (s *RBACStore) ResourceNamespace(resourceID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ns, ok := s.namespaces[resourceID]; ok {
		return ns
	}
	return NamespaceWildcard
}
