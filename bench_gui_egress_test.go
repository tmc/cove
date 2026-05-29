package main

import (
	"testing"

	"github.com/tmc/cove/internal/guibench"
)

// TestEgressPolicyForDenyAll proves the runner wires a deny-egress cove policy
// by default: a task with no allowlist maps to the "offline" policy, whose
// AllowsDomain returns false for an arbitrary host (the gold-reference host the
// Berkeley RDI exploit would wget — design 047 §8). The applied policy must
// satisfy the task's egress lockdown.
func TestEgressPolicyForDenyAll(t *testing.T) {
	lock := guibench.TaskEgress(&guibench.Task{ID: "deny"})
	policy := egressPolicyFor(lock)

	if policy.Name != "offline" {
		t.Fatalf("deny-all policy name = %q, want offline", policy.Name)
	}
	if policy.Mode != NetworkModeNone {
		t.Fatalf("deny-all policy mode = %v, want NetworkModeNone (virtual network disabled)", policy.Mode)
	}
	if !policy.Enforced {
		t.Fatalf("deny-all policy Enforced = false, want true (egress actually blocked)")
	}
	// AllowsDomain must reject an arbitrary host — the gold-reference host stays
	// unreachable from the guest.
	for _, host := range []string{"huggingface.co", "raw.githubusercontent.com", "example.com"} {
		if policy.AllowsDomain(host) {
			t.Errorf("offline policy allowed %q, want denied", host)
		}
	}
	// The applied policy satisfies the lockdown's guard.
	if err := lock.CheckPolicy(policy, "huggingface.co"); err != nil {
		t.Fatalf("CheckPolicy(offline) = %v, want nil", err)
	}
}

// TestEgressPolicyForAllowlist proves the allowlist escape: a task that names a
// domain it genuinely needs gets a policy whose AllowsDomain admits that domain
// (suffix-matched) and still denies an arbitrary gold-reference host.
func TestEgressPolicyForAllowlist(t *testing.T) {
	lock := guibench.TaskEgress(&guibench.Task{
		ID:           "allow",
		NetworkAllow: []string{"wikipedia.org"},
	})
	policy := egressPolicyFor(lock)

	if !policy.AllowsDomain("wikipedia.org") {
		t.Errorf("allowlisted policy denied wikipedia.org, want allowed")
	}
	if !policy.AllowsDomain("en.wikipedia.org") {
		t.Errorf("allowlisted policy denied en.wikipedia.org (suffix), want allowed")
	}
	if policy.AllowsDomain("huggingface.co") {
		t.Errorf("allowlisted policy allowed arbitrary host huggingface.co, want denied")
	}
	// The applied policy satisfies the lockdown: allowlisted host admitted, gold
	// host denied.
	if err := lock.CheckPolicy(policy, "huggingface.co"); err != nil {
		t.Fatalf("CheckPolicy(allowlist) = %v, want nil", err)
	}
}

// TestEgressPolicyForMatchesParsedOffline confirms egressPolicyFor's deny-all
// path is the same policy ParseNetworkPolicy produces for "offline", so the
// runner's lockdown is the canonical cove offline policy, not a bespoke one.
func TestEgressPolicyForMatchesParsedOffline(t *testing.T) {
	want, err := ParseNetworkPolicy("offline")
	if err != nil {
		t.Fatalf("ParseNetworkPolicy(offline) = %v", err)
	}
	got := egressPolicyFor(guibench.TaskEgress(&guibench.Task{ID: "x"}))
	if got.Name != want.Name || got.Mode != want.Mode || got.Enforced != want.Enforced {
		t.Fatalf("egressPolicyFor deny-all = %+v, want %+v", got, want)
	}
}
