package controlserver

import (
	"testing"
	"time"
)

func TestUserProbeInvalidation(t *testing.T) {
	b := &AgentBridge{}
	b.recordUserProbe(0, true)
	if b.HealthSnapshot().LastUserPing.IsZero() {
		t.Fatal("successful probe not recorded")
	}
	b.MarkAgentReconnecting("test")
	b.recordUserProbe(0, true)
	h := b.HealthSnapshot()
	if h.UserStatus != "unknown" || !h.LastUserPing.IsZero() {
		t.Fatalf("stale probe restored readiness: %+v", h)
	}
	b.recordUserProbe(h.ConnectionGeneration, true)
	b.recordUserProbe(h.ConnectionGeneration, false)
	h = b.HealthSnapshot()
	if h.UserStatus != "disconnected" || h.LastUserPing != (time.Time{}) {
		t.Fatalf("failed probe retained readiness: %+v", h)
	}
}
