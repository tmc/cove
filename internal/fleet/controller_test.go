// cove fleet control plane (paid layer) — see docs/designs/046-fleet-control-plane.md
package fleet

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/tmc/cove/internal/fleet/fleetproto"
)

func newTestController(t *testing.T, token string, now func() time.Time) (*HostRegistry, *httptest.Server) {
	t.Helper()
	reg, err := NewHostRegistry(filepath.Join(t.TempDir(), "state.json"), token)
	if err != nil {
		t.Fatalf("NewHostRegistry: %v", err)
	}
	reg.Now = now
	srv := httptest.NewServer(NewController(reg).Handler())
	t.Cleanup(srv.Close)
	return reg, srv
}

func TestControllerRoundTrip(t *testing.T) {
	clock := time.Unix(1_000_000, 0)
	reg, srv := newTestController(t, "secret", func() time.Time { return clock })
	ctx := context.Background()
	client := srv.Client()

	// Register.
	regResp, err := fleetproto.Call[fleetproto.Register, fleetproto.RegisterResp](
		ctx, client, srv.URL, fleetproto.PathRegister, "secret",
		fleetproto.Register{HostID: "h1", Hostname: "mac1", Arch: "arm64", MacOSVersion: "15.0", Token: "secret"})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if !regResp.OK || regResp.LeaseID == "" {
		t.Fatalf("register resp = %+v, want OK lease", regResp)
	}
	lease := regResp.LeaseID

	// Queue an assignment to be delivered on the next heartbeat.
	id, err := reg.Enqueue("h1", fleetproto.Assignment{Kind: fleetproto.KindStopVM, Payload: json.RawMessage(`{"name":"v1"}`)})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if id == "" {
		t.Fatal("enqueue returned empty id")
	}

	// Heartbeat returns the queued assignment and records facts.
	hbResp, err := fleetproto.Call[fleetproto.Heartbeat, fleetproto.HeartbeatResp](
		ctx, client, srv.URL, fleetproto.PathHeartbeat, lease,
		fleetproto.Heartbeat{HostID: "h1", LeaseID: lease, FreeRAMBytes: 1 << 30, VMCount: 2, Images: []string{"base:latest"}, RunningVMs: []string{"v1"}})
	if err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if len(hbResp.Assignments) != 1 || hbResp.Assignments[0].ID != id {
		t.Fatalf("heartbeat assignments = %+v, want id %q", hbResp.Assignments, id)
	}

	hosts := reg.List()
	if len(hosts) != 1 || hosts[0].VMCount != 2 || hosts[0].FreeRAMBytes != 1<<30 {
		t.Fatalf("host facts not materialized: %+v", hosts)
	}
	if len(hosts[0].RunningVMs) != 1 || hosts[0].RunningVMs[0] != "v1" {
		t.Fatalf("running vms = %v, want [v1]", hosts[0].RunningVMs)
	}

	// Second heartbeat drains the queue.
	hbResp2, err := fleetproto.Call[fleetproto.Heartbeat, fleetproto.HeartbeatResp](
		ctx, client, srv.URL, fleetproto.PathHeartbeat, lease,
		fleetproto.Heartbeat{HostID: "h1", LeaseID: lease})
	if err != nil {
		t.Fatalf("heartbeat 2: %v", err)
	}
	if len(hbResp2.Assignments) != 0 {
		t.Fatalf("second heartbeat returned %d assignments, want 0", len(hbResp2.Assignments))
	}

	// ReportStatus.
	ack, err := fleetproto.Call[fleetproto.ReportStatus, fleetproto.StatusAck](
		ctx, client, srv.URL, fleetproto.PathStatus, lease,
		fleetproto.ReportStatus{HostID: "h1", LeaseID: lease, AssignmentID: id, State: fleetproto.StateDone})
	if err != nil {
		t.Fatalf("report status: %v", err)
	}
	if !ack.OK {
		t.Fatal("report status ack not OK")
	}
}

func TestControllerRejectsBadToken(t *testing.T) {
	_, srv := newTestController(t, "secret", nil)
	_, err := fleetproto.Call[fleetproto.Register, fleetproto.RegisterResp](
		context.Background(), srv.Client(), srv.URL, fleetproto.PathRegister, "wrong",
		fleetproto.Register{HostID: "h1", Token: "wrong"})
	if err == nil {
		t.Fatal("expected register with bad token to fail")
	}
}

func TestControllerRejectsBadLease(t *testing.T) {
	_, srv := newTestController(t, "", nil)
	ctx := context.Background()
	if _, err := fleetproto.Call[fleetproto.Register, fleetproto.RegisterResp](
		ctx, srv.Client(), srv.URL, fleetproto.PathRegister, "",
		fleetproto.Register{HostID: "h1"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	// Heartbeat with a forged lease must be rejected.
	_, err := fleetproto.Call[fleetproto.Heartbeat, fleetproto.HeartbeatResp](
		ctx, srv.Client(), srv.URL, fleetproto.PathHeartbeat, "forged",
		fleetproto.Heartbeat{HostID: "h1", LeaseID: "forged"})
	if err == nil {
		t.Fatal("expected heartbeat with forged lease to fail")
	}
}

func TestHostRegistryOnlineOffline(t *testing.T) {
	clock := time.Unix(2_000_000, 0)
	reg, err := NewHostRegistry("", "")
	if err != nil {
		t.Fatalf("NewHostRegistry: %v", err)
	}
	reg.Now = func() time.Time { return clock }
	reg.OnlineWindow = 60 * time.Second

	if _, err := reg.Register(fleetproto.Register{HostID: "h1"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if !reg.Online("h1") {
		t.Fatal("host should be online immediately after register")
	}

	tests := []struct {
		name    string
		advance time.Duration
		want    bool
	}{
		{"within window", 30 * time.Second, true},
		{"at boundary", 60 * time.Second, true},
		{"past window", 61 * time.Second, false},
	}
	base := clock
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clock = base.Add(tt.advance)
			if got := reg.Online("h1"); got != tt.want {
				t.Fatalf("Online after %v = %v, want %v", tt.advance, got, tt.want)
			}
		})
	}

	if reg.Online("unknown") {
		t.Fatal("unknown host should not be online")
	}
}

func TestControllerMethodGuards(t *testing.T) {
	_, srv := newTestController(t, "", nil)
	resp, err := http.Get(srv.URL + fleetproto.PathRegister)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET register status = %d, want 405", resp.StatusCode)
	}
}

func TestEnqueueUnknownHost(t *testing.T) {
	reg, err := NewHostRegistry("", "")
	if err != nil {
		t.Fatalf("NewHostRegistry: %v", err)
	}
	if _, err := reg.Enqueue("ghost", fleetproto.Assignment{Kind: fleetproto.KindStopVM}); err == nil {
		t.Fatal("expected enqueue to unknown host to fail")
	}
}

func TestHostRegistryPersistAndReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	reg, err := NewHostRegistry(path, "")
	if err != nil {
		t.Fatalf("NewHostRegistry: %v", err)
	}
	if _, err := reg.Register(fleetproto.Register{HostID: "h1", Hostname: "mac1"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	reloaded, err := NewHostRegistry(path, "")
	if err != nil {
		t.Fatalf("reload NewHostRegistry: %v", err)
	}
	hosts := reloaded.List()
	if len(hosts) != 1 || hosts[0].Hostname != "mac1" {
		t.Fatalf("reloaded hosts = %+v, want h1/mac1", hosts)
	}
}
