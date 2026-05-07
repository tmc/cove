package main

import (
	"context"
	"net"
	"sync"
	"testing"
)

func TestNetworkBridgePortForwardManagerLazy(t *testing.T) {
	var n networkBridge
	got := n.portForwardManager()
	if got == nil {
		t.Fatal("portForwardManager returned nil")
	}
	if again := n.portForwardManager(); again != got {
		t.Fatal("portForwardManager should cache the manager")
	}
}

func TestNetworkBridgePortForwardManagerConcurrent(t *testing.T) {
	var n networkBridge
	const goroutines = 64
	results := make(chan *PortForwardManager, goroutines)
	var wg sync.WaitGroup
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- n.portForwardManager()
		}()
	}
	wg.Wait()
	close(results)

	var first *PortForwardManager
	for m := range results {
		if m == nil {
			t.Fatal("portForwardManager returned nil")
		}
		if first == nil {
			first = m
			continue
		}
		if m != first {
			t.Fatal("portForwardManager returned multiple managers under concurrency")
		}
	}
}

func TestNetworkBridgeClearPortForwardManager(t *testing.T) {
	var n networkBridge
	if got := n.clearPortForwardManager(); got != nil {
		t.Fatalf("clearPortForwardManager on empty bridge = %p, want nil", got)
	}
	manager := n.portForwardManager()
	if got := n.clearPortForwardManager(); got != manager {
		t.Fatalf("clearPortForwardManager = %p, want %p", got, manager)
	}
	if again := n.clearPortForwardManager(); again != nil {
		t.Fatalf("clearPortForwardManager after clear = %p, want nil", again)
	}
}

func TestNetworkBridgeVNCStatusDerivedState(t *testing.T) {
	var n networkBridge
	if got := n.vncStatusValue(); got.State != "disabled" {
		t.Fatalf("default vncStatus.State = %q, want disabled", got.State)
	}
	n.setVNCStatus(VNCStatus{Enabled: true})
	if got := n.vncStatusValue(); got.State != "enabled" {
		t.Fatalf("enabled vncStatus.State = %q, want enabled", got.State)
	}
	n.setVNCStatus(VNCStatus{Enabled: true, State: "binding"})
	if got := n.vncStatusValue(); got.State != "binding" {
		t.Fatalf("explicit vncStatus.State = %q, want binding", got.State)
	}
}

func TestNetworkBridgeDebugStubStatusDerivedState(t *testing.T) {
	var n networkBridge
	if got := n.debugStubStatusValue(); got.State != "disabled" {
		t.Fatalf("default debugStubStatus.State = %q, want disabled", got.State)
	}
	n.setDebugStubStatus(DebugStubStatus{Enabled: true})
	if got := n.debugStubStatusValue(); got.State != "enabled" {
		t.Fatalf("enabled debugStubStatus.State = %q, want enabled", got.State)
	}
}

func TestNetworkBridgeAddHTTPListenerLazy(t *testing.T) {
	var n networkBridge
	ln1, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	ln2, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		ln1.Close()
		t.Fatalf("net.Listen: %v", err)
	}
	n.addHTTPListener(ln1)
	n.addHTTPListener(ln2)

	n.closeHTTPListeners()
	n.stopPortForwards()
	n.stopITerm2Proxy(context.Background())

	// closeAll closed both listeners; a second Accept must error.
	if _, err := ln1.Accept(); err == nil {
		t.Fatal("ln1.Accept after shutdown succeeded, want error")
	}
	if _, err := ln2.Accept(); err == nil {
		t.Fatal("ln2.Accept after shutdown succeeded, want error")
	}
}

func TestNetworkBridgeTeardownIdempotent(t *testing.T) {
	var n networkBridge
	n.closeHTTPListeners()
	n.stopPortForwards()
	n.stopITerm2Proxy(context.Background())
	n.closeHTTPListeners()
	n.stopPortForwards()
	n.stopITerm2Proxy(context.Background())
}

func TestNetworkBridgeStopPortForwardsClearsManager(t *testing.T) {
	var n networkBridge
	if manager := n.portForwardManager(); manager == nil {
		t.Fatal("portForwardManager returned nil")
	}
	n.stopPortForwards()
	if got := n.clearPortForwardManager(); got != nil {
		t.Fatalf("portForwards still installed after stopPortForwards: %p", got)
	}
}
