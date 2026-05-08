# cove codebase cleanup plan

**Status**: draft; status verified 2026-04-29
**Author**: Codex
**Date**: 2026-04-22
**Scope**: repository-internal cleanup; no intended user-facing feature changes

## Why this doc exists

This plan responds to the design review in `/tmp/vz-macos-design-review.md` and the
current repository shape. Today the module has only a handful of packages, but the
root `package main` still contains **127 non-test Go files** and most of the product
logic. That flat shape is the main problem. It hides ownership, encourages package
global state, and makes server, GUI, provisioning, runtime, and CLI concerns depend
on each other directly.

The cleanup should fix that without a flag day rewrite.

## Status verification (2026-04-29)

This pass reconciles the plan against `origin/main` at `097997d`.

| Area | Current status | Evidence | v0.2 implication |
|---|---|---|---|
| Root package size | Not improved yet | root has 127 non-test Go files and 115 test files; `control_socket.go` is 1,981 lines. | Keep root-package shrink as a later outcome, not an immediate v0.2 gate. |
| `internal/control/operations` | Done | `internal/control/operations/` owns the registry, file store, tests, explicit `Start`, `SetProgress`, `Succeed`, and `Fail` transitions. Root `control_operations.go` is now a thin `ControlServer` adapter. | Do not redo operations extraction in v0.2; build on the package that exists. |
| `internal/vmconfig` | Partial | `internal/vmconfig/` owns config, registry, paths, migration, detection, and info helpers. Root `vm_selector.go`, `shared_folders_*`, and `volumes.go` still carry VM config behavior and globals. | v0.2 phase 1 should finish shared folders, volumes, and selector seams rather than start from config codec. |
| `internal/agent` | Partial | `internal/agent/` currently owns persisted agent state only. `agent_client.go`, `agent_control.go`, and `agent_inject.go` remain in root and still hang off `ControlServer`. | v0.2 phase 2 remains open; extract client/bootstrap/upgrade behind a small service API. |
| Control server decomposition | Not started in package terms | `ControlServer` still has 129 methods across root files and the request edge still dispatches raw `req.Type` strings. | Phase 3 stays v0.3; v0.2 should only prepare seams needed by vmconfig/agent extraction. |
| Lifecycle plumbing | Partial | server stop/join and agent health lifecycle work landed, but ownership is still rooted in `ControlServer`. | Preserve and reuse existing context/stop paths during extraction. |

Conclusion: the roadmap's v0.2 "ControlServer decomposition — phases 1+2" should mean **finish `internal/vmconfig` and extract `internal/agent`**, while treating `internal/control/operations` as already shipped. Phase 3 (`internal/control`) remains a separate v0.3 item.

## Current problems to address

1. `package main` is carrying nearly all application logic, so boundaries are implicit
   instead of enforced by the compiler.
2. `ControlServer` owns too many responsibilities: socket accept loop, auth, request
   routing, agent lifecycle, GUI access, OCR, screenshots, HTTP listeners, and port
   forwards.
3. Core logic reads CLI/package globals such as `vmDir`, `linuxMode`, `verbose`,
   `cpuCount`, and `memoryGB`, which couples runtime behavior to argument parsing.
4. Several interfaces are too wide, notably `proxyRuntime` and `VMGUIController`.
5. Long-lived goroutines are not consistently tied to explicit lifecycle management.
6. File system, process execution, networking, and AppKit dependencies are mixed into
   logic that should be testable in isolation.

## Goals

1. Reduce the root `main` package to thin CLI composition and compatibility shims.
2. Move product logic into a small set of explicit `internal/` packages with clear
   ownership.
3. Remove package-global configuration from core code paths.
4. Turn server and GUI lifecycles into explicit context-managed components.
5. Narrow interfaces until each one represents one job.
6. Preserve behavior and wire formats while the cleanup lands incrementally.
7. Keep `go build .`, `go build ./...`, and `go test ./...` working throughout.

## Non-goals

1. No CLI redesign as part of this cleanup.
2. No control-socket or HTTP API redesign unless needed for internal separation.
3. No attempt to turn this repo into a general-purpose public library.
4. No broad rename churn that does not improve ownership or coupling.

## Design principles

1. Use `internal/` packages, not public packages. This repo is an application.
2. Split by domain ownership, not by technical flavor. Avoid packages like `util`,
   `common`, or `helpers`.
3. Prefer concrete types and narrow interfaces. Add interfaces only where a second
   implementation or a real test seam exists.
4. Keep wire compatibility at the edges while changing internals behind adapters.
5. Land the cleanup in small phases. Every phase must compile, test, and be shippable.

## Target package shape

The end state should look roughly like this:

```text
.
├── main.go                  # thin CLI entrypoint; keep go build . working
├── cli_help.go              # thin command wiring only
├── cmd/vz-agent             # guest agent stays separate
├── internal/agent           # guest agent clients, bootstrap, agent state
├── internal/control         # control socket, HTTP bridge, auth, operations, routing
├── internal/gui             # AppKit windowing, screenshots, OCR, UI thread glue
├── internal/provision       # disk injection, setup assistant bypass, passwords, templates
├── internal/runtime         # VM runtime lifecycle, state, memory, snapshots, devices
├── internal/vmconfig        # config codec, registry, selector, shared folders, volumes
├── internal/vzscript        # script engine, apply logic, automation script wiring
├── internal/gateway         # serve/gateway, proxy, authorization, gateway tokens
└── internal/assets|autosign|diskimages2
```

This is intentionally not a maximal split. The target is a small number of strong
packages with obvious ownership.

## Dependency rules

These rules matter more than the directory names:

1. `main` may depend on any internal package; internal packages may not depend on `main`.
2. `internal/runtime` may not read CLI globals. It receives explicit config and paths.
3. `internal/gui` owns raw AppKit and Objective-C handles. Those types do not cross
   back into unrelated packages.
4. `internal/control` depends on `internal/agent`, `internal/runtime`, `internal/gui`,
   and `proto/*`, but those packages do not depend on `internal/control`.
5. `internal/vmconfig` is pure data/config ownership. It does not depend on GUI,
   control, or AppKit.
6. `internal/provision` and `internal/vzscript` consume `internal/vmconfig` and
   `internal/agent` APIs but do not reach into package globals.
7. If two packages want to call each other, the boundary is wrong. Move the shared
   data type down or introduce a smaller interface owned by the consumer.

## First cleanup before file moves

Do these changes while code is still in place. They reduce risk before package
extraction.

### 1. Replace global configuration reads with explicit inputs

Introduce small option structs at the seams that currently pull from globals:

- `RunConfig`
- `InstallConfig`
- `ServeConfig`
- `VMPaths`

Examples of code that should stop reading globals directly:

- runtime code reading `cpuCount`, `memoryGB`, `vmDir`
- platform branching on `linuxMode`
- service behavior depending on `verbose`

The rule is simple: parsing flags is a CLI concern; acting on them is not.

### 2. Split `ControlServer` by responsibility before moving it

Keep one top-level server type for now, but turn it into a composition root around
smaller components:

- `socketAcceptor`
- `requestRouter`
- `agentBridge`
- `captureService`
- `portForwardService`
- `healthMonitor`

The goal is to make the later package move mechanical instead of conceptual.

### 3. Replace callback mutation in operations with explicit transitions

`OperationRegistry.Update(id, func(*Operation))` should become explicit methods such
as:

- `Start`
- `SetProgress`
- `Succeed`
- `Fail`

That makes legal state transitions visible and keeps arbitrary caller logic out of
the registry lock path.

### 4. Introduce lifecycle management everywhere a goroutine outlives a call

Every background loop should have:

- a parent `context.Context`
- a `cancel` path
- a `sync.WaitGroup` or equivalent join point

Apply this first to:

- control socket accept loop
- agent health monitoring
- any background HTTP listener/watcher

### 5. Narrow wide interfaces now

Replace coarse interfaces with job-specific ones. For example:

- `proxyRuntime` should split into runtime execution and guest file access roles
- `VMGUIController` should stop returning raw window and toolbar types across package
  boundaries

If a consumer needs only `Exec`, do not force it to also depend on `WriteFile`.

### 6. Add test seams at OS and process boundaries

Introduce small seams where the review already found pain:

- IPSW download and existence checks
- VM directory filesystem access
- GUI/install presentation
- command execution for helper tools

Prefer `fs.FS`, function injection, and tiny concrete adapters over large mock
interfaces.

## Extraction plan

### Phase 1: `internal/vmconfig` and `internal/control/operations`

Move the lowest-risk, least-AppKit code first.

Candidate files:

- `vm_config_codec.go`
- `vm_registry.go`
- `vm_selector.go`
- `shared_folders_cli.go`
- `shared_folders_runtime.go`
- `volumes.go`
- `control_operations.go`
- `control_operations_store.go`

Deliverables:

1. Config loading/saving no longer depends on root globals.
2. Operation persistence and registry are isolated behind typed APIs.
3. Existing callers use imported packages instead of same-package reach-through.

Why first:

- low framework coupling
- high payoff for testability
- minimal risk to GUI/runtime code

### Phase 2: `internal/agent`

Candidate files:

- `agent_client.go`
- `agent_control.go`
- `agent_inject.go`
- `agent_state.go`
- agent-related helpers that are currently mixed into control/runtime code

Deliverables:

1. One package owns guest-agent dialing, bootstrap, verification, and upgrade logic.
2. Control and provisioning consume agent services instead of open-coding them.
3. Guest agent platform detection uses injectable filesystem access where needed.

### Phase 3: `internal/control`

Candidate files:

- `control_socket.go`
- `control_socket_commands.go`
- `control_socket_ocr.go`
- `control_http.go`
- `control_http_listener.go`
- `control_client.go`
- `control_runtime_*.go`
- `control_mcp.go`
- auth/token helpers currently embedded in socket files

Deliverables:

1. `ControlServer` becomes `control.Server` composed from smaller private types.
2. Request parsing/routing is typed. Raw string switching is limited to a parser
   layer at the edge.
3. Server lifecycle is explicit and testable.
4. Socket, HTTP, and MCP entrypoints share the same internal command layer.

Important constraint:

Keep the current control socket and HTTP behavior stable while internals move.

### Phase 4: `internal/gui`

Candidate files:

- `gui_control.go`
- `toolbar.go`
- `status_item.go`
- `mainmenu.go`
- `window_frame.go`
- `screenshots.go`
- `screenshots_private_darwin.go`
- `screen_detection.go`
- `screen_detection_ocr.go`
- `ocr.go`
- `clipboard.go`
- `appkit_compat.go`
- `ui_thread.go`

Deliverables:

1. All raw AppKit and Objective-C ownership is contained in one package.
2. Other packages consume small GUI/capture abstractions, not `uintptr` handles.
3. Screenshot, OCR, and input code become testable at the service boundary.

Important constraint:

Do not leak `appkit.NSWindow`, toolbar internals, or raw Objective-C IDs into
unrelated packages.

### Phase 5: `internal/runtime` and `internal/provision`

Candidate runtime files:

- `macos.go`
- `linux.go`
- `runtime_lifecycle.go`
- `runtime_actions.go`
- `runtime_private.go`
- `vm_state.go`
- `memory.go`
- `memory_limits.go`
- `snapshots.go`
- `display.go`
- `networking.go`
- `usb.go`
- `port_forward.go`

Candidate provision files:

- `installer.go`
- `linux_installer.go`
- `provision.go`
- `provision_*.go`
- `unattended.go`
- `setup_assistant.go`
- `password.go`
- template/render helpers tied to provisioning

Deliverables:

1. VM runtime code is driven by explicit configuration, not global flags.
2. Provisioning code owns disk injection and first-boot setup end to end.
3. Platform-specific behavior is expressed through small internal seams instead of
   package-global mode flags.

### Phase 6: `internal/vzscript` and `internal/gateway`

Candidate `vzscript` files:

- `vzscript.go`
- `vzscript_apply.go`
- `boot_commands.go`
- `automation_script.go`
- `automation_backend.go`
- script/setup helpers

Candidate gateway files:

- `serve.go`
- `serve_gateway.go`
- `authorization.go`
- `gateway_token.go`
- `proxy.go`
- `pcap.go`

Deliverables:

1. Script execution stops depending on same-package access to control/runtime internals.
2. Serve/gateway code becomes a bounded HTTP-facing package instead of a repo-wide
   utility layer.

### Phase 7: shrink root `main`

After the domain packages exist, reduce root files to:

- CLI entrypoints
- flag parsing
- command wiring
- compatibility shims that can be deleted after callers move

Target end state:

1. `package main` is small enough to understand by reading only command setup.
2. Core behavior lives in internal packages with owned tests.
3. New code no longer lands in the root by default.

## Specific design corrections required during cleanup

These are not optional; they are the point of the cleanup.

### Control request routing

Replace the giant request switch with a parser plus typed handlers:

```go
type Command interface {
	Apply(context.Context, *Server) (*controlpb.ControlResponse, error)
}
```

The edge may still parse legacy `req.Type` strings, but the rest of the server
should not route on ad hoc string constants.

### Operation state machine

Define allowed states and transitions in one place. Do not let random callers mutate
`Operation` structs through callbacks.

### AppKit ownership

Wrap unsafe Objective-C/AppKit handles in package-private types with explicit lifetime
rules. Silent no-op behavior on invalid handles should become explicit errors where
possible.

### Background worker shutdown

No new long-lived goroutine should be added without:

1. a documented owner
2. a stop path
3. a test that proves shutdown does not hang

## Migration mechanics

1. Move one domain at a time.
2. Use temporary adapters and forwarding functions freely.
3. Do not move files and redesign behavior in the same patch unless the behavior
   change is the reason for the move.
4. Prefer adding package tests before deleting same-package test helpers.
5. Keep public filenames or wrapper functions temporarily if that avoids broad churn.

This cleanup should be a sequence of boring patches, not one dramatic branch.

## Quality gates for every phase

Each phase must:

1. pass `go build ./...`
2. pass `go test ./...`
3. preserve current CLI and control-socket behavior
4. add tests for any new seam introduced during the phase
5. avoid new package globals unless they are immutable constants

## Exit criteria

The cleanup is done when all of the following are true:

1. root `package main` is a thin CLI layer, not the application implementation
2. `ControlServer` is no longer a god object and is assembled from owned components
3. runtime/provision/control/gui code no longer read CLI globals directly
4. background server components have explicit shutdown
5. core packages can be tested without real AppKit, real network, or real disk when
   the logic does not require them
6. new feature work has an obvious package destination other than repo root

## Recommended order of landing

1. global-config cleanup and lifecycle plumbing in place
2. `internal/vmconfig`
3. operations package
4. `internal/agent`
5. `internal/control`
6. `internal/gui`
7. `internal/runtime`
8. `internal/provision`
9. `internal/vzscript`
10. `internal/gateway`
11. final root-package shrink

That order minimizes the risk of cyclic dependencies and keeps the highest-churn
systems moving only after their data and lifecycle seams are already explicit.
