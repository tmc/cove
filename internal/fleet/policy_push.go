// cove fleet control plane (paid layer) — see docs/designs/046-fleet-control-plane.md
package fleet

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/tmc/cove/internal/fleet/fleetproto"
)

// FleetPolicy is the fleet-wide lifecycle policy the controller pushes to
// workers. It mirrors the per-host vmpolicy fields (idle timeout, max age, run
// budget) so a worker can apply it to its local VMs without translation. A zero
// FleetPolicy is empty: pushing it clears no thresholds, it simply sets none.
type FleetPolicy struct {
	IdleTimeout time.Duration
	MaxAge      time.Duration
	RunBudget   int
}

// Validate rejects negative thresholds, matching vmpolicy semantics.
func (p FleetPolicy) Validate() error {
	if p.IdleTimeout < 0 {
		return fmt.Errorf("idle timeout must be non-negative")
	}
	if p.MaxAge < 0 {
		return fmt.Errorf("max age must be non-negative")
	}
	if p.RunBudget < 0 {
		return fmt.Errorf("run budget must be non-negative")
	}
	return nil
}

// payload encodes the policy as the wire PolicyPayload, using Go duration
// strings so the shape matches the per-VM policy file. Unset (zero) durations
// serialize to the empty string and are omitted.
func (p FleetPolicy) payload() fleetproto.PolicyPayload {
	var pp fleetproto.PolicyPayload
	if p.IdleTimeout > 0 {
		pp.IdleTimeout = p.IdleTimeout.String()
	}
	if p.MaxAge > 0 {
		pp.MaxAge = p.MaxAge.String()
	}
	pp.RunBudget = p.RunBudget
	return pp
}

// PushOutcome is the controller-side record of enqueueing one assignment to one
// host. AssignmentID is the queue ID the worker will report against; Error is
// set if the host could not be enqueued (e.g. not registered).
type PushOutcome struct {
	HostID       string `json:"host_id"`
	AssignmentID string `json:"assignment_id,omitempty"`
	Error        string `json:"error,omitempty"`
}

// PushResult aggregates the per-host enqueue outcomes of a single push together
// with success and failure tallies. It is returned immediately at enqueue time;
// worker results land later via ReportStatus and are read back with
// AggregateResults.
type PushResult struct {
	Kind     string        `json:"kind"`
	Outcomes []PushOutcome `json:"outcomes"`
	Enqueued int           `json:"enqueued"`
	Failed   int           `json:"failed"`
}

// PushPolicy enqueues a KindPolicy assignment carrying policy to each host in
// hosts. An empty hosts slice targets every registered host. Enqueue is
// fail-soft: a host that cannot be enqueued is recorded as a failed outcome
// rather than aborting the push.
func (r *HostRegistry) PushPolicy(policy FleetPolicy, hosts []string) (PushResult, error) {
	if err := policy.Validate(); err != nil {
		return PushResult{}, fmt.Errorf("push policy: %w", err)
	}
	payload, err := json.Marshal(policy.payload())
	if err != nil {
		return PushResult{}, fmt.Errorf("push policy: encode payload: %w", err)
	}
	return r.pushKind(fleetproto.KindPolicy, payload, hosts), nil
}

// PushImageGC enqueues a KindImageGC assignment to each host in hosts. An empty
// hosts slice targets every registered host.
func (r *HostRegistry) PushImageGC(hosts []string) PushResult {
	return r.pushKind(fleetproto.KindImageGC, nil, hosts)
}

// pushKind enqueues one assignment of the given kind+payload to each target
// host. When hosts is empty it targets every registered host. It records the
// kind on each enqueued assignment-id for later aggregation.
func (r *HostRegistry) pushKind(kind string, payload []byte, hosts []string) PushResult {
	targets := hosts
	if len(targets) == 0 {
		targets = r.hostIDs()
	}
	res := PushResult{Kind: kind, Outcomes: make([]PushOutcome, 0, len(targets))}
	for _, hostID := range targets {
		id, err := r.Enqueue(hostID, fleetproto.Assignment{Kind: kind, Payload: payload})
		outcome := PushOutcome{HostID: hostID, AssignmentID: id}
		if err != nil {
			outcome.Error = err.Error()
			res.Failed++
		} else {
			r.recordPushed(id, hostID, kind)
			res.Enqueued++
		}
		res.Outcomes = append(res.Outcomes, outcome)
	}
	sort.Slice(res.Outcomes, func(i, j int) bool { return res.Outcomes[i].HostID < res.Outcomes[j].HostID })
	return res
}

// hostIDs returns the sorted IDs of every registered host.
func (r *HostRegistry) hostIDs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make([]string, 0, len(r.hosts))
	for id := range r.hosts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// HostResult is the controller's view of one host's reported result for a push.
// State is the worker's terminal state (done/failed/refused); Detail is the
// worker's structured payload (a PolicyResult or ImageGCResult JSON). Pending is
// true when the assignment was enqueued but no report has arrived yet.
type HostResult struct {
	HostID       string `json:"host_id"`
	AssignmentID string `json:"assignment_id"`
	Kind         string `json:"kind"`
	State        string `json:"state,omitempty"`
	Detail       string `json:"detail,omitempty"`
	Pending      bool   `json:"pending"`
}

// pushRecord tracks one enqueued assignment so a later ReportStatus can be
// matched back to its host and kind for aggregation.
type pushRecord struct {
	hostID string
	kind   string
	state  string
	detail string
}

func (r *HostRegistry) recordPushed(assignmentID, hostID, kind string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.results == nil {
		r.results = make(map[string]*pushRecord)
	}
	r.results[assignmentID] = &pushRecord{hostID: hostID, kind: kind}
}

// AggregateResults returns the per-host result for every push of the given kind
// (use the empty string to aggregate all kinds). Hosts that were pushed but have
// not yet reported appear with Pending=true. Results are sorted by host ID.
func (r *HostRegistry) AggregateResults(kind string) []HostResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]HostResult, 0, len(r.results))
	for id, rec := range r.results {
		if kind != "" && rec.kind != kind {
			continue
		}
		out = append(out, HostResult{
			HostID:       rec.hostID,
			AssignmentID: id,
			Kind:         rec.kind,
			State:        rec.state,
			Detail:       rec.detail,
			Pending:      rec.state == "",
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].HostID != out[j].HostID {
			return out[i].HostID < out[j].HostID
		}
		return out[i].AssignmentID < out[j].AssignmentID
	})
	return out
}
