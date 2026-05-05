package main

import (
	"sync"
	"testing"
)

func TestPortForwardManagerConcurrentAccessor(t *testing.T) {
	s := NewControlServerWithVMDir("", t.TempDir())

	const goroutines = 64
	got := make(chan *PortForwardManager, goroutines)
	var wg sync.WaitGroup
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got <- s.portForwardManager()
		}()
	}
	wg.Wait()
	close(got)

	var first *PortForwardManager
	for manager := range got {
		if manager == nil {
			t.Fatal("portForwardManager returned nil")
		}
		if first == nil {
			first = manager
			continue
		}
		if manager != first {
			t.Fatal("portForwardManager returned multiple managers")
		}
	}
}

func TestClearPortForwardManager(t *testing.T) {
	s := NewControlServerWithVMDir("", t.TempDir())
	manager := s.portForwardManager()

	if got := s.clearPortForwardManager(); got != manager {
		t.Fatalf("clearPortForwardManager = %p, want %p", got, manager)
	}
	if s.portForwards != nil {
		t.Fatal("clearPortForwardManager left manager installed")
	}
}
