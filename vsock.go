// vsock.go - Host-side vsock infrastructure for guest agent communication.
//
// Uses VZVirtioSocketDevice from Apple's Virtualization framework to establish
// bidirectional socket connections with the guest agent over vsock.
package main

import (
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
func NewVsockDeviceManager(vm vz.VZVirtualMachine, queue dispatch.Queue) (*VsockDeviceManager, error) {
	mgr, err := vsockx.NewManager(vm)
	if err != nil {
		return nil, err
	}
	mgr.DispatchFunc = func(fn func()) {
		DispatchAsyncQueue(queue, fn)
	}
	return &VsockDeviceManager{mgr: mgr}, nil
}

// ConnectToAgent establishes a vsock connection to the guest agent on the given port.
func (m *VsockDeviceManager) ConnectToAgent(port uint32) (net.Conn, error) {
	return m.mgr.Connect(port)
}
