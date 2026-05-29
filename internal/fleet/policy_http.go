// cove fleet control plane (paid layer) — see docs/designs/046-fleet-control-plane.md
package fleet

import (
	"net/http"
	"time"

	"github.com/tmc/cove/internal/fleet/fleetproto"
)

// Operator-facing HTTP paths for fleet-wide policy and GC push. They are
// authenticated with the controller's register token (its admin secret),
// distinct from the per-host worker lease used on the four worker verbs.
const (
	PathPushPolicy  = "/v1/push/policy"
	PathPushImageGC = "/v1/push/image-gc"
	PathResults     = "/v1/results"
)

// PushPolicyRequest is the operator's request to push a fleet-wide policy.
// Durations are Go duration strings ("30m", "24h"); an empty Hosts slice targets
// every registered host.
type PushPolicyRequest struct {
	IdleTimeout string   `json:"idle_timeout,omitempty"`
	MaxAge      string   `json:"max_age,omitempty"`
	RunBudget   int      `json:"run_budget,omitempty"`
	Hosts       []string `json:"hosts,omitempty"`
}

// PushImageGCRequest is the operator's request to push image-gc. An empty Hosts
// slice targets every registered host.
type PushImageGCRequest struct {
	Hosts []string `json:"hosts,omitempty"`
}

// toPolicy parses the request durations into a validated FleetPolicy.
func (r PushPolicyRequest) toPolicy() (FleetPolicy, error) {
	var p FleetPolicy
	if r.IdleTimeout != "" {
		d, err := time.ParseDuration(r.IdleTimeout)
		if err != nil {
			return FleetPolicy{}, err
		}
		p.IdleTimeout = d
	}
	if r.MaxAge != "" {
		d, err := time.ParseDuration(r.MaxAge)
		if err != nil {
			return FleetPolicy{}, err
		}
		p.MaxAge = d
	}
	p.RunBudget = r.RunBudget
	return p, nil
}

// RegisterOperatorHandlers adds the operator-facing push and results endpoints
// to mux. They are exposed separately from Handler so a deployment can serve
// them on a different (e.g. admin-only) listener if desired.
func (c *Controller) RegisterOperatorHandlers(mux *http.ServeMux) {
	mux.HandleFunc(PathPushPolicy, c.handlePushPolicy)
	mux.HandleFunc(PathPushImageGC, c.handlePushImageGC)
	mux.HandleFunc(PathResults, c.handleResults)
}

// operatorAuthorized reports whether the request carries the controller's admin
// secret (the register token). When no register token is configured the
// endpoints are open, matching the register verb's behavior.
func (c *Controller) operatorAuthorized(r *http.Request) bool {
	want := c.Registry.regToken
	if want == "" {
		return true
	}
	return fleetproto.BearerToken(r) == want
}

func (c *Controller) handlePushPolicy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fleetproto.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !c.operatorAuthorized(r) {
		fleetproto.WriteError(w, http.StatusUnauthorized, "operator token required")
		return
	}
	req, err := fleetproto.DecodeJSON[PushPolicyRequest](r)
	if err != nil {
		fleetproto.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	policy, err := req.toPolicy()
	if err != nil {
		fleetproto.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	res, err := c.Registry.PushPolicy(policy, req.Hosts)
	if err != nil {
		fleetproto.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	fleetproto.WriteJSON(w, http.StatusOK, res)
}

func (c *Controller) handlePushImageGC(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fleetproto.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !c.operatorAuthorized(r) {
		fleetproto.WriteError(w, http.StatusUnauthorized, "operator token required")
		return
	}
	req, err := fleetproto.DecodeJSON[PushImageGCRequest](r)
	if err != nil {
		fleetproto.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	res := c.Registry.PushImageGC(req.Hosts)
	fleetproto.WriteJSON(w, http.StatusOK, res)
}

func (c *Controller) handleResults(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		fleetproto.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !c.operatorAuthorized(r) {
		fleetproto.WriteError(w, http.StatusUnauthorized, "operator token required")
		return
	}
	kind := r.URL.Query().Get("kind")
	results := c.Registry.AggregateResults(kind)
	if results == nil {
		results = []HostResult{}
	}
	fleetproto.WriteJSON(w, http.StatusOK, results)
}
