// cove fleet control plane (paid layer) — see docs/designs/046-fleet-control-plane.md
package fleet

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// WarmState is the lifecycle of one pre-forked VM in a warm pool.
//
//	Forking -> Warming -> Ready -> Claimed
//	   |          |
//	   +----------+--> Failed
//
// A fork starts in Forking; once the worker's fork call returns it moves to
// Warming while the controller waits for the guest vsock agent to be reachable;
// MarkReady advances it to Ready. Claim takes a Ready VM to Claimed and serves
// it to a job. A fork or agent-wait failure lands in Failed and frees the slot.
type WarmState string

const (
	// WarmForking means the worker fork call is in flight.
	WarmForking WarmState = "forking"
	// WarmWarming means the VM forked and the controller is waiting for its
	// vsock agent to be reachable.
	WarmWarming WarmState = "warming"
	// WarmReady means the VM is forked and its agent answered; it can be claimed.
	WarmReady WarmState = "ready"
	// WarmClaimed means a job took the VM; it is no longer pool capacity.
	WarmClaimed WarmState = "claimed"
	// WarmFailed means fork or agent-wait failed; the slot is freed for refork.
	WarmFailed WarmState = "failed"
)

// WarmConfig declares a warm-pool quota: keep TargetReady ready forks of Ref
// across the fleet. The pool pays the ~6-10s boot-to-agent cost ahead of a job;
// the fork itself is only ~132-140ms.
type WarmConfig struct {
	Ref         string
	TargetReady int
}

// WarmForker forks a ready VM from a base ref on some fleet host and waits for
// its vsock agent, returning the placed VM's identity. It is the warm pool's
// single dependency on the worker lifecycle path; the pool never calls vz
// directly. Implementations delegate to the controller's Schedule + a KindForkRun
// assignment and then poll the guest agent.
type WarmForker interface {
	// Fork places one fork of ref on a feasible host and returns the chosen host
	// and the VM name. It does not wait for the agent; the pool advances the VM
	// to Warming and a separate signal (MarkReady) reports agent readiness.
	Fork(ctx context.Context, ref string) (hostID, vmName string, err error)
}

// warmVM is one tracked pool member.
type warmVM struct {
	id     string
	ref    string
	hostID string
	vmName string
	state  WarmState
}

// WarmPool maintains warm-fork quotas across the fleet. It tracks pool members
// through their state machine and refills to each ref's target on claim or
// failure. It is safe for concurrent use.
//
// The pool is intentionally a controller-side bookkeeper: forking delegates to a
// WarmForker (the worker lifecycle path), and agent readiness is reported back
// via MarkReady (driven by the controller's vsock-agent probe).
type WarmPool struct {
	forker WarmForker

	mu      sync.Mutex
	configs map[string]WarmConfig // ref -> quota
	members map[string]*warmVM    // id -> member
	seq     uint64
}

// NewWarmPool builds a pool that forks via forker. A nil forker makes Reconcile
// a no-op (useful before the worker integration lands).
func NewWarmPool(forker WarmForker) *WarmPool {
	return &WarmPool{
		forker:  forker,
		configs: make(map[string]WarmConfig),
		members: make(map[string]*warmVM),
	}
}

// SetQuota installs or updates the target ready count for ref. A target of 0
// drains the ref (no new forks; existing ready VMs stay claimable).
func (p *WarmPool) SetQuota(cfg WarmConfig) error {
	if cfg.Ref == "" {
		return fmt.Errorf("warm pool: ref required")
	}
	if cfg.TargetReady < 0 {
		return fmt.Errorf("warm pool: target ready must be non-negative")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.configs[cfg.Ref] = cfg
	return nil
}

// counts returns how many members of ref are in a pending (forking or warming)
// or ready state. Caller holds p.mu.
func (p *WarmPool) counts(ref string) (pending, ready int) {
	for _, m := range p.members {
		if m.ref != ref {
			continue
		}
		switch m.state {
		case WarmForking, WarmWarming:
			pending++
		case WarmReady:
			ready++
		}
	}
	return pending, ready
}

// Reconcile brings every ref up to its target by launching forks for the
// shortfall (target - ready - pending). Each new member starts in Forking; the
// fork call delegates to the WarmForker. It returns the number of forks started.
// A forker error fails that member's slot but does not abort the sweep; the next
// Reconcile retries the shortfall.
func (p *WarmPool) Reconcile(ctx context.Context) (started int, err error) {
	if p.forker == nil {
		return 0, nil
	}
	// Snapshot the shortfall under lock, then fork outside the lock so a slow
	// fork does not block Claim/MarkReady. Reserve pending slots up front so two
	// concurrent Reconciles do not double-fork.
	type slot struct {
		ref string
		id  string
	}
	var slots []slot
	p.mu.Lock()
	for ref, cfg := range p.configs {
		pending, ready := p.counts(ref)
		for n := ready + pending; n < cfg.TargetReady; n++ {
			p.seq++
			id := fmt.Sprintf("warm-%d", p.seq)
			p.members[id] = &warmVM{id: id, ref: ref, state: WarmForking}
			slots = append(slots, slot{ref: ref, id: id})
		}
	}
	p.mu.Unlock()

	var firstErr error
	for _, s := range slots {
		hostID, vmName, ferr := p.forker.Fork(ctx, s.ref)
		p.mu.Lock()
		m := p.members[s.id]
		if m == nil {
			p.mu.Unlock()
			continue
		}
		if ferr != nil {
			m.state = WarmFailed
			if firstErr == nil {
				firstErr = fmt.Errorf("warm pool fork %s: %w", s.ref, ferr)
			}
			p.mu.Unlock()
			continue
		}
		m.hostID = hostID
		m.vmName = vmName
		m.state = WarmWarming
		started++
		p.mu.Unlock()
	}
	return started, firstErr
}

// MarkReady advances a Warming member to Ready, reporting that its vsock agent
// answered. It errors if the id is unknown or not in Warming.
func (p *WarmPool) MarkReady(id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	m := p.members[id]
	if m == nil {
		return fmt.Errorf("warm pool: unknown member %q", id)
	}
	if m.state != WarmWarming {
		return fmt.Errorf("warm pool: member %q in state %q, want warming", id, m.state)
	}
	m.state = WarmReady
	return nil
}

// MarkFailed lands a member in Failed (fork crashed or agent never came up),
// freeing the slot for the next Reconcile. Unknown ids are a no-op.
func (p *WarmPool) MarkFailed(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if m := p.members[id]; m != nil {
		m.state = WarmFailed
	}
}

// ClaimedVM identifies a warm VM handed to a job.
type ClaimedVM struct {
	ID     string
	Ref    string
	HostID string
	VMName string
}

// Claim takes the oldest Ready VM of ref, marks it Claimed, and triggers an
// async replenish so the pool refills toward its target. It returns the claimed
// VM. When no VM is ready, it returns ok=false; the caller then cold-forks (and
// Reconcile will still backfill the pool).
//
// replenish runs Reconcile in a new goroutine bound to ctx so the hot claim path
// is not blocked by a fork. Callers wanting deterministic replenish (tests) can
// call Reconcile directly after Claim.
func (p *WarmPool) Claim(ctx context.Context, ref string) (ClaimedVM, bool) {
	p.mu.Lock()
	var oldest *warmVM
	for _, m := range p.members {
		if m.ref != ref || m.state != WarmReady {
			continue
		}
		if oldest == nil || m.id < oldest.id {
			oldest = m
		}
	}
	if oldest == nil {
		p.mu.Unlock()
		return ClaimedVM{}, false
	}
	oldest.state = WarmClaimed
	claimed := ClaimedVM{ID: oldest.id, Ref: oldest.ref, HostID: oldest.hostID, VMName: oldest.vmName}
	p.mu.Unlock()

	if p.forker != nil {
		go func() { _, _ = p.Reconcile(ctx) }()
	}
	return claimed, true
}

// Members returns a snapshot of all pool members sorted by id, for observability
// and tests.
func (p *WarmPool) Members() []WarmMember {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]WarmMember, 0, len(p.members))
	for _, m := range p.members {
		out = append(out, WarmMember{ID: m.id, Ref: m.ref, HostID: m.hostID, VMName: m.vmName, State: m.state})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// WarmMember is an exported snapshot of a pool member.
type WarmMember struct {
	ID     string    `json:"id"`
	Ref    string    `json:"ref"`
	HostID string    `json:"host_id,omitempty"`
	VMName string    `json:"vm_name,omitempty"`
	State  WarmState `json:"state"`
}

// ReadyCount reports how many members of ref are Ready (claimable now).
func (p *WarmPool) ReadyCount(ref string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ready := p.counts(ref)
	return ready
}
