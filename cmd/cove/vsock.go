// vsock.go - Host-side vsock infrastructure for guest agent communication.
//
// Uses VZVirtioSocketDevice from Apple's Virtualization framework to establish
// bidirectional socket connections with the guest agent over vsock.

package main

import (
	"fmt"
	"net"

	"github.com/tmc/apple/dispatch"
	vz "github.com/tmc/apple/virtualization"
	vsockx "github.com/tmc/apple/x/vzkit/vsock"
)

// VsockDeviceManager manages the VZVirtioSocketDevice for a running VM.
type VsockDeviceManager struct {
	mgr *vsockx.Manager
}

// NewVsockDeviceManager wraps the VZVirtioSocketDevice from a running VM.
// The queue parameter is the VM's dispatch queue; Virtualization framework
// calls must be dispatched on this queue to avoid SIGTRAP crashes.
// The caller must run outside that queue because device lookup waits for it.
func NewVsockDeviceManager(vm vz.VZVirtualMachine, queue dispatch.Queue) (*VsockDeviceManager, error) {
	if vm.ID == 0 {
		return nil, fmt.Errorf("vm not initialized")
	}
	if queue.Handle() == 0 {
		return nil, fmt.Errorf("vm queue not initialized")
	}
	dispatch := func(fn func()) { DispatchAsyncQueue(queue, fn) }
	return newVsockDeviceManager(dispatch, func() (*vsockx.Manager, error) {
		return vsockx.NewManager(vm)
	})
}

func newVsockDeviceManager(dispatch func(func()), create func() (*vsockx.Manager, error)) (*VsockDeviceManager, error) {
	var mgr *vsockx.Manager
	var err error
	done := make(chan struct{})
	dispatch(func() {
		mgr, err = create()
		close(done)
	})
	<-done
	if err != nil {
		return nil, err
	}
	mgr.DispatchFunc = dispatch
	return &VsockDeviceManager{mgr: mgr}, nil
}

// ConnectToAgent establishes a vsock connection to the guest agent on the given port.
func (m *VsockDeviceManager) ConnectToAgent(port uint32) (net.Conn, error) {
	return m.mgr.Connect(port)
}
