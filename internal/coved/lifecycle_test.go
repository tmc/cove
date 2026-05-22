package coved

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	controlx "github.com/tmc/cove/internal/control"
	"github.com/tmc/cove/internal/vmpolicy"
	controlpb "github.com/tmc/cove/proto/controlpb"
)

func TestLifecycleEnforcerThresholds(t *testing.T) {
	now := time.Unix(4000, 0).UTC()
	tests := []struct {
		name   string
		policy vmpolicy.Policy
		status vmStatus
		want   bool
	}{
		{
			name:   "idle",
			policy: vmpolicy.Policy{IdleTimeout: 30 * time.Minute},
			status: vmStatus{State: "running", LastPing: now.Add(-31 * time.Minute), PolicyStartedAt: now.Add(-time.Hour)},
			want:   true,
		},
		{
			name:   "max age",
			policy: vmpolicy.Policy{MaxAge: time.Hour},
			status: vmStatus{State: "running", LastPing: now, PolicyStartedAt: now.Add(-2 * time.Hour)},
			want:   true,
		},
		{
			name:   "run budget",
			policy: vmpolicy.Policy{RunBudget: 2},
			status: vmStatus{State: "running", LastPing: now, PolicyStartedAt: now.Add(-time.Hour), PolicyExecCount: 2},
			want:   true,
		},
		{
			name:   "zero policy disabled",
			policy: vmpolicy.Policy{},
			status: vmStatus{State: "running", LastPing: now.Add(-24 * time.Hour), PolicyStartedAt: now.Add(-24 * time.Hour), PolicyExecCount: 100},
			want:   false,
		},
		{
			name:   "below threshold",
			policy: vmpolicy.Policy{IdleTimeout: time.Hour, MaxAge: 24 * time.Hour, RunBudget: 10},
			status: vmStatus{State: "running", LastPing: now.Add(-time.Minute), PolicyStartedAt: now.Add(-time.Hour), PolicyExecCount: 3},
			want:   false,
		},
		{
			name:   "already stopped",
			policy: vmpolicy.Policy{RunBudget: 1},
			status: vmStatus{State: "running", PolicyExecCount: 2, PolicyStopIssued: true},
			want:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := shortTempDir(t)
			vmDir := filepath.Join(root, "vm")
			if err := os.Mkdir(vmDir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := vmpolicy.Save(vmDir, tt.policy); err != nil {
				t.Fatalf("Save(): %v", err)
			}

			stopCount := serveLifecycleControl(t, vmDir, tt.status)
			bus := NewEventBus(4)
			enforcer := NewLifecycleEnforcer(LifecycleConfig{
				VMRoot: root,
				Now:    func() time.Time { return now },
				Bus:    bus,
			})
			enforcer.EnforceOnce(context.Background())
			if got := *stopCount > 0; got != tt.want {
				t.Fatalf("stopped = %v, want %v", got, tt.want)
			}
			if got := enforcer.Stats().Enforced; got != boolUint64(tt.want) {
				t.Fatalf("enforced = %d, want %d", got, boolUint64(tt.want))
			}
			if tt.want {
				tail := bus.Tail()
				if len(tail) != 1 || tail[0].EventType != "lifecycle.policy.stop" || tail[0].VMName != "vm" {
					t.Fatalf("tail = %+v", tail)
				}
			}
		})
	}
}

func TestLifecycleEnforcerRunLoopStopsByPolicy(t *testing.T) {
	now := time.Unix(4000, 0).UTC()
	tests := []struct {
		name   string
		policy vmpolicy.Policy
		status vmStatus
		reason string
	}{
		{"idle", vmpolicy.Policy{IdleTimeout: time.Minute}, vmStatus{State: "running", LastPing: now.Add(-2 * time.Minute)}, "idle"},
		{"max age", vmpolicy.Policy{MaxAge: time.Minute}, vmStatus{State: "running", PolicyStartedAt: now.Add(-2 * time.Minute)}, "max_age"},
		{"run budget", vmpolicy.Policy{RunBudget: 2}, vmStatus{State: "running", PolicyExecCount: 2}, "run_budget"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := shortTempDirLink(t)
			vmDir := filepath.Join(root, "vm")
			if err := os.Mkdir(vmDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := vmpolicy.Save(vmDir, tt.policy); err != nil {
				t.Fatal(err)
			}
			stopCount := serveLifecycleControl(t, vmDir, tt.status)
			bus := NewEventBus(8)
			sub, cancelSub := bus.Subscribe(4)
			defer cancelSub()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan struct{})
			go func() {
				NewLifecycleEnforcer(LifecycleConfig{
					VMRoot:   root,
					Interval: time.Hour,
					Now:      func() time.Time { return now },
					Bus:      bus,
				}).Run(ctx)
				close(done)
			}()

			select {
			case ev := <-sub:
				cancel()
				<-done
				if ev.EventType != "lifecycle.policy.stop" || ev.Extra["reason"] != tt.reason {
					t.Fatalf("event = %+v, want reason %q", ev, tt.reason)
				}
				if *stopCount != 1 {
					t.Fatalf("stopCount = %d, want 1", *stopCount)
				}
			case <-time.After(2 * time.Second):
				cancel()
				<-done
				t.Fatal("timed out waiting for lifecycle policy event")
			}
		})
	}
}

func TestLifecycleEnforcerSkipsUnreachableVM(t *testing.T) {
	root := shortTempDir(t)
	vmDir := filepath.Join(root, "vm")
	if err := os.Mkdir(vmDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := vmpolicy.Save(vmDir, vmpolicy.Policy{MaxAge: time.Second}); err != nil {
		t.Fatalf("Save(): %v", err)
	}
	enforcer := NewLifecycleEnforcer(LifecycleConfig{
		VMRoot: root,
		Now:    func() time.Time { return time.Unix(4000, 0).UTC() },
	})
	enforcer.EnforceOnce(context.Background())
	if got := enforcer.Stats().Enforced; got != 0 {
		t.Fatalf("enforced = %d, want 0", got)
	}
}

func serveLifecycleControl(t *testing.T, vmDir string, status vmStatus) *int {
	t.Helper()
	socket := filepath.Join(vmDir, "control.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { listener.Close() })

	stopCount := 0
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			handleLifecycleControlConn(conn, status, &stopCount)
		}
	}()
	t.Cleanup(func() {
		listener.Close()
		<-done
	})
	return &stopCount
}

func handleLifecycleControlConn(conn net.Conn, status vmStatus, stopCount *int) {
	defer conn.Close()
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return
	}
	var req controlpb.ControlRequest
	if err := controlx.ProtoJSONUnmarshaler.Unmarshal(line, &req); err != nil {
		_ = controlx.WriteResponse(conn, &controlpb.ControlResponse{Error: err.Error()})
		return
	}
	switch req.Type {
	case "status":
		_ = controlx.WriteResponse(conn, lifecycleStatusResponse(status))
	case "request-stop":
		(*stopCount)++
		_ = controlx.WriteResponse(conn, &controlpb.ControlResponse{Success: true})
	default:
		_ = controlx.WriteResponse(conn, &controlpb.ControlResponse{Error: "unknown command"})
	}
}

func lifecycleStatusResponse(status vmStatus) *controlpb.ControlResponse {
	data, _ := json.Marshal(map[string]any{
		"state":            status.State,
		"lastPing":         status.LastPing.Format(time.RFC3339),
		"policyStartedAt":  status.PolicyStartedAt.Format(time.RFC3339),
		"policyExecCount":  status.PolicyExecCount,
		"policyStopIssued": status.PolicyStopIssued,
	})
	return &controlpb.ControlResponse{
		Success: true,
		Data:    string(data),
		Result: &controlpb.ControlResponse_Status{Status: &controlpb.StatusResponse{
			State: status.State,
		}},
	}
}

func TestLifecycleEnforcerSkipsNonRunningState(t *testing.T) {
	for _, state := range []string{"stopped", "stopping", ""} {
		t.Run(state, func(t *testing.T) {
			root := shortTempDir(t)
			vmDir := filepath.Join(root, "vm")
			if err := os.Mkdir(vmDir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := vmpolicy.Save(vmDir, vmpolicy.Policy{RunBudget: 1}); err != nil {
				t.Fatalf("Save(): %v", err)
			}
			stopCount := serveLifecycleControl(t, vmDir, vmStatus{State: state, PolicyExecCount: 99})
			enforcer := NewLifecycleEnforcer(LifecycleConfig{
				VMRoot: root,
				Now:    func() time.Time { return time.Unix(4000, 0).UTC() },
			})
			enforcer.EnforceOnce(context.Background())
			if *stopCount != 0 {
				t.Fatalf("stopCount = %d, want 0 for state %q", *stopCount, state)
			}
			if got := enforcer.Stats().Enforced; got != 0 {
				t.Fatalf("enforced = %d, want 0", got)
			}
		})
	}
}

func TestParseTime(t *testing.T) {
	ref := time.Date(2026, 5, 9, 12, 30, 45, 0, time.UTC)
	tests := []struct {
		name   string
		in     string
		want   time.Time
		isZero bool
	}{
		{name: "empty", in: "", isZero: true},
		{name: "rfc3339", in: ref.Format(time.RFC3339), want: ref},
		{name: "rfc3339nano", in: "2026-05-09T12:30:45.123456789Z", want: time.Date(2026, 5, 9, 12, 30, 45, 123456789, time.UTC)},
		{name: "invalid", in: "not-a-time", isZero: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseTime(tt.in)
			if tt.isZero {
				if !got.IsZero() {
					t.Fatalf("got %v, want zero", got)
				}
				return
			}
			if !got.Equal(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func boolUint64(v bool) uint64 {
	if v {
		return 1
	}
	return 0
}

func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "cvd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func shortTempDirLink(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	link := filepath.Join("/tmp", "cvd-"+filepath.Base(dir))
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(link) })
	return link
}

func TestLifecyclePublishStopErrorEmitsEvent(t *testing.T) {
	bus := NewEventBus(8)
	e := NewLifecycleEnforcer(LifecycleConfig{Bus: bus})
	e.publishStopError(context.Background(), "vm1", "idle", &net.OpError{Op: "dial", Err: net.UnknownNetworkError("vsock")}, time.Unix(0, 0))
	tail := bus.Tail()
	if len(tail) != 1 {
		t.Fatalf("len(tail) = %d, want 1", len(tail))
	}
	got := tail[0]
	if got.EventType != "lifecycle.policy.stop_error" || got.Status != "error" || got.VMName != "vm1" {
		t.Fatalf("event = %#v", got)
	}
	if got.Extra["reason"] != "idle" {
		t.Fatalf("reason = %v, want idle", got.Extra["reason"])
	}
	if got.Extra["stop_error"] == nil || got.Extra["stop_error"] == "" {
		t.Fatalf("stop_error empty: %#v", got.Extra)
	}
}
