package main

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	vz "github.com/tmc/apple/virtualization"
	"github.com/tmc/cove/internal/controlserver"
	controlpb "github.com/tmc/cove/proto/controlpb"
)

type stalledNetworkInfoHost struct {
	controlserver.AgentHost
	started chan context.Context
	release chan struct{}
}

func (h stalledNetworkInfoHost) VMState() (vz.VZVirtualMachineState, error) {
	return vz.VZVirtualMachineStateRunning, nil
}

func (h stalledNetworkInfoHost) LifecycleContext() context.Context {
	return context.Background()
}

func (h stalledNetworkInfoHost) DialAgent(ctx context.Context, _ uint32) (net.Conn, error) {
	h.started <- ctx
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-h.release:
		return nil, context.Canceled
	}
}

func TestNetworkInfoBoundsStalledAgentAcquisition(t *testing.T) {
	dir := t.TempDir()
	const mac = "aa:bb:cc:dd:ee:ff"
	if err := os.WriteFile(filepath.Join(dir, "mac.address"), []byte(mac+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	host := stalledNetworkInfoHost{started: make(chan context.Context, 1), release: make(chan struct{})}
	defer close(host.release)
	s := &ControlServer{vmDir: dir, lifeDone: make(chan struct{})}
	s.bridge = controlserver.NewAgentBridge(host)
	done := make(chan *controlpb.ControlResponse, 1)
	go func() { done <- s.handleNetworkInfo() }()
	select {
	case ctx := <-host.started:
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 5*time.Second {
			t.Errorf("agent acquisition deadline = %v, present %v; want network-info budget", deadline, ok)
		}
	case <-time.After(time.Second):
		t.Fatal("agent acquisition did not start")
	}
	close(s.lifeDone)
	select {
	case resp := <-done:
		info := resp.GetNetworkInfo()
		if !resp.Success || info == nil || info.MacAddress != mac || info.GuestIp != "" {
			t.Fatalf("stalled agent changed saved network information: %v", resp)
		}
	case <-time.After(time.Second):
		t.Fatal("network-info did not return after lifecycle cancellation")
	}
}
