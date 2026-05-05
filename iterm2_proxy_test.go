package main

import (
	"testing"

	"github.com/tmc/apple/dispatch"
	"github.com/tmc/apple/objc"
	vz "github.com/tmc/apple/virtualization"
)

func TestNewITerm2ProxyCapturesGuestConnector(t *testing.T) {
	vm1 := vz.VZVirtualMachineFromID(objc.ID(1))
	vm2 := vz.VZVirtualMachineFromID(objc.ID(2))
	q1 := dispatch.QueueFromHandle(11)
	q2 := dispatch.QueueFromHandle(22)

	s := &ControlServer{}
	s.SetVM(vm1, q1)
	proxy := NewITerm2Proxy(s, 0)
	s.SetVM(vm2, q2)

	if proxy.guest.vm.ID != vm1.ID {
		t.Fatalf("proxy vm ID = %v, want %v", proxy.guest.vm.ID, vm1.ID)
	}
	if got, want := proxy.guest.queue.Handle(), q1.Handle(); got != want {
		t.Fatalf("proxy queue handle = %v, want %v", got, want)
	}
	if proxy.port != iterm2DefaultPort {
		t.Fatalf("proxy port = %d, want %d", proxy.port, iterm2DefaultPort)
	}
}
