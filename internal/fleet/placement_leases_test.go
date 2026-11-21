package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPlacementLeaseCounts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fleet.json")
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	if err := RecordPlacementLease(path, "a", now, time.Minute); err != nil {
		t.Fatalf("RecordPlacementLease a: %v", err)
	}
	if err := RecordPlacementLease(path, "a", now.Add(10*time.Second), time.Minute); err != nil {
		t.Fatalf("RecordPlacementLease a second: %v", err)
	}
	if err := RecordPlacementLease(path, "expired", now.Add(-2*time.Minute), time.Minute); err != nil {
		t.Fatalf("RecordPlacementLease expired: %v", err)
	}
	counts, err := ActivePlacementLeaseCounts(path, now)
	if err != nil {
		t.Fatalf("ActivePlacementLeaseCounts: %v", err)
	}
	if counts["a"] != 2 {
		t.Fatalf("counts[a] = %d, want 2", counts["a"])
	}
	if counts["expired"] != 0 {
		t.Fatalf("counts[expired] = %d, want 0", counts["expired"])
	}
}

func TestPlacementLeaseCountsRejectsMalformedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fleet.json")
	if err := os.WriteFile(placementLeasePath(path), []byte("{"), 0600); err != nil {
		t.Fatalf("write lease file: %v", err)
	}
	_, err := ActivePlacementLeaseCounts(path, time.Now())
	if err == nil || !strings.Contains(err.Error(), "parse placement leases") {
		t.Fatalf("ActivePlacementLeaseCounts err = %v, want parse placement leases", err)
	}
}
