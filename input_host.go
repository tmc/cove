// input_host.go — ControlServer adapters that satisfy
// controlserver.InputHost.
//
// These adapters expose existing control-server state under the names
// the input bridge will reach for once it moves into
// internal/controlserver. The bridge in package main today still uses
// unexported back-references; the compile-time assertion below pins
// the contract so the later move is a mechanical rename rather than
// an interface negotiation.
package main

import (
	"github.com/tmc/apple/appkit"
	vz "github.com/tmc/apple/virtualization"

	"github.com/tmc/vz-macos/internal/controlserver"
)

var _ controlserver.InputHost = (*ControlServer)(nil)

// VM returns the VZ virtual machine handle.
func (s *ControlServer) VM() vz.VZVirtualMachine { return s.vm }

// VMView returns the VZ virtual machine view.
func (s *ControlServer) VMView() vz.VZVirtualMachineView { return s.vmView }

// Window returns the host AppKit window hosting the VMView.
func (s *ControlServer) Window() appkit.NSWindow { return s.window }

// ViewContentHeight returns the cached VM content area height in pixels.
func (s *ControlServer) ViewContentHeight() int { return s.viewContentHeight }

// CaptureBackend returns the configured screen-capture backend.
func (s *ControlServer) CaptureBackend() controlserver.BackendMode {
	return controlserver.BackendMode(s.captureBackend())
}

// InputBackend returns the configured input backend.
func (s *ControlServer) InputBackend() controlserver.BackendMode {
	return controlserver.BackendMode(s.inputBackend())
}

// LastCaptureBounds returns the dimensions of the most recent screen
// capture, or (0, 0) if none has been recorded.
func (s *ControlServer) LastCaptureBounds() (width, height int) {
	return s.lastCaptureBounds()
}

// Lock acquires the RPC serialization mutex.
func (s *ControlServer) Lock() { s.mu.Lock() }

// Unlock releases the RPC serialization mutex.
func (s *ControlServer) Unlock() { s.mu.Unlock() }
