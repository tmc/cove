package main

import (
	"sync"
	"testing"
)

func TestParsePortForwardSpec(t *testing.T) {
	got, err := parsePortForwardSpec("8080:80")
	if err != nil {
		t.Fatalf("parsePortForwardSpec: %v", err)
	}
	if got.HostPort != 8080 || got.GuestPort != 80 {
		t.Fatalf("parsePortForwardSpec = %#v, want 8080:80", got)
	}
}

func TestParsePortForwardSpecRejectsInvalid(t *testing.T) {
	for _, in := range []string{"", "8080", "0:80", "8080:0", "host:80", "8080:guest"} {
		t.Run(in, func(t *testing.T) {
			if _, err := parsePortForwardSpec(in); err == nil {
				t.Fatalf("parsePortForwardSpec(%q) succeeded, want error", in)
			}
		})
	}
}

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
	if got := s.network.ClearPortForwards(); got != nil {
		t.Fatalf("clearPortForwardManager left manager installed: %p", got)
	}
}
