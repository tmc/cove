# cove codebase refactor plan

**Status**: draft v0
**Author**: cove team
**Date**: 2026-04-22
**Target**: begin immediately; land incrementally over a small series of reviewable PRs
**Scope**: repository-internal refactor; preserve CLI and wire behavior

## Changelog

- **v0 (2026-04-22)**: Initial refactor plan based on the design review and current repository shape.

## Goal

Break the codebase into a small set of internal packages with explicit ownership so the compiler enforces boundaries that are currently only implied by convention.

The refactor is not a rewrite. The goal is to make the existing product easier to change, easier to test, and harder to accidentally couple.

## Non-goals

- No user-facing CLI redesign as part of this work.
- No protocol rewrite for the control socket, HTTP API, MCP surface, or guest-agent RPCs.
- No daemon split or multi-module reorganization.
- No broad rename churn unless it buys a real ownership boundary.
- No flag day branch where half the tree stops building until the move is complete.

## Why this needs to happen

The root problem is not any one file. The problem is that almost all product logic still lives in `package main`.

Concrete hotspots:

- `control_socket.go:50` defines a `ControlServer` that owns socket lifecycle, VM state, GUI handles, screenshots, OCR, agent connections, iTerm2 proxying, port forwards, and HTTP listeners.
- `control_socket.go:1921` defines a global `controlServer`, which hides lifecycle and makes isolation difficult.
- `proxy.go:128` defines a `proxyRuntime` interface that mixes VM identity, process execution, and guest file I/O in one seam.
- `agent_control.go:484` runs a long-lived health loop with fixed sleeps and internal background timeouts instead of caller-owned lifecycle.
- `ipsw.go:32` shells out to `curl` directly and reaches into the real filesystem, which makes download logic hard to test.
- `boot_commands.go:38` hardcodes `time.Now` and `time.Sleep`, which makes OCR automation timing slow and nondeterministic to test.

These are symptoms of the same design issue: the codebase has weak ownership boundaries, so packages cannot protect invariants for us.

## Design principles

1. Keep the application an application. Use `internal/` packages, not public library packages.
2. Split by domain ownership, not by technical flavor. Avoid packages named `util`, `common`, or `helpers`.
3. Prefer concrete types. Introduce interfaces only where there is a real alternate implementation or a real test seam.
4. Keep package APIs narrow. If a consumer needs guest file reads, it should not also be forced to depend on guest exec.
5. Move logic before moving files. First create seams in place; then extract packages when the dependency direction is already clear.
6. Every phase must leave `go build ./...` and `go test ./...` green.
7. Preserve external behavior while the refactor is in flight. Internal cleanup is only valuable if it is shippable at every step.

## Target package shape

The end state should be small and opinionated:

```text
.
├── main.go                    # thin CLI entrypoint; keep go build . working
├── cmd/vz-agent               # guest agent stays separate
├── internal/agent             # host-side guest agent clients and health management
├── internal/control           # control socket, HTTP/MCP bridge, auth, request routing
├── internal/gui               # AppKit, UI thread, screenshots, OCR, display control
├── internal/provision         # disk injection, setup assistant, guest bootstrap files
├── internal/runtime           # VM lifecycle, devices, snapshots, suspend, memory, network
├── internal/vmconfig          # registry, config codec, paths, shared folders, volumes
├── internal/vzscript          # script engine and apply logic
├── internal/version           # build/runtime version resolution
└── internal/assets|autosign|diskimages2
```

This is intentionally not a maximal split. More packages would only create a different kind of sprawl.

## Dependency rules

These rules matter more than the directory names:

1. `main` may depend on any internal package. Internal packages may not depend on `main`.
2. `internal/runtime` may not read package globals such as `vmDir`, `linuxMode`, `cpuCount`, `memoryGB`, or `verbose`.
3. `internal/gui` owns raw AppKit and Objective-C handles. Unrelated packages should consume narrow Go interfaces, not AppKit types.
4. `internal/control` may depend on `internal/runtime`, `internal/agent`, `internal/gui`, and `proto/*`. Those packages must not depend back on `internal/control`.
5. `internal/vmconfig` owns disk layout, registry, and config serialization. It does not depend on GUI, control, or agent packages.
6. `internal/provision` may depend on `internal/vmconfig` and narrow runtime/config interfaces, but not on CLI globals or server globals.
7. `internal/vzscript` should depend on a narrow control client or executor surface, not on concrete control-server internals.
8. If two packages need to call each other, the boundary is wrong. Move the shared data type down or define a smaller consumer-owned interface.

## What to change before any file moves

The first phase should reduce coupling while everything is still in place. That keeps the later package extraction mostly mechanical.

### 1. Replace package-global configuration with explicit inputs

Introduce small configuration structs at the real seams:

- `RunConfig`
- `InstallConfig`
- `ServeConfig`
- `VMPaths`

Flag parsing stays in CLI code. Runtime, provisioning, and server code should receive explicit config instead of reading globals.

### 2. Split receiver-bound files before moving them

Several of the files that look like early extraction candidates are not
actually movable yet because they still bind methods directly to
`*ControlServer` or hold pointers to it.

Fix that before any package move:

- split `shared_folders_runtime.go` into a reusable share-apply service plus a
  thin control handler adapter
- split `agent_control.go` into an agent manager plus control-facing glue
- split `port_forward.go` into a forwarding service that depends on a narrow
  guest-connector interface instead of `*ControlServer`

The rule is: if a file still contains `func (s *ControlServer) ...`, it is not
an extraction candidate yet unless `internal/control` is being created in the
same step.

### 3. Split `ControlServer` into components before extracting it

Keep one top-level server type for now, but make it a composition root over smaller concrete services:

- `socketAcceptor`
- `requestRouter`
- `agentBridge`
- `captureService`
- `portForwardService`
- `healthMonitor`

That is the most important structural change in the whole plan. If `ControlServer` stays a god object, every later package move will be cosmetic.

### 4. Narrow wide interfaces now

`proxyRuntime` should split into job-specific seams, for example:

```go
type guestExec interface {
	Exec(context.Context, []string, map[string]string, string) (*pb.ExecResponse, error)
	UserExec(context.Context, []string, map[string]string, string) (*pb.ExecResponse, error)
}

type guestFiles interface {
	ReadFile(context.Context, string) ([]byte, error)
	WriteFile(context.Context, string, []byte, uint32) error
}
```

The same rule applies to GUI-facing surfaces. Packages should not depend on raw window ownership when they only need capture or input.

### 5. Put every background loop on an explicit lifecycle

Long-lived goroutines need a parent context, a cancel path, and a join point. Apply this first to:

- control socket accept loops
- HTTP listeners
- agent health monitoring
- polling loops that currently sleep forever

The current pattern in `agent_control.go:484` should become context-aware and owned by the server lifecycle, not by hidden background state.

### 6. Add test seams at host-OS boundaries

The refactor should introduce tiny seams where the code already crosses into the host:

- IPSW download command execution and filesystem access
- time and sleep in OCR automation
- host version resolution
- subprocess launching for installers and helpers

Prefer `fs.FS`, small function injection, and narrow interfaces over large fakeable abstractions.

## What belongs in `../apple/x/vzkit`

The `vzkit` repository already exists to hold reusable Virtualization.framework
and private-API wrappers. We should use it, but selectively.

The rule is simple:

Move code to `vzkit` when it can be named and documented without mentioning
cove, control sockets, protobufs, VM registry layout, or CLI flags.

Keep code in `vz-macos` when it encodes cove product behavior, cove on-disk
layout, or cove API semantics.

There is already a working precedent for this split:

- `display.go` is a compatibility layer over `vzkit/display`.
- `ocr.go` is a compatibility layer over `vzkit/ocr`.
- `memory.go` delegates balloon operations to `vzkit/balloon`.
- part of `snapshots.go` already delegates VM state snapshots to `vzkit/snapshot`.

That is the migration model to keep using: first extract a reusable package in
`vzkit`, then leave a thin compatibility layer in `vz-macos` until callsites
can be simplified.

### Good candidates for `vzkit`

These are framework-facing helpers that are likely useful to more than one VM
application:

1. VirtioFS builders and live share swapping.
   The low-level work in `shared_folders_runtime.go` is mostly Objective-C and
   Virtualization.framework manipulation. The reusable parts belong in
   `vzkit/virtiofs`:
   - build a `VZMultipleDirectoryShare` from host paths and tags
   - replace a live VirtioFS device's share on a running VM
   - inspect configured directory-sharing devices by tag

   The cove-specific parts stay here:
   - `SharedFolderEntry`
   - loading `shared_folders.json`
   - control-socket command handling and response shaping

2. USB mass-storage configuration and live USB helpers.
   `usb.go` and parts of `control_runtime_usb.go` are mostly reusable device
   construction and controller plumbing. Those should move into either
   `vzkit/storage`, `vzkit/storagehotplug`, or a small new `vzkit/usbstorage`
   package:
   - parse or build disk-image-backed USB storage devices
   - ensure an XHCI controller exists on config
   - create and attach live storage devices to a controller
   - inspect runtime USB controller/device state

   The cove-specific parts stay here:
   - JSON request parsing
   - control API action names
   - protobuf and HTTP response shapes

3. File-handle network attachment and datagram session plumbing.
   `network_filehandle.go` is low-level framework plumbing around
   `VZFileHandleNetworkDeviceAttachment`, connected datagram sockets, and frame
   pump loops. That belongs in `vzkit/network` after the cove-specific config
   and PCAP wiring are peeled off:
   - construct file-handle-backed network attachments
   - own the host/guest socket pair
   - expose frame read/write and pump helpers

   The cove-specific parts stay here:
   - CLI/network mode selection
   - control-surface status and reporting
   - pcap policy and product defaults

4. Restore-image object loading, installer wrappers, and host capability helpers.
   `vzkit/restore` already wraps basic restore-image loading. We should extend
   that package, or add a sibling `vzkit/installer` package, instead of
   growing more wrappers in `vz-macos`:
   - fetch latest supported restore image object or metadata
   - load a restore image from a path
   - wrap `VZMacOSInstaller` lifecycle and `NSProgress` polling behind a Go API
   - expose host-support checks in one place

   The cove-specific parts stay here:
   - cache location and reuse policy
   - `curl`-based download orchestration
   - progress UI and CLI output

5. Framework limit and clamping helpers.
   The hardware-bound helpers around install-time CPU and memory limits should
   not stay wired to CLI globals. Once they take explicit inputs, they are good
   candidates for `vzkit/vm`:
   - clamp requested CPU and memory to framework min/max
   - merge framework limits with restore-image minimums

   The cove-specific parts stay here:
   - choosing defaults from CLI flags
   - printing or logging those choices

6. Continue growing `vzkit/vnc` and `vzkit/debugstub`, not local wrappers.
   Runtime VNC/debug-stub state belongs in cove, but construction and framework
   attachment belong in `vzkit`. Any new framework-level helper in
   `runtime_private.go` should be pushed down into those packages, not added to
   `package main`.

7. `internal/diskimages2`.
   `internal/diskimages2` is already a generic purego wrapper around
   `DiskImages2.framework`. It fits `vzkit` better than the application repo.
   Promote it to a `vzkit/diskimages2` package and keep cove-specific mount or
   provisioning policy here.

### Borderline candidates

Some code is reusable in pieces, but not as-is:

1. AppKit-bound screenshot capture.
   `captureVMView` in `screenshots.go` and `capturePrivateGraphicsDisplay` in
   `screenshots_private_darwin.go` should stay in `vz-macos`. They depend on
   `NSWindow`, `NSView`, private framebuffer-view access, and UI-thread
   ownership. `vzkit/capture` should continue owning image conversion, diffing,
   scaling, and encoding, but not window-bound capture policy.

2. Disk snapshots.
   `DiskSnapshotManager` in `snapshots.go` is not a good direct fit for
   `vzkit` because it hardcodes cove's `vmDir` layout and `disk.img` naming.
   A smaller helper around clonefile-or-copy semantics could move, but the
   manager itself should stay here.

3. Setup Assistant OCR heuristics.
   `screen_detection_ocr.go` is built on reusable OCR primitives, but the page
   names and markers are cove automation policy, not framework plumbing. The
   OCR engine belongs in `vzkit`; these heuristics do not.

4. Version and build info.
   `version.go` should move out of `package main`, but not to `vzkit`.
   It describes the cove binary, not Virtualization.framework behavior.

5. Generic dispatch helpers.
   The raw wrappers in `blocks.go` should not remain permanent API in
   `package main`, but they are also not a good standalone package as-is. The
   preferred end state is to consume `dispatch.Queue` or `vzkit/vm.Queue`
   directly and delete the wrappers, not to create another thin shared package
   with cove-flavored naming.

### What should not move to `vzkit`

These are application concerns and should stay in `vz-macos` even after the
refactor:

- control socket, HTTP, and MCP transport
- authorization, gateway tokens, and operation persistence
- VM registry layout and config JSON for cove's on-disk state
- provisioning flows, guest bootstrap, and Setup Assistant automation
- guest-agent protocol, install, and lifecycle
- CLI flag parsing, status output, and progress rendering
- cove-specific polling policies, retry behavior, and timeouts
- AppKit window ownership, view capture policy, and UI-thread rules

### Extraction rule

Do not move code directly from a god object into `vzkit`.

First split it into a clean local seam inside `vz-macos`. Then, only if the API
still makes sense without cove vocabulary, promote that seam into `vzkit`.

That avoids two common failure modes:

1. exporting cove-specific concepts into a shared package
2. creating a shared package that still depends on `package main` assumptions

## Extraction sequence

### Phase 0: break the `ControlServer` dependency graph

No new directories in this phase. The point is to make later package moves
possible.

Candidate files:

- `control_socket.go`
- `agent_control.go`
- `agent_state.go`
- `agent_inject.go`
- `shared_folders_runtime.go`
- `port_forward.go`
- `gui_control.go`
- `setup_assistant.go`
- `control_http_listener.go`

Deliverables:

1. Consumer-owned interfaces replace direct `*ControlServer`, `*ControlClient`,
   and package-global reach-through in early extraction candidates.
2. Receiver-bound files are split into domain services plus thin control adapters.
3. Long-lived loops are owned by explicit context and wait-group lifecycles.
4. New code stops depending on the global `controlServer`.

Required seams:

- a guest-exec and guest-files surface for agent work
- a vsock or guest-connector interface for forwarding
- a share-applier interface for live VirtioFS updates
- a capture/input surface for GUI-facing automation
- a GUI state sink so window/view code does not store `*ControlServer`
- a setup-assistant transport surface that hides in-process versus socket mode

This phase is mandatory. If it is skipped, the later phases create import
cycles immediately.

### Phase 1: `internal/vmconfig` and `internal/version`

Move the lowest-risk, least-framework code first.

Candidate files:

- `vm_config_codec.go`
- `vm_registry.go`
- `vm_selector.go`
- `volumes.go`
- `version.go`
- shared-folder persistence and path helpers split out of `shared_folders_cli.go`

Deliverables:

1. Config loading, saving, and registry lookups no longer depend on root globals.
2. Path resolution is explicit and reusable.
3. Version negotiation is no longer hidden behind mutable package state.

Why first:

- low AppKit risk
- high testability payoff
- immediate reduction in `package main` surface area

### Phase 2: control operations and persistence

Extract operation state and persistence before extracting the server that uses them.

Candidate files:

- `control_operations.go`
- `control_operations_store.go`

Deliverables:

1. Operation lifecycle uses explicit state-transition methods instead of arbitrary closure mutation under lock.
2. The store has a narrow package API and isolated tests.
3. The control server depends on the operations package instead of owning operation persistence details.

### Phase 3: `internal/agent`

Move host-side guest-agent concerns behind a single package boundary.

Candidate files:

- `agent_client.go`
- `agent_state.go` after `vmDirectory` and platform are explicit inputs
- agent manager code split out of `agent_control.go`
- agent injection code that no longer depends on CLI globals or direct socket checks

Deliverables:

1. Agent connection management and health monitoring are no longer mixed into socket or GUI code.
2. Callers depend on narrow execution and file-access seams, not concrete agent clients.
3. Agent version negotiation and upgrade behavior are testable without booting a full VM.

### Phase 4: `internal/gui`

Extract all AppKit- and capture-heavy code into a package that owns UI-thread constraints.

Candidate files:

- `gui_control.go`
- `ui_thread.go`
- `display.go`
- `window_frame.go`
- `toolbar.go`
- `screenshots.go`
- `screenshots_private_darwin.go`
- `ocr.go`
- `screen_detection*.go`

Deliverables:

1. Raw AppKit types stop leaking into unrelated code, and GUI code no longer
   stores `*ControlServer`.
2. Screenshot capture policy stays local to GUI ownership, while OCR and image-processing helpers continue delegating to `vzkit`.
3. GUI tests can target one package instead of booting the whole server stack.

### Phase 5: `internal/provision`

Move disk-preparation and host-side guest bootstrap code into one domain package.

Candidate files:

- `provision*.go`
- `password.go`
- `setup_assistant.go`
- `autologin_credentials.go`
- `postinstall.go`
- `guest_tools.go`

Deliverables:

1. Provisioning no longer reaches into CLI globals, `*ControlServer`, or
   `*ControlClient` directly.
2. Disk injection code can be exercised against temp directories and small test images.
3. macOS-specific guest bootstrap logic has one owner.

### Phase 6: `internal/runtime`

Extract VM lifecycle ownership after the lower-level seams exist.

Candidate files:

- `runtime_*.go`
- `macos.go`
- `linux.go`
- `memory*.go`
- `snapshots*.go`
- `network*.go`
- `vm_ops.go`
- `vm_state.go`
- runtime-facing service code already split out of `port_forward.go`

Deliverables:

1. One package owns VM lifecycle, not the control server.
2. Runtime code receives explicit config and dependencies instead of reading global state.
3. Wait loops such as `waitForVMStart` become context-aware runtime operations.

### Phase 7: `internal/control`

Only after the dependencies above are real packages should the control surface be extracted.

Candidate files:

- `control_socket*.go`
- `control_http*.go`
- `control_client.go`
- `control_mcp.go`
- `authorization.go`
- `gateway_token.go`
- `serve*.go`
- `proxy.go`
- control adapters split out of `agent_control.go`, `shared_folders_runtime.go`, and `port_forward.go`

Deliverables:

1. `ControlServer` becomes a small assembly type over runtime, agent, GUI, and operations services.
2. The global `controlServer` disappears.
3. Socket, HTTP, and MCP entrypoints share routing and auth without sharing unrelated state.

### Phase 8: `internal/vzscript` and CLI thinning

Move the automation engine last, after its runtime and control dependencies are already explicit.

Candidate files:

- `vzscript.go`
- `vzscript_apply.go`
- `automation_*.go`
- `boot_commands.go`

Deliverables:

1. The script engine depends on an explicit executor surface.
2. Time and polling are injectable for tests.
3. `main.go`, `ctl*.go`, `up.go`, and related CLI files become thin composition code.

## The first PR

If only one refactor PR lands first, it should do four things and nothing more:

1. Add explicit config structs for run/install/serve paths.
2. Introduce consumer-owned interfaces for agent access, share application, guest connection, and capture without changing package layout.
3. Split `agent_control.go`, `shared_folders_runtime.go`, and `port_forward.go` into service code plus thin control adapters.
4. Replace the `agentHealthMonitor` and similar loops with context-owned lifecycle management.

That PR buys the most leverage. It does not create new packages yet, but it removes the cycle-causing `*ControlServer` reach-through that would otherwise block every later extraction.

## Acceptance criteria

The refactor is successful when these conditions are true:

1. The root package is mostly CLI wiring and compatibility shims rather than product logic.
2. No internal package reads root CLI globals.
3. No long-lived goroutine depends on hidden package state for shutdown.
4. AppKit types are owned by `internal/gui`.
5. Agent, runtime, provision, and control code each have one obvious package owner.
6. Unit tests can cover version resolution, OCR polling, IPSW download orchestration, and operation persistence without requiring a live VM or real network.
7. `go build .`, `go build ./...`, and `go test ./...` stay green across every phase.

## Risks and mitigations

### Risk: package cycles appear during extraction

Mitigation: do not move receiver-bound files until they have been split into service code and control adapters. Move shared data types down into lower packages and let consumers own the interfaces they need. Do not solve cycles by creating a catch-all package.

### Risk: control-surface changes leak into behavior changes

Mitigation: extract operations, agent, GUI, and runtime seams before moving `control_socket.go`.

### Risk: UI-thread and AppKit assumptions break during moves

Mitigation: isolate AppKit ownership early in `internal/gui`, and keep all raw handles there.

### Risk: the refactor turns into a months-long branch

Mitigation: phase the work into small PRs with concrete boundaries. Each phase must compile, test, and be releasable by itself.

## Recommendation

Start with package-global configuration removal, consumer-owned interfaces, and `ControlServer` decomposition. Breaking up `package main` is the right end state, but it will fail if receiver-bound files move before the dependencies are narrowed. The compiler can only enforce architecture after the architecture exists.
