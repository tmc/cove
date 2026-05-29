package main

import (
	"testing"

	"github.com/tmc/apple/dispatch"
	"github.com/tmc/apple/objc"
	vz "github.com/tmc/apple/virtualization"
)

// TestNewITerm2ProxyCapturesGuestConnector verifies that NewITerm2Proxy
// captures the VM and dispatch queue at construction time so later
// SetVM calls on the same ControlServer do not retroactively change
// the proxy's target guest.
func TestNewITerm2ProxyCapturesGuestConnector(t *testing.T) {
	vm1 := vz.VZVirtualMachineFromID(objc.ID(1))
	vm2 := vz.VZVirtualMachineFromID(objc.ID(2))
	q1 := dispatch.QueueFromHandle(11)
	q2 := dispatch.QueueFromHandle(22)

	s := &ControlServer{}
	s.SetVM(vm1, q1)
	proxy := NewITerm2Proxy(s, 0)
	s.SetVM(vm2, q2)

	guest, ok := proxy.Guest().(controlServerGuestConnector)
	if !ok {
		t.Fatalf("proxy guest type = %T, want controlServerGuestConnector", proxy.Guest())
	}
	if guest.vm.ID != vm1.ID {
		t.Fatalf("proxy vm ID = %v, want %v", guest.vm.ID, vm1.ID)
	}
	if got, want := guest.queue.Handle(), q1.Handle(); got != want {
		t.Fatalf("proxy queue handle = %v, want %v", got, want)
	}
	if proxy.Port() != iterm2DefaultPort {
		t.Fatalf("proxy port = %d, want %d", proxy.Port(), iterm2DefaultPort)
	}
}
