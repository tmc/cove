package main

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	vz "github.com/tmc/apple/virtualization"
	"github.com/tmc/cove/internal/controlserver"
	controlpb "github.com/tmc/cove/proto/controlpb"
)

type stalledFDAProbeHost struct {
	controlserver.AgentHost
	started chan context.Context
}

func (stalledFDAProbeHost) VMState() (vz.VZVirtualMachineState, error) {
	return vz.VZVirtualMachineStateRunning, nil
}

func (stalledFDAProbeHost) LifecycleContext() context.Context { return context.Background() }

func (h stalledFDAProbeHost) DialAgent(ctx context.Context, _ uint32) (net.Conn, error) {
	h.started <- ctx
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestTCCFDAProbeBoundsStalledAgentAcquisition(t *testing.T) {
	host := stalledFDAProbeHost{started: make(chan context.Context, 1)}
	s := &ControlServer{lifeDone: make(chan struct{})}
	s.bridge = controlserver.NewAgentBridge(host)
	done := make(chan *controlpb.ControlResponse, 1)
	go func() { done <- s.handleAgentTCCFDAProbe() }()
	select {
	case ctx := <-host.started:
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > tccFDAProbeBudget {
			t.Errorf("acquisition deadline = %v, present %v; want probe budget", deadline, ok)
		}
	case <-time.After(time.Second):
		t.Fatal("agent acquisition did not start")
	}
	close(s.lifeDone)
	select {
	case resp := <-done:
		status, detail := parseTCCFDAProbe(resp.Data)
		if !resp.Success || status != tccFDAUnknown || !strings.Contains(detail, "agent unavailable") {
			t.Fatalf("stalled acquisition reported %v (%s, %q), want unknown", resp, status, detail)
		}
	case <-time.After(time.Second):
		t.Fatal("probe did not return after lifecycle cancellation")
	}
}
