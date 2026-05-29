// cove fleet control plane (paid layer) — see docs/designs/046-fleet-control-plane.md
package fleet

import "fmt"

// Cordon marks a host unschedulable so the scheduler skips it in feasibility,
// without evicting its running VMs. It is idempotent. Cordoning an unregistered
// host errors so an operator typo is caught. Uncordon reverses it.
//
// *HostRegistry satisfies the hosted API's Cordoner interface through this
// method; the scheduler's feasibility filter honors the cordoned set.
func (r *HostRegistry) Cordon(hostID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.hosts[hostID]; !ok {
		return fmt.Errorf("cordon: host %q not registered", hostID)
	}
	if r.cordoned == nil {
		r.cordoned = make(map[string]struct{})
	}
	r.cordoned[hostID] = struct{}{}
	return nil
}

// Uncordon clears a host's cordon, making it schedulable again. It is
// idempotent and a no-op for an uncordoned or unknown host.
func (r *HostRegistry) Uncordon(hostID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.cordoned, hostID)
}

// isCordoned reports whether a host is cordoned. Caller holds r.mu.
func (r *HostRegistry) isCordoned(hostID string) bool {
	_, ok := r.cordoned[hostID]
	return ok
}

var _ Cordoner = (*HostRegistry)(nil)
