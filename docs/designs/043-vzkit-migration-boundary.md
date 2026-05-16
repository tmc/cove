# Design 043: Vzkit Migration Boundary

Status: Draft.
Date: 2026-05-15

## Problem

Cove already depends on `github.com/tmc/apple/x/vzkit` for focused Virtualization
helpers: display parsing, VirtioFS, capture, clipboard, VNC, debug stubs,
balloon control, and runtime state. The remaining raw Virtualization code is
still spread across `macos.go`, `linux.go`, `windows.go`, `usb.go`,
`block_device.go`, `networking.go`, `volumes.go`, and
`runtime_private.go`.

Some of that code is reusable VM construction. Some of it is Cove product
policy. Moving both would make vzkit harder to use and would hide Cove behavior
inside a shared library. The goal is to move the reusable Virtualization layer
out of Cove while keeping Cove's CLI, state layout, provisioning, guest agent,
and control API in Cove.

This design is an extraction plan. It is not an implementation.

## Boundary

Vzkit should own:

- pure Virtualization.framework configuration construction;
- purego Objective-C lifetime and private-API wrappers;
- reusable device builders for storage, display, audio, network, VirtioFS,
  vsock, entropy, balloon, serial, USB, and debug devices;
- generic VM lifecycle calls and state predicates;
- portable validation for device specs that have no Cove policy;
- small typed options that can be used by more than one VM tool.

Cove should own:

- CLI flags, command behavior, help text, docs, and support bundles;
- VM directory layout, saved config files, image store, fork/disposable state,
  and compatibility with existing VMs;
- install, provisioning, login, guest-agent, and setup-assistant flows;
- runtime control socket requests, JSON status shapes, and user-visible errors;
- sandbox, network policy, proxy, runner, action, daemon, and fleet policy;
- product defaults, including display size, networking mode, and credential
  behavior.

The practical rule is: if a function needs a Cove global, a VM name, a saved
config path, a control socket shape, guest-agent behavior, or a user-facing
message, keep it in Cove. If it only turns typed inputs into VZ objects or
wraps a framework operation, move it to vzkit.

## Current Overlap

Vzkit already has the right lower-level packages:

- `display` parses display specs and creates Mac or Virtio graphics devices.
- `network` parses basic network modes and creates NAT, bridged, host-only,
  vmnet, and vhost-user attachments.
- `virtiofs` parses docker-style mount specs and creates VirtioFS devices.
- `storage` creates disk attachments, block devices, directory shares, serial
  consoles, and NSData conversions.
- `vm`, `balloon`, `capture`, `clipboard`, `vnc`, and `debugstub` wrap runtime
  features Cove already uses.
- `storagehotplug`, `usbpassthrough`, `vminput`, `framebuffer`, `configcodec`,
  and `privatevm` are natural homes for private APIs now tested or called from
  Cove.

Cove still contains reusable construction code:

- `linux.go` builds generic platform state, EFI stores, Linux boot loaders,
  storage devices, Virtio graphics, entropy, balloon, vsock, serial, Rosetta,
  shared folders, and USB devices.
- `macos.go` builds repeated array setters, macOS storage, graphics, network,
  keyboard, pointing, entropy, audio, serial, and VirtioFS devices.
- `windows.go` builds generic platform state, EFI boot, NVMe and USB storage,
  Virtio or linear framebuffer display, network, USB input, entropy, audio,
  balloon, vsock, serial, and private Windows boot pieces.
- `usb.go` creates USB mass-storage device configurations and makes sure the VM
  has a USB controller.
- `block_device.go` creates runtime block storage devices.
- `networking.go` adapts Cove network flags into VZ network devices.
- `runtime_private.go` starts VNC servers and attaches GDB debug stubs.

## Move First

### 1. Storage and USB Device Builders

Move the reusable parts of `usb.go`, `block_device.go`,
`createLinuxRootStorageDevice`, `createLinuxStorageDeviceWithAttachment`,
`windowsNVMeStorageDevice`, and `windowsUSBStorageDevice` into vzkit storage
packages.

Suggested homes:

- `vzkit/storage`: disk attachment, Virtio block, NVMe, USB mass storage, and
  serial console constructors;
- `vzkit/storagehotplug`: runtime attach/detach primitives;
- `vzkit/usbpassthrough` or a new `vzkit/usb`: USB controller helpers.

Cove should keep `USBStorageSlice`, flag parsing, validation messages, control
socket requests, and runtime JSON response types.

### 2. Generic Platform and EFI Helpers

Move generic machine identifier and EFI variable store helpers out of
`linux.go` and `windows.go`.

Suggested home: root `vzkit` or a small `vzkit/platform` package. The API should
accept explicit paths:

```go
type GenericPlatformOptions struct {
	StateDir              string
	MachineIdentifierPath string
	EFIVariableStorePath  string
	NestedVirtualization  *bool
}
```

Cove should still decide the paths, whether nested virtualization is enabled,
and how saved config compatibility is handled.

### 3. Config Array Setters

Cove has repeated helpers such as `setStorageDevices`, `setNetworkDevices`,
`setKeyboards`, `setPointingDevices`, `setEntropyDevices`,
`setMemoryBalloonDevices`, `setSocketDevices`, `setSerialPorts`, and graphics
variants. Move these to vzkit if generated bindings still make direct setter
calls awkward.

These helpers should remain mechanical. They should not choose defaults.

### 4. Runtime VNC and Debug Primitives

Move generic VNC server start/stop and debug-stub attach primitives from
`runtime_private.go` into `vzkit/vnc` and `vzkit/debugstub` if those packages do
not already cover the complete operation.

Cove should keep:

- `-vnc`, `-vnc-password`, Bonjour, and GDB flag validation;
- `vncStatus` and `debugStubStatus` JSON shapes;
- control socket commands;
- user-facing hints and error wording.

## Move After the First Slice

### Linux Configuration Builder

Vzkit already has `BuildLinuxVMConfig`, but Cove's Linux path has more product
policy than the current builder can represent: installed boot artifact
detection, persistent MAC address handling, Cove display defaults, sandboxed
Rosetta policy, shared-folder live apply, USB storage, clipboard support, proxy
and network policy, and guest-agent expectations.

Do not replace Cove's Linux builder in one step. First move the low-level
constructors listed above. Then add a smaller vzkit builder that accepts a typed
device plan and returns a VZ configuration without reading Cove state.

The Cove side should convert `vmrun.RunConfig` and `HostConfig` into that plan.
That conversion is product policy and should stay in Cove.

### macOS Configuration Builder

Vzkit's `BuildMacVMConfig` covers the reusable macOS base case. Cove's macOS
path also owns identity recovery, suspend fingerprints, cold-boot decisions,
setup-assistant automation, login watchdogs, stale control sockets, AppKit
window setup, and save/restore policy.

Move only mechanical VZ construction from `macos.go`. Keep lifecycle and
identity orchestration in Cove until it is represented as explicit inputs and
tests show old VMs still boot.

### Windows Configuration Builder

Windows has the largest remaining duplication with vzkit-style code, but it
also uses private boot, framebuffer, storage, and setup behavior that is less
stable. Treat Windows as the second consumer that proves the storage, platform,
graphics, USB, and serial helpers are general.

After those helpers land, add either `vzkit/windows` or a generic builder that
does not mention Cove. Avoid putting Windows policy into root vzkit.

## Do Not Move

These areas should stay in Cove:

- `vmrun` planning and host capability decisions;
- CLI flag parsing, help, documentation, and examples;
- VM names, VM directory layout, saved config files, and image metadata;
- support bundle and doctor checks;
- install media discovery, provisioning, cloud-init, setup assistant, and login;
- guest-agent protocol and daemon/user-agent routing;
- control socket APIs and status JSON compatibility;
- sandbox, proxy, port forwarding, network audit, and runner/action policy;
- screen/OCR automation that is about Cove workflows rather than VZ primitives.

ScreencaptureKit wrappers should not move to vzkit just because Cove uses them
near VMs. If they need a shared home, use a separate Apple capture package.

## Migration Plan

### Phase 0: Inventory and Tests

Add or tighten unit tests around current helpers before moving them. The useful
tests are table-driven parse tests, constructor tests that can run without
booting a VM, and small private-API availability tests guarded by explicit
conditions.

### Phase 1: Pure Helper Moves

Move storage, USB, generic platform, EFI, serial, and config-array helpers into
vzkit. Keep Cove wrappers temporarily when that avoids large call-site churn.
The wrappers should be thin and deleted once call sites are migrated.

### Phase 2: Plan-Based Builders

Introduce a plan type in Cove that is independent of flags and globals. Convert
that plan into vzkit builder inputs. The builder inputs should contain paths,
device specs, and booleans, not Cove VM names or saved config structs.

### Phase 3: Linux and macOS Adoption

Adopt the plan-based builder in Linux first, because it has less identity and
restore coupling than macOS. Adopt macOS only after the storage and platform
helpers have already shipped and live boot tests pass on existing VMs.

### Phase 4: Windows and Runtime Private APIs

Use Windows to validate the generalized storage, USB, platform, graphics, and
serial APIs. Move runtime private operations only after Cove can preserve its
existing control socket status and errors exactly.

## Validation

For every slice:

- run vzkit unit tests in `../apple/x/vzkit`;
- run Cove unit tests for changed call sites;
- build and re-sign `./cove` after any Go build;
- smoke one macOS or Linux VM path when the slice changes boot, storage,
  network, or display construction;
- compare user-visible `cove run -h`, `cove vnc -h`, control socket JSON, and
  support bundle output when a slice touches runtime status.

Private API slices should also have an opt-in live test path. They should not
make normal `go test ./...` depend on booting a VM or on a specific macOS seed.

## Risks

- Vzkit can accidentally absorb Cove product policy and become hard to reuse.
- Moving macOS identity, auxiliary storage, or suspend behavior can break
  existing VMs.
- Default changes in display, network, or storage device type can be visible to
  users even if tests still pass.
- Private API wrappers may compile on one macOS version and fail at runtime on
  another.
- A broad builder swap makes regressions hard to localize.

Keep each move small enough that Cove can still show the exact behavior change,
or lack of one, with one focused test or live smoke.

## Open Questions

- Should generic platform helpers live in root vzkit or a `platform`
  subpackage?
- Should Windows get a dedicated `vzkit/windows` package, or should it consume
  lower-level storage, display, platform, and input helpers directly?
- How stable should vzkit private-API packages promise to be while Apple's
  private selectors are still moving?
- Should vzkit expose builder plans as concrete structs only, or also expose
  smaller functional options for tools that need partial construction?
