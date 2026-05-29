package guibench

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"
)

// EgressPolicy is the network-egress decision surface the scoring runner needs:
// it answers, host-side, whether the guest may reach a given domain or IP.
// *main.NetworkPolicy (network_policy.go) satisfies it, so the runner applies a
// real cove network policy without guibench importing package main. Keeping the
// interface to these two methods makes the lockdown wiring unit-testable against
// either the production policy or a stub (design 047 §8).
type EgressPolicy interface {
	// AllowsDomain reports whether egress to domain is permitted.
	AllowsDomain(domain string) bool
	// AllowsIP reports whether egress to addr is permitted.
	AllowsIP(addr netip.Addr) bool
}

// EgressLockdown is the egress policy a task runs under during scoring.
//
// The benchmark denies all guest egress by default so a task agent cannot
// fetch the gold reference and replay it into the path the verifier checks —
// the Berkeley RDI contamination exploit that reached a 73% bogus score on
// OSWorld (design 047 §8). A task that genuinely needs the network names an
// Allow list; only those exact domains pass. Gold references never live in the
// task config: they stay host-side in the verifier, unreachable from the guest.
type EgressLockdown struct {
	// PolicyName is the cove network-policy name the runner should request.
	// "offline" disables the virtual network entirely (the deny-all default);
	// an allowlisted task requests a named policy whose audit records Allow.
	PolicyName string `json:"policy_name"`
	// Allow is the explicit per-task domain allowlist. Empty means deny-all.
	// Entries are lowercased and de-duplicated by [TaskEgress].
	Allow []string `json:"allow,omitempty"`
}

// deniedPolicyName is the cove network-policy name that disables the guest's
// virtual network outright. It maps to ParseNetworkPolicy("offline"), the only
// policy that records enforcement=virtual-network-disabled rather than a NAT
// best-effort allowlist (network_policy.go).
const deniedPolicyName = "offline"

// TaskEgress derives the egress lockdown for a task. A task with no network
// allowlist (the common case) runs fully offline; a task that names domains it
// needs runs under a named policy carrying exactly that allowlist. The returned
// value never embeds a gold reference, only the host names a task must reach.
func TaskEgress(t *Task) EgressLockdown {
	allow := normalizeAllow(t.NetworkAllow)
	if len(allow) == 0 {
		return EgressLockdown{PolicyName: deniedPolicyName}
	}
	return EgressLockdown{PolicyName: "task-allow", Allow: allow}
}

// DenyAll reports whether the lockdown permits no egress at all.
func (e EgressLockdown) DenyAll() bool {
	return len(e.Allow) == 0
}

// Permits reports whether the lockdown's own allowlist admits domain. This is
// the host-side decision the runner makes before granting any network: a
// deny-all lockdown permits nothing; an allowlisted lockdown permits only its
// listed domains (suffix-matched, so "wikipedia.org" admits "en.wikipedia.org").
// It mirrors [EgressPolicy.AllowsDomain] so a test can assert the wiring without
// constructing a live policy.
func (e EgressLockdown) Permits(domain string) bool {
	domain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
	if domain == "" {
		return false
	}
	for _, allowed := range e.Allow {
		if domain == allowed || strings.HasSuffix(domain, "."+allowed) {
			return true
		}
	}
	return false
}

// CheckPolicy verifies that an applied [EgressPolicy] enforces this lockdown:
// every domain the lockdown denies must be denied by the policy, and every
// domain it allows must be allowed. It is the runner's guard that the policy it
// asked for actually got applied before the agent runs. probe is the set of
// arbitrary hosts to confirm are blocked (e.g. the gold-reference host); it
// returns an error naming the first divergence, nil when the policy matches.
func (e EgressLockdown) CheckPolicy(p EgressPolicy, probe ...string) error {
	if p == nil {
		return fmt.Errorf("egress lockdown: nil policy")
	}
	for _, host := range probe {
		host = strings.ToLower(strings.TrimSpace(host))
		if host == "" {
			continue
		}
		want := e.Permits(host)
		if got := p.AllowsDomain(host); got != want {
			return fmt.Errorf("egress lockdown: policy allows %q = %v, want %v", host, got, want)
		}
	}
	for _, allowed := range e.Allow {
		if !p.AllowsDomain(allowed) {
			return fmt.Errorf("egress lockdown: policy denies allowlisted domain %q", allowed)
		}
	}
	return nil
}

// normalizeAllow lowercases, trims, drops empties, and sorts/uniques the
// allowlist so the lockdown is deterministic regardless of authoring order.
func normalizeAllow(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, d := range in {
		d = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(d), "."))
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}
