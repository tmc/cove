// Command cove manages macOS and Linux virtual machines with Apple's
// Virtualization framework.
//
// # Overview
//
// cove creates, provisions, runs, snapshots, and controls local virtual
// machines on Apple Silicon Macs. It uses generated purego bindings for
// Apple's native frameworks, so the main binary is built without cgo.
//
// VM data is stored under ~/.vz. A running VM exposes a per-VM Unix control
// socket and token for local automation, and higher-level commands such as
// cove ctl, cove shell, cove cp, and cove serve use the same control path.
//
// # Common Commands
//
// A typical first run is:
//
//	cove doctor host
//	cove up -user <name>
//	cove list
//	cove run
//
// Common command groups are:
//
//	install, up, run          create and boot VMs
//	provision, verify         prepare guest users and agents
//	ctl, shell, cp            control a running VM
//	image, fork, clone        create reusable images and copy-on-write VMs
//	snapshot, disk-snapshot   save and restore VM or disk state
//	serve                    expose the local control API over HTTP/MCP
//	support bundle           collect redacted diagnostics
//
// Run cove help advanced for the complete command list, and cove <command> -h
// for command-specific flags.
//
// # Guest Support
//
// macOS guests are installed from IPSW restore images. Linux guests use EFI
// boot and cloud-init based installation paths. Windows support is
// experimental and lives behind explicit Windows commands and flags.
//
// Each VM is stored in ~/.vz/vms/<name>. Images built by cove image are stored
// separately under ~/.vz/images and can be materialized with cove run
// -fork-from.
//
// # Control Socket
//
// Running VMs expose a local socket and token:
//
//	~/.vz/vms/<name>/control.sock
//	~/.vz/vms/<name>/control.token
//
// The socket accepts JSON using the protobuf JSON mapping of ControlRequest.
// Most callers should use cove ctl or the internal controlclient package
// instead of writing socket JSON by hand.
//
// # Build and Signing
//
// Virtualization.framework requires the virtualization entitlement. On normal
// startup, cove checks the running binary, ad-hoc signs it with the embedded
// entitlement when needed, and re-execs.
//
// If autosigning fails or the normal startup path is bypassed, sign the binary
// manually:
//
//	go build -o cove .
//	codesign -s - -f --entitlements internal/autosign/vz.entitlements ./cove
//
// Manual signing is also useful for tests and launch paths that cannot re-exec
// after autosigning.
//
// # Repository Layout
//
// Package main contains CLI dispatch and cove product policy. Reusable code is
// kept in focused internal packages, such as internal/imagestore for local
// image-store primitives and internal/controlclient for the control-socket
// client. Product-neutral Virtualization.framework helpers live in the
// github.com/tmc/apple/x/vzkit module.
//
// # Requirements
//
//   - Apple Silicon Mac
//   - macOS 14.0+ (Sonoma or later)
//   - Xcode Command Line Tools
//   - an IPSW restore image for macOS installation
//
// # References
//
//   - Apple Virtualization Framework: https://developer.apple.com/documentation/virtualization
package main

//go:generate go build -o cove .
//go:generate codesign --entitlements internal/autosign/vz.entitlements -f -s - ./cove
