package main

import (
	"context"
	"strings"
	"testing"
)

func TestParseForwardReverseFlag(t *testing.T) {
	got, err := parseForwardArgs([]string{"vm1", "-reverse", "8080:3000"})
	if err != nil {
		t.Fatalf("parseForwardArgs: %v", err)
	}
	if !got.Reverse || got.VM != "vm1" || got.GuestPort != 8080 || got.HostPort != 3000 {
		t.Fatalf("spec = %#v, want reverse vm1 guest 8080 host 3000", got)
	}
	if got.RelayPort != uint32(forwardRelayBasePort+8080%forwardRelayPortWindow) {
		t.Fatalf("RelayPort = %d, want derived from guest port", got.RelayPort)
	}
}

func TestParseForwardNaturalReverse(t *testing.T) {
	got, err := parseForwardArgs([]string{"vm1", "vm:8080->host:3000"})
	if err != nil {
		t.Fatalf("parseForwardArgs: %v", err)
	}
	if !got.Reverse || got.GuestPort != 8080 || got.HostPort != 3000 {
		t.Fatalf("spec = %#v, want reverse 8080->3000", got)
	}
}

func TestParseForwardNaturalForward(t *testing.T) {
	got, err := parseForwardArgs([]string{"vm1", "host:3000->vm:8080"})
	if err != nil {
		t.Fatalf("parseForwardArgs: %v", err)
	}
	if got.Reverse || got.HostPort != 3000 || got.GuestPort != 8080 {
		t.Fatalf("spec = %#v, want forward 3000->8080", got)
	}
}

func TestParseForwardNaturalErrors(t *testing.T) {
	_, err := parseForwardArgs([]string{"vm1", "vm:8080->vm:3000"})
	if err == nil || !strings.Contains(err.Error(), "expected host:port->vm:port or vm:port->host:port") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunForwardReverseUsesStarter(t *testing.T) {
	starter := &fakeForwardStarter{}
	newStarter := func(vm string) forwardStarter {
		if vm != "vm1" {
			t.Fatalf("starter vm = %q, want vm1", vm)
		}
		return starter
	}
	if err := runForward(context.Background(), []string{"vm1", "-reverse", "8080:3000"}, newStarter); err != nil {
		t.Fatalf("runForward: %v", err)
	}
	if !starter.spec.Reverse || starter.spec.GuestPort != 8080 || starter.spec.HostPort != 3000 {
		t.Fatalf("starter spec = %#v, want reverse guest 8080 host 3000", starter.spec)
	}
}
