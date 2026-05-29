// cove fleet control plane (paid layer) — see docs/designs/046-fleet-control-plane.md
package fleet

import (
	"encoding/json"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tmc/cove/internal/fleet/fleetproto"
)

const gib = int64(1) << 30

// newSchedRegistry builds a registry with a frozen clock and registered hosts
// whose heartbeat facts are set directly for deterministic scheduling tests.
func newSchedRegistry(t *testing.T, hosts ...*Host) *HostRegistry {
	t.Helper()
	reg, err := NewHostRegistry(filepath.Join(t.TempDir(), "state.json"), "")
	if err != nil {
		t.Fatalf("NewHostRegistry: %v", err)
	}
	now := time.Unix(1_000_000, 0)
	reg.Now = func() time.Time { return now }
	for _, h := range hosts {
		if h.LastSeenUnix == 0 {
			h.LastSeenUnix = now.Unix()
		}
		reg.hosts[h.HostID] = h
	}
	return reg
}

func TestScheduleFeasibilityFilter(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	tests := []struct {
		name    string
		host    *Host
		req     ScheduleRequest
		wantErr bool
	}{
		{
			name: "fits",
			host: &Host{HostID: "h", Arch: "arm64", MacOSVersion: "15.0", FreeRAMBytes: 8 * gib, LastSeenUnix: now.Unix()},
			req:  ScheduleRequest{RequestedRAM: 4 * gib, Arch: "arm64", MinMacOS: "14.0"},
		},
		{
			name:    "offline rejected",
			host:    &Host{HostID: "h", Arch: "arm64", FreeRAMBytes: 8 * gib, LastSeenUnix: now.Add(-10 * time.Minute).Unix()},
			req:     ScheduleRequest{RequestedRAM: 4 * gib},
			wantErr: true,
		},
		{
			name:    "too small rejected",
			host:    &Host{HostID: "h", Arch: "arm64", FreeRAMBytes: 2 * gib, LastSeenUnix: now.Unix()},
			req:     ScheduleRequest{RequestedRAM: 4 * gib},
			wantErr: true,
		},
		{
			name:    "wrong arch rejected",
			host:    &Host{HostID: "h", Arch: "x86_64", FreeRAMBytes: 8 * gib, LastSeenUnix: now.Unix()},
			req:     ScheduleRequest{RequestedRAM: 4 * gib, Arch: "arm64"},
			wantErr: true,
		},
		{
			name:    "macos floor rejected",
			host:    &Host{HostID: "h", Arch: "arm64", MacOSVersion: "13.6", FreeRAMBytes: 8 * gib, LastSeenUnix: now.Unix()},
			req:     ScheduleRequest{RequestedRAM: 4 * gib, MinMacOS: "14.0"},
			wantErr: true,
		},
		{
			name: "macos floor satisfied higher",
			host: &Host{HostID: "h", Arch: "arm64", MacOSVersion: "15.2", FreeRAMBytes: 8 * gib, LastSeenUnix: now.Unix()},
			req:  ScheduleRequest{RequestedRAM: 4 * gib, MinMacOS: "14.5"},
		},
		{
			name:    "exact fit boundary fails when over by one",
			host:    &Host{HostID: "h", Arch: "arm64", FreeRAMBytes: 4*gib - 1, LastSeenUnix: now.Unix()},
			req:     ScheduleRequest{RequestedRAM: 4 * gib},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := newSchedRegistry(t, tt.host)
			_, token, err := reg.Schedule(tt.req)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Schedule err = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && token == "" {
				t.Fatal("expected non-empty reservation token on success")
			}
		})
	}
}

func TestScheduleRankImageLocalThenDensest(t *testing.T) {
	reg := newSchedRegistry(t,
		// Big free RAM, no image: would win bin-pack only if no image-local host.
		&Host{HostID: "spacious", Arch: "arm64", FreeRAMBytes: 64 * gib},
		// Has the image: should win on affinity despite more free RAM than dense.
		&Host{HostID: "image-local", Arch: "arm64", FreeRAMBytes: 32 * gib, Images: []string{"base:14.5"}},
		// Densest fit but no image.
		&Host{HostID: "dense", Arch: "arm64", FreeRAMBytes: 5 * gib},
	)
	pl, _, err := reg.Schedule(ScheduleRequest{RequestedRAM: 4 * gib, BaseRef: "base:14.5"})
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if pl.Chosen != "image-local" {
		t.Fatalf("chosen = %q, want image-local (affinity must dominate)", pl.Chosen)
	}
	if pl.NeedsImageSync {
		t.Fatal("NeedsImageSync should be false when a feasible host has the image")
	}
}

func TestScheduleRankDensestWhenNoImage(t *testing.T) {
	reg := newSchedRegistry(t,
		&Host{HostID: "spacious", Arch: "arm64", FreeRAMBytes: 64 * gib},
		&Host{HostID: "dense", Arch: "arm64", FreeRAMBytes: 5 * gib},
		&Host{HostID: "medium", Arch: "arm64", FreeRAMBytes: 16 * gib},
	)
	pl, _, err := reg.Schedule(ScheduleRequest{RequestedRAM: 4 * gib, BaseRef: "base:14.5"})
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if pl.Chosen != "dense" {
		t.Fatalf("chosen = %q, want dense (bin-pack prefers densest fit)", pl.Chosen)
	}
	if !pl.NeedsImageSync {
		t.Fatal("NeedsImageSync should be true when no feasible host has the image")
	}
}

func TestScheduleAntiAffinitySpreadsReplicas(t *testing.T) {
	// hostA is densest and already runs a replica of job j1; hostB is roomier but
	// replica-free. Anti-affinity must push the next replica to hostB.
	reg := newSchedRegistry(t,
		&Host{HostID: "hostA", Arch: "arm64", FreeRAMBytes: 8 * gib, RunningVMs: []string{"r1@job:j1"}},
		&Host{HostID: "hostB", Arch: "arm64", FreeRAMBytes: 8 * gib},
	)
	pl, _, err := reg.Schedule(ScheduleRequest{RequestedRAM: 4 * gib, BaseRef: "base:14.5", JobID: "j1"})
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if pl.Chosen != "hostB" {
		t.Fatalf("chosen = %q, want hostB (anti-affinity must spread replicas)", pl.Chosen)
	}
}

func TestScheduleTopKRetained(t *testing.T) {
	var hosts []*Host
	for i := 0; i < 10; i++ {
		hosts = append(hosts, &Host{HostID: string(rune('a' + i)), Arch: "arm64", FreeRAMBytes: int64(8+i) * gib})
	}
	reg := newSchedRegistry(t, hosts...)
	pl, _, err := reg.Schedule(ScheduleRequest{RequestedRAM: 4 * gib, BaseRef: "base"})
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if len(pl.Candidates) != MaxRetainedScores {
		t.Fatalf("retained %d candidates, want %d", len(pl.Candidates), MaxRetainedScores)
	}
	// Candidates must be sorted best-first.
	for i := 1; i < len(pl.Candidates); i++ {
		if pl.Candidates[i-1].Score < pl.Candidates[i].Score {
			t.Fatalf("candidates not sorted best-first at %d", i)
		}
	}
}

func TestScheduleReservationPreventsImmediateOversubscribe(t *testing.T) {
	// One host with room for exactly two 4GiB forks. Two sequential Schedule
	// calls without releasing the first reservation must exhaust capacity.
	reg := newSchedRegistry(t, &Host{HostID: "h", Arch: "arm64", FreeRAMBytes: 9 * gib})
	if _, _, err := reg.Schedule(ScheduleRequest{RequestedRAM: 4 * gib}); err != nil {
		t.Fatalf("first schedule: %v", err)
	}
	if _, _, err := reg.Schedule(ScheduleRequest{RequestedRAM: 4 * gib}); err != nil {
		t.Fatalf("second schedule: %v", err)
	}
	// Third should fail: 9 - 4 - 4 = 1 GiB < 4 GiB.
	if _, _, err := reg.Schedule(ScheduleRequest{RequestedRAM: 4 * gib}); err == nil {
		t.Fatal("third schedule should fail: reservations exhausted capacity")
	}
	if got := reg.ReservedRAM("h"); got != 8*gib {
		t.Fatalf("reserved = %d, want %d", got, 8*gib)
	}
}

func TestReleaseReservationFreesCapacity(t *testing.T) {
	reg := newSchedRegistry(t, &Host{HostID: "h", Arch: "arm64", FreeRAMBytes: 5 * gib})
	_, token, err := reg.Schedule(ScheduleRequest{RequestedRAM: 4 * gib})
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}
	if _, _, err := reg.Schedule(ScheduleRequest{RequestedRAM: 4 * gib}); err == nil {
		t.Fatal("second schedule should fail before release")
	}
	reg.ReleaseReservation(token)
	if got := reg.ReservedRAM("h"); got != 0 {
		t.Fatalf("reserved after release = %d, want 0", got)
	}
	if _, _, err := reg.Schedule(ScheduleRequest{RequestedRAM: 4 * gib}); err != nil {
		t.Fatalf("schedule after release should succeed: %v", err)
	}
	// Release is idempotent.
	reg.ReleaseReservation("res-does-not-exist")
}

func TestScheduleConcurrentNeverOversubscribes(t *testing.T) {
	const capacityGiB = 32
	const perReqGiB = 4
	reg := newSchedRegistry(t, &Host{HostID: "h", Arch: "arm64", FreeRAMBytes: int64(capacityGiB) * gib})

	const goroutines = 50
	var wg sync.WaitGroup
	var success atomic.Int64
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			if _, _, err := reg.Schedule(ScheduleRequest{RequestedRAM: int64(perReqGiB) * gib}); err == nil {
				success.Add(1)
			}
		}()
	}
	wg.Wait()

	// Capacity is capacity/perReq successful placements, never more.
	wantMax := int64(capacityGiB / perReqGiB)
	if success.Load() != wantMax {
		t.Fatalf("successful placements = %d, want exactly %d", success.Load(), wantMax)
	}
	if got := reg.ReservedRAM("h"); got > int64(capacityGiB)*gib {
		t.Fatalf("reserved %d exceeds capacity %d (oversubscribed)", got, int64(capacityGiB)*gib)
	}
}

func TestScheduleRejectsNonPositiveRAM(t *testing.T) {
	reg := newSchedRegistry(t, &Host{HostID: "h", Arch: "arm64", FreeRAMBytes: 8 * gib})
	if _, _, err := reg.Schedule(ScheduleRequest{RequestedRAM: 0}); err == nil {
		t.Fatal("expected error for non-positive ram")
	}
}

func TestScheduleForkRunEnqueuesAssignments(t *testing.T) {
	reg := newSchedRegistry(t,
		&Host{HostID: "image-local", Arch: "arm64", FreeRAMBytes: 16 * gib, Images: []string{"base:14.5"}},
	)
	out, err := reg.ScheduleForkRun(ScheduleRequest{RequestedRAM: 4 * gib, BaseRef: "base:14.5"}, "fork-1")
	if err != nil {
		t.Fatalf("ScheduleForkRun: %v", err)
	}
	if out.Chosen != "image-local" {
		t.Fatalf("chosen = %q, want image-local", out.Chosen)
	}
	if out.ForkAssignmentID == "" {
		t.Fatal("expected fork assignment id")
	}
	if out.SyncAssignmentID != "" {
		t.Fatal("no image-sync expected when host has image")
	}
	// The fork-run assignment must be queued and carry the encoded payload.
	assigned, err := reg.Assignments("image-local", reg.hosts["image-local"].LeaseID)
	if err != nil {
		t.Fatalf("Assignments: %v", err)
	}
	if len(assigned) != 1 || assigned[0].Kind != fleetproto.KindForkRun {
		t.Fatalf("queued = %+v, want one fork-run", assigned)
	}
	var p fleetproto.ForkRunPayload
	if err := json.Unmarshal(assigned[0].Payload, &p); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if p.BaseRef != "base:14.5" || p.Name != "fork-1" || p.RAMBytes != 4*gib {
		t.Fatalf("payload = %+v, unexpected", p)
	}
}

func TestScheduleForkRunPreflightImageSync(t *testing.T) {
	reg := newSchedRegistry(t,
		&Host{HostID: "h", Arch: "arm64", FreeRAMBytes: 16 * gib}, // no image
	)
	out, err := reg.ScheduleForkRun(ScheduleRequest{RequestedRAM: 4 * gib, BaseRef: "base:14.5"}, "fork-1")
	if err != nil {
		t.Fatalf("ScheduleForkRun: %v", err)
	}
	if !out.NeedsImageSync || out.SyncAssignmentID == "" {
		t.Fatalf("expected pre-flight image sync, got %+v", out)
	}
	assigned, err := reg.Assignments("h", reg.hosts["h"].LeaseID)
	if err != nil {
		t.Fatalf("Assignments: %v", err)
	}
	// image-sync must precede fork-run.
	if len(assigned) != 2 {
		t.Fatalf("queued %d assignments, want 2", len(assigned))
	}
	if assigned[0].Kind != fleetproto.KindImageSync || assigned[1].Kind != fleetproto.KindForkRun {
		t.Fatalf("order = [%s, %s], want [image-sync, fork-run]", assigned[0].Kind, assigned[1].Kind)
	}
}

func TestScheduleForkRunNoFeasibleHostReleasesNothing(t *testing.T) {
	reg := newSchedRegistry(t, &Host{HostID: "h", Arch: "arm64", FreeRAMBytes: gib})
	if _, err := reg.ScheduleForkRun(ScheduleRequest{RequestedRAM: 4 * gib, BaseRef: "base"}, "fork"); err == nil {
		t.Fatal("expected error when no feasible host")
	}
	if got := reg.ReservedRAM("h"); got != 0 {
		t.Fatalf("reserved = %d, want 0 (no reservation on failed schedule)", got)
	}
}

func TestVersionAtLeast(t *testing.T) {
	tests := []struct {
		got, want string
		ok        bool
	}{
		{"15.0", "14.0", true},
		{"14.0", "14.0", true},
		{"14.0", "15.0", false},
		{"14.5", "14.0", true},
		{"14", "14.5", false},
		{"15", "14.5", true},
		{"14.5.1", "14.5", true},
		{"14.5", "14.5.1", false},
		{"", "14.0", false},
		{"14.0", "", true},
	}
	for _, tt := range tests {
		if got := versionAtLeast(tt.got, tt.want); got != tt.ok {
			t.Errorf("versionAtLeast(%q, %q) = %v, want %v", tt.got, tt.want, got, tt.ok)
		}
	}
}
