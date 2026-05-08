// Package controlserver holds the sub-components of the control plane.
//
// Per design 039 §7 (facade-late rule), all five sub-components live
// here: agent, capture, lifecycle, input, and network. The
// ControlServer facade itself remains in package main and embeds each
// of them by value.
//
// Sub-components that need access to ControlServer-owned state (the VM
// view, the underlying VM handle, etc.) take a narrow Host interface
// at construction rather than holding a back-reference to the facade.
// That keeps the dependency direction a one-way edge from package main
// into this package.
package controlserver
