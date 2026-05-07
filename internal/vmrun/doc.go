// Package vmrun describes the configuration of a single VM run or install
// as plain data. It is consumed by package main's macOS, Linux, and Windows
// entry points after global flag parsing, and exists so those entry points
// no longer reach into package-level globals.
//
// The package owns three values:
//
//   - RunConfig: per-run user intent (CPU, memory, disk, displays, network,
//     boot mode, provisioning intent).
//   - HostConfig: host-side context (VM directory, runtime profile, launch
//     order, verbosity).
//   - DevicePlan: a derived, pre-AppKit description of the devices the VM
//     should expose.
//
// Plan turns a (RunConfig, HostConfig) pair into a DevicePlan or returns a
// validation error. It performs no I/O and creates no Virtualization.framework
// objects; AppKit and VZ object creation stay in package main until the data
// boundary holds.
//
// vmrun has no dependency on package main, AppKit, or the Virtualization
// framework. That keeps validation table-testable without a live host.
package vmrun
