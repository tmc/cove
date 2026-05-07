// Package controlserver holds the sub-components of the control plane.
//
// Per design 039 §7 (facade-late rule), the ControlServer facade
// itself remains in package main until all five sub-components have
// been extracted and have local invariants. This package is the
// destination for those sub-components: agent, capture, lifecycle,
// input, and network.
//
// Sub-components that need access to ControlServer-owned state (the VM
// view, the underlying VM handle, etc.) take a narrow Host interface
// at construction rather than holding a back-reference to the facade.
// That keeps the dependency direction a one-way edge from package main
// into this package.
package controlserver
