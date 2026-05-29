package guibench

import (
	"net/netip"
	"testing"
)

// stubPolicy is an in-memory [EgressPolicy] for asserting the runner's wiring
// without constructing a live cove network policy. A nil allow set denies every
// domain (the deny-all default); listed domains are allowed exactly.
type stubPolicy struct {
	allow map[string]bool
}

func (s stubPolicy) AllowsDomain(domain string) bool { return s.allow[domain] }
func (s stubPolicy) AllowsIP(addr netip.Addr) bool   { return false }

func TestTaskEgressDenyAllByDefault(t *testing.T) {
	got := TaskEgress(&Task{ID: "t1"})
	if !got.DenyAll() {
		t.Fatalf("TaskEgress(no allowlist).DenyAll() = false, want true")
	}
	if got.PolicyName != deniedPolicyName {
		t.Fatalf("policy name = %q, want %q", got.PolicyName, deniedPolicyName)
	}
	if got.Permits("huggingface.co") {
		t.Fatalf("deny-all lockdown permitted an arbitrary host")
	}
}

func TestTaskEgressAllowlist(t *testing.T) {
	task := &Task{ID: "t2", NetworkAllow: []string{"EN.Wikipedia.org", "wikipedia.org", ""}}
	got := TaskEgress(task)
	if got.DenyAll() {
		t.Fatalf("allowlisted task reported DenyAll")
	}
	// Normalized: lowercased, empty dropped, deduped.
	if len(got.Allow) != 2 {
		t.Fatalf("allow = %v, want 2 normalized entries", got.Allow)
	}
	tests := []struct {
		domain string
		want   bool
	}{
		{"wikipedia.org", true},
		{"en.wikipedia.org", true}, // suffix match through the listed apex
		{"EN.WIKIPEDIA.ORG", true}, // case-insensitive
		{"huggingface.co", false},  // arbitrary host denied
		{"notwikipedia.org", false},
		{"", false},
	}
	for _, tt := range tests {
		if got.Permits(tt.domain) != tt.want {
			t.Errorf("Permits(%q) = %v, want %v", tt.domain, got.Permits(tt.domain), tt.want)
		}
	}
}

// TestEgressLockdownCheckPolicyDenyAll proves the runner's guard: under a
// deny-all lockdown the applied policy must deny an arbitrary gold-reference
// host, and CheckPolicy passes only when it does.
func TestEgressLockdownCheckPolicyDenyAll(t *testing.T) {
	lock := TaskEgress(&Task{ID: "t3"}) // deny-all
	deny := stubPolicy{}                // allows nothing — matches deny-all
	if err := lock.CheckPolicy(deny, "huggingface.co", "raw.githubusercontent.com"); err != nil {
		t.Fatalf("CheckPolicy(deny-all policy) = %v, want nil", err)
	}
	// A policy that leaks egress to the gold host must be rejected.
	leaky := stubPolicy{allow: map[string]bool{"huggingface.co": true}}
	if err := lock.CheckPolicy(leaky, "huggingface.co"); err == nil {
		t.Fatalf("CheckPolicy(leaky policy) = nil, want error")
	}
}

// TestEgressLockdownCheckPolicyAllowlist proves the allowlist escape: the
// applied policy must admit the named host and still deny everything else.
func TestEgressLockdownCheckPolicyAllowlist(t *testing.T) {
	lock := TaskEgress(&Task{ID: "t4", NetworkAllow: []string{"wikipedia.org"}})
	good := stubPolicy{allow: map[string]bool{"wikipedia.org": true}}
	if err := lock.CheckPolicy(good, "huggingface.co"); err != nil {
		t.Fatalf("CheckPolicy(allowlist honored, gold denied) = %v, want nil", err)
	}
	// A policy that fails to admit the allowlisted host is wrong wiring.
	missing := stubPolicy{}
	if err := lock.CheckPolicy(missing); err == nil {
		t.Fatalf("CheckPolicy(policy denies allowlisted host) = nil, want error")
	}
	// A policy that admits the allowlisted host but also leaks the gold host.
	leaky := stubPolicy{allow: map[string]bool{"wikipedia.org": true, "huggingface.co": true}}
	if err := lock.CheckPolicy(leaky, "huggingface.co"); err == nil {
		t.Fatalf("CheckPolicy(leaks gold host) = nil, want error")
	}
}

func TestEgressLockdownCheckPolicyNil(t *testing.T) {
	if err := (EgressLockdown{}).CheckPolicy(nil); err == nil {
		t.Fatal("CheckPolicy(nil) = nil, want error")
	}
}
