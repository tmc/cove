package coved

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tmc/cove/internal/fleet/fleetproto"
	"github.com/tmc/cove/internal/vmpolicy"
)

// applyPolicy handles a KindPolicy assignment: it decodes the pushed thresholds,
// writes them to every local VM's policy file (reusing internal/vmpolicy), then
// runs one lifecycle-enforcement pass (reusing LifecycleEnforcer) so a VM that
// the new policy already obsoletes is stopped immediately. It reports the count
// of VMs the policy was applied to, the count stopped, and any per-VM failures
// as a fleetproto.PolicyResult JSON detail.
//
// Worker mode never reimplements enforcement: the stop decision and request live
// in LifecycleEnforcer; this function only stages the policy and triggers a pass.
func (h *BoundedHandler) applyPolicy(ctx context.Context, payload []byte) (detail string, err error) {
	var pp fleetproto.PolicyPayload
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &pp); err != nil {
			return "", fmt.Errorf("decode policy payload: %w", err)
		}
	}
	policy, err := policyFromPayload(pp)
	if err != nil {
		return "", err
	}

	vmRoot := h.vmRoot()
	entries, err := os.ReadDir(vmRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return marshalDetail(fleetproto.PolicyResult{})
		}
		return "", fmt.Errorf("read vm root: %w", err)
	}

	var result fleetproto.PolicyResult
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		vmDir := filepath.Join(vmRoot, e.Name())
		if err := vmpolicy.Save(vmDir, policy); err != nil {
			result.Failed++
			continue
		}
		result.Applied++
	}

	// Run a single enforcement pass so newly tightened thresholds take effect
	// without waiting for the daemon's enforcement ticker.
	before := h.enforcer().Stats().Enforced
	h.enforcer().EnforceOnce(ctx)
	after := h.enforcer().Stats().Enforced
	if after > before {
		result.Stopped = int(after - before)
	}
	return marshalDetail(result)
}

// runImageGC handles a KindImageGC assignment by invoking the existing
// ImageGCScheduler one-shot path (RunOnce) and reporting its stats. It never
// reimplements GC: the scan/reference/delete logic lives in ImageGCScheduler.
func (h *BoundedHandler) runImageGC(ctx context.Context) (detail string, err error) {
	stats, err := h.imageGC().RunOnce(ctx)
	if err != nil {
		return "", fmt.Errorf("image gc: %w", err)
	}
	return marshalDetail(fleetproto.ImageGCResult{
		ManifestsScanned: stats.ManifestsScanned,
		ManifestsRemoved: stats.ManifestsRemoved,
		BytesFreed:       stats.BytesFreed,
		Skipped:          stats.Skipped,
	})
}

// vmRoot returns the configured VM root or the default ~/.vz/vms.
func (h *BoundedHandler) vmRoot() string {
	if h.VMRoot != "" {
		return h.VMRoot
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".vz", "vms")
}

// enforcer returns the injected LifecycleEnforcer or builds a default one over
// the worker's VM root. The default is cheap to construct and stateless beyond
// counters, so building it per-assignment is fine.
func (h *BoundedHandler) enforcer() *LifecycleEnforcer {
	if h.Lifecycle != nil {
		return h.Lifecycle
	}
	return NewLifecycleEnforcer(LifecycleConfig{VMRoot: h.vmRoot(), Log: h.Logger})
}

// imageGC returns the injected ImageGCScheduler or builds a default one over the
// worker's home directory.
func (h *BoundedHandler) imageGC() *ImageGCScheduler {
	if h.ImageGC != nil {
		return h.ImageGC
	}
	return NewImageGCScheduler(h.HomeDir, h.Logger)
}

// policyFromPayload converts the wire payload into a validated vmpolicy.Policy,
// parsing the Go duration strings the controller encodes.
func policyFromPayload(pp fleetproto.PolicyPayload) (vmpolicy.Policy, error) {
	p := vmpolicy.Policy{RunBudget: pp.RunBudget}
	if pp.IdleTimeout != "" {
		d, err := time.ParseDuration(pp.IdleTimeout)
		if err != nil {
			return vmpolicy.Policy{}, fmt.Errorf("parse idle timeout: %w", err)
		}
		p.IdleTimeout = d
	}
	if pp.MaxAge != "" {
		d, err := time.ParseDuration(pp.MaxAge)
		if err != nil {
			return vmpolicy.Policy{}, fmt.Errorf("parse max age: %w", err)
		}
		p.MaxAge = d
	}
	if err := p.Validate(); err != nil {
		return vmpolicy.Policy{}, err
	}
	return p, nil
}

func marshalDetail(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("encode result: %w", err)
	}
	return string(data), nil
}
