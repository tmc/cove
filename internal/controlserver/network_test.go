package controlserver

import (
	"context"
	"net"
	"sync"
	"testing"
)

func TestNetworkBridgePortForwardManagerLazy(t *testing.T) {
	var n NetworkBridge
	got := n.PortForwards()
	if got == nil {
		t.Fatal("PortForwards returned nil")
	}
	if again := n.PortForwards(); again != got {
		t.Fatal("PortForwards should cache the manager")
	}
}

func TestNetworkBridgePortForwardManagerConcurrent(t *testing.T) {
	var n NetworkBridge
	const goroutines = 64
	results := make(chan *PortForwardManager, goroutines)
	var wg sync.WaitGroup
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- n.PortForwards()
		}()
	}
	wg.Wait()
	close(results)

	var first *PortForwardManager
	for m := range results {
		if m == nil {
			t.Fatal("PortForwards returned nil")
		}
		if first == nil {
			first = m
			continue
		}
		if m != first {
			t.Fatal("PortForwards returned multiple managers under concurrency")
		}
	}
}

func TestNetworkBridgeClearPortForwardManager(t *testing.T) {
	var n NetworkBridge
	if got := n.ClearPortForwards(); got != nil {
		t.Fatalf("ClearPortForwards on empty bridge = %p, want nil", got)
	}
	manager := n.PortForwards()
	if got := n.ClearPortForwards(); got != manager {
		t.Fatalf("ClearPortForwards = %p, want %p", got, manager)
	}
	if again := n.ClearPortForwards(); again != nil {
		t.Fatalf("ClearPortForwards after clear = %p, want nil", again)
	}
}

func TestNetworkBridgeVNCStatusDerivedState(t *testing.T) {
	var n NetworkBridge
	if got := n.VNCStatusValue(); got.State != "disabled" {
		t.Fatalf("default vncStatus.State = %q, want disabled", got.State)
	}
	n.SetVNCStatus(VNCStatus{Enabled: true})
	if got := n.VNCStatusValue(); got.State != "enabled" {
		t.Fatalf("enabled vncStatus.State = %q, want enabled", got.State)
	}
	n.SetVNCStatus(VNCStatus{Enabled: true, State: "binding"})
	if got := n.VNCStatusValue(); got.State != "binding" {
		t.Fatalf("explicit vncStatus.State = %q, want binding", got.State)
	}
}

func TestNetworkBridgeDebugStubStatusDerivedState(t *testing.T) {
	var n NetworkBridge
	if got := n.DebugStubStatusValue(); got.State != "disabled" {
		t.Fatalf("default debugStubStatus.State = %q, want disabled", got.State)
	}
	n.SetDebugStubStatus(DebugStubStatus{Enabled: true})
	if got := n.DebugStubStatusValue(); got.State != "enabled" {
		t.Fatalf("enabled debugStubStatus.State = %q, want enabled", got.State)
	}
}

func TestNetworkBridgeAddHTTPListenerLazy(t *testing.T) {
	var n NetworkBridge
	ln1, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	ln2, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		ln1.Close()
		t.Fatalf("net.Listen: %v", err)
	}
	n.AddHTTPListener(ln1)
	n.AddHTTPListener(ln2)

	n.CloseHTTPListeners()
	n.StopPortForwards()
	n.StopITerm2Proxy(context.Background())

	if _, err := ln1.Accept(); err == nil {
		t.Fatal("ln1.Accept after shutdown succeeded, want error")
	}
	if _, err := ln2.Accept(); err == nil {
		t.Fatal("ln2.Accept after shutdown succeeded, want error")
	}
}

func TestNetworkBridgeTeardownIdempotent(t *testing.T) {
	var n NetworkBridge
	n.CloseHTTPListeners()
	n.StopPortForwards()
	n.StopITerm2Proxy(context.Background())
	n.CloseHTTPListeners()
	n.StopPortForwards()
	n.StopITerm2Proxy(context.Background())
}

func TestNetworkBridgeStopPortForwardsClearsManager(t *testing.T) {
	var n NetworkBridge
	if manager := n.PortForwards(); manager == nil {
		t.Fatal("PortForwards returned nil")
	}
	n.StopPortForwards()
	if got := n.ClearPortForwards(); got != nil {
		t.Fatalf("portForwards still installed after StopPortForwards: %p", got)
	}
}
