// cove fleet control plane (paid layer) — see docs/designs/046-fleet-control-plane.md
package fleet

import (
	"net/http"
	"strings"

	"github.com/tmc/cove/internal/fleet/fleetproto"
)

// AccessControl ties the RBAC store, an authenticator, and the audit log into
// one HTTP middleware: authenticate -> authorize -> audit-append -> handle. It
// is applied to the hosted /v1 surface and the controller's operator endpoints
// so every mutating action is gated and recorded against the authenticated
// SSO/service-account identity. The zero value is not usable; build one with
// NewAccessControl.
type AccessControl struct {
	auth  Authenticator
	store *RBACStore
	audit *AuditLog
}

// NewAccessControl builds the middleware over an authenticator, RBAC store, and
// audit log. The store resolves a resource id to its namespace; the audit log
// records the outcome of every mutating request. A nil audit log disables
// recording (authorization still runs).
func NewAccessControl(auth Authenticator, store *RBACStore, audit *AuditLog) *AccessControl {
	return &AccessControl{auth: auth, store: store, audit: audit}
}

// resourceResolver maps a request to the RBAC (action, resource) it requires.
// Returning a view action on a wildcard resource is the safe default for an
// unrecognized read; a mutating route should return its precise action so the
// audit log is accurate.
type resourceResolver func(r *http.Request) (Action, Resource)

// Protect wraps next with the authenticate -> authorize -> audit pipeline,
// deriving the required action and resource from resolve. A request that fails
// authentication gets 401; one that fails authorization gets 403 and a denied
// audit entry for any mutating action. On success the handler runs and a
// mutating action is recorded as allowed.
func (ac *AccessControl) Protect(resolve resourceResolver, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		subject, err := ac.auth.Authenticate(r)
		if err != nil {
			fleetproto.WriteError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		action, resource := resolve(r)
		if !(Policy{}).Can(subject, action, resource) {
			ac.record(subject.ID, action, resource, AuditDenied)
			fleetproto.WriteError(w, http.StatusForbidden, "forbidden")
			return
		}
		ac.record(subject.ID, action, resource, AuditAllowed)
		next.ServeHTTP(w, r)
	})
}

// record appends a mutating action to the audit log. Read-only (view) actions
// are not recorded, keeping the log focused on who-did-what mutations. A nil
// audit log is a no-op.
func (ac *AccessControl) record(subject string, action Action, resource Resource, result AuditResult) {
	if ac.audit == nil || action == ActionView {
		return
	}
	_, _ = ac.audit.Append(subject, action, resourceString(resource), result)
}

// resourceString renders a resource for the audit log: "namespace/id",
// "namespace" when id is empty, or "" when both are unset (fleet-wide).
func resourceString(res Resource) string {
	switch {
	case res.Namespace == "" && res.ID == "":
		return ""
	case res.ID == "":
		return res.Namespace
	default:
		return res.Namespace + "/" + res.ID
	}
}

// SandboxResolver derives the RBAC action and resource from a hosted
// /v1/sandboxes request. It maps the REST verb to an Action and resolves the
// sandbox id to its namespace via the store so namespace isolation is enforced
// per resource. The base prefix is the path the routes are mounted under
// (typically PathSandboxes).
func (ac *AccessControl) SandboxResolver(r *http.Request) (Action, Resource) {
	id, verb := splitSandboxPath(r.URL.Path)
	ns := NamespaceWildcard
	if id != "" && ac.store != nil {
		ns = ac.store.ResourceNamespace(id)
	}
	return sandboxAction(r.Method, verb), Resource{Namespace: ns, ID: id}
}

// splitSandboxPath extracts the sandbox id and verb from a /v1/sandboxes path.
// "/v1/sandboxes" yields ("", ""); "/v1/sandboxes/sb-x" yields ("sb-x", "");
// "/v1/sandboxes/sb-x/stop" yields ("sb-x", "stop").
func splitSandboxPath(path string) (id, verb string) {
	rest := strings.TrimPrefix(path, PathSandboxes)
	rest = strings.TrimPrefix(rest, "/")
	if rest == "" {
		return "", ""
	}
	id, verb, _ = strings.Cut(rest, "/")
	return id, verb
}

// sandboxAction maps an HTTP method and verb to the guarded RBAC action.
func sandboxAction(method, verb string) Action {
	switch verb {
	case "stop":
		return ActionStop
	case "start":
		return ActionPlace
	case "shell":
		return ActionShell
	case "cordon":
		return ActionStop
	}
	switch method {
	case http.MethodPost:
		return ActionPlace
	case http.MethodDelete:
		return ActionDelete
	default:
		return ActionView
	}
}

// OperatorResolver derives the RBAC action for a controller operator endpoint.
// Policy push is fleet-wide (wildcard resource); results is a view. It backs the
// middleware over the Slice 5/6 operator surface.
func (ac *AccessControl) OperatorResolver(r *http.Request) (Action, Resource) {
	switch {
	case strings.HasPrefix(r.URL.Path, PathPushPolicy), strings.HasPrefix(r.URL.Path, PathPushImageGC):
		return ActionPushPolicy, Resource{Namespace: NamespaceWildcard}
	default:
		return ActionView, Resource{Namespace: NamespaceWildcard}
	}
}
