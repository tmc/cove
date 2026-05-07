// InputHost is the narrow back-channel the input bridge uses to reach
// state owned by the control server facade in package main.
//
// The input bridge holds an InputHost rather than a *ControlServer
// back-reference so the dependency direction stays one-way from package
// main into this package (per design 039 §7).
package controlserver

import (
	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/dispatch"
	vz "github.com/tmc/apple/virtualization"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

// InputHost exposes the slice of control-server state the input bridge
// needs to dispatch pointer and keyboard events: VM and view handles
// for direct framebuffer input, the host window for AppKit NSEvent
// delivery, the cached content height for mouse-Y mapping, the
// configured automation backend modes, and the RPC serialization
// mutex that typeText acquires per key event.
//
// Implementations must be safe for concurrent use; the bridge dispatches
// from RPC and automation goroutines.
type InputHost interface {
	// VM returns the VZ virtual machine handle.
	VM() vz.VZVirtualMachine

	// VMView returns the VZ virtual machine view (zero value when
	// running headless).
	VMView() vz.VZVirtualMachineView

	// Window returns the host AppKit window hosting the VMView (zero
	// value when running headless).
	Window() appkit.NSWindow

	// ViewContentHeight returns the cached VM content area height in
	// pixels, excluding the title bar. Mouse-Y mapping must flip
	// against this value, not the NSView bounds.
	ViewContentHeight() int

	// CaptureBackend returns the configured screen-capture backend.
	CaptureBackend() BackendMode

	// InputBackend returns the configured input backend.
	InputBackend() BackendMode

	// LastCaptureBounds returns the dimensions of the most recent
	// screen capture, or (0, 0) if none has been recorded.
	LastCaptureBounds() (width, height int)

	// Lock and Unlock guard the RPC serialization mutex. typeText
	// acquires this around each key event so each send observes the
	// same control-server state as a fresh key RPC.
	Lock()
	Unlock()

	// VMQueue returns the dispatch queue used to serialize VM input
	// sends. The bridge's private-keyboard path passes it to
	// vminput.NewSender.
	VMQueue() dispatch.Queue

	// Verbose reports whether the host is in verbose-logging mode. The
	// bridge's mouse/keyboard paths emit packet traces when true.
	Verbose() bool

	// PrivateKeyHook returns a test override for the private-keyboard
	// path, or nil to use the real path. The bridge's SendKeyPrivate
	// calls the hook (if non-nil) instead of dispatching through
	// vminput.NewSender, which lets tests assert the path is reached
	// without a live VM. The hook is owned by package main; this
	// accessor lets the bridge consult it without taking a back
	// reference to *ControlServer.
	PrivateKeyHook() func(*controlpb.KeyCommand) *controlpb.ControlResponse
}

// BackendMode identifies a capture or input backend.
//
// The values mirror package main's automationBackendMode enum so a
// host that stores its mode as automationBackendMode can convert with
// BackendMode(mode).
type BackendMode int32

// Backend modes. The integer values are part of the contract: package
// main's automationBackendMode constants share the same values.
const (
	BackendAuto BackendMode = iota
	BackendFramebuffer
	BackendWindow
)
