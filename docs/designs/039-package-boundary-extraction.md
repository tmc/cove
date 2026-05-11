# Design 039: Package Boundary Extraction

Status: Shipped through the 2026-05-07 package-boundary arc. `internal/vmrun`
shipped at `3d876b8`, with macOS/Linux/Windows callers routed through it at
`c5dcfec`, `ed05f50`, and `23c25d1`. All five ControlServer sub-bridges
(Capture, Lifecycle, Agent, Input, Network) now live in
`internal/controlserver/`; the final bridge move landed at `8bd7a65`.
Verified 2026-05-11 (R108): cited SHAs are reachable from `origin/main`; bridge files
`internal/controlserver/{capture,lifecycle,agent,input,network}.go`
all present; `internal/vmrun/` package present.
Date: 2026-05-06

## Problem

The root package is still the product's gravity well. On the reviewed branch,
the repository has 395 root-level Go files in package `main`. That package owns
CLI parsing, VM install and run flows, the control socket server, GUI/AppKit
automation, provisioning, images, networking, fleet, vzscript, and most tests.

The problem is not just file count. The problem is that unrelated decisions
share package globals, package-private helpers, and one large concurrency
object. The next round should make the existing product easier to review and
change without changing user-visible behavior.

This design is an extraction plan. It is not an implementation.

## 1. Census

The census was generated from the 395 root-level `.go` files and then spot
checked by reading the largest files plus smaller representatives. The grouping
is approximate; it is meant to show pressure points, not define final packages.

| Theme | Files | Lines | Representative files |
|---|---:|---:|---|
| CLI, images, and local store | 83 | 24,461 | `main.go`, `up.go`, `build.go`, `image.go`, `image_registry.go` |
| VM lifecycle and runtime devices | 55 | 17,778 | `macos.go`, `linux.go`, `windows.go`, `runtime_lifecycle.go`, `block_device.go` |
| Control socket, ctl, and agent bridge | 44 | 14,361 | `control_socket.go`, `ctl.go`, `control_client.go`, `agent_control.go`, `control_operations.go` |
| Install, provisioning, and recovery | 25 | 11,699 | `installer.go`, `linux_installer.go`, `agent_inject.go`, `provision.go`, `setup_assistant.go` |
| Tests and live probes | 57 | 9,143 | `integration_test.go`, `serve_test.go`, `private_api_live_test.go`, `shared_folders_cli_test.go` |
| Miscellaneous support | 48 | 9,096 | `phase0_interfaces.go`, `status.go`, `shell.go`, `logs.go`, `utils.go` |
| GUI, OCR, and automation | 34 | 8,131 | `vm_selector.go`, `screen_detection.go`, `automation_script.go`, `toolbar.go`, `window_frame.go` |
| Networking, proxy, serve, and fleet | 25 | 7,431 | `proxy.go`, `port_forward.go`, `serve_gateway.go`, `network_logs.go`, `fleet_run.go` |
| VZScript engine and recipes | 5 | 4,223 | `vzscript.go`, `vzscript_apply.go`, `vzscript_template.go` |
| Host integration and helpers | 14 | 3,846 | `helper.go`, `authorization.go`, `elevated_exec.go`, `doctor_tcc.go` |
| Shared folders and volumes | 5 | 960 | `shared_folders_config.go`, `shared_folders_runtime.go`, `volumes.go` |

The largest root files by line count are:

| File | Lines | Function count | Primary responsibility |
|---|---:|---:|---|
| `macos.go` | 2,754 | 76 | macOS VM configuration, runtime startup, device wiring, suspend/resume, GUI/headless run behavior |
| `ctl.go` | 2,594 | 61 | `cove ctl` parsing, request construction, response formatting, UI automation subcommands |
| `vm_selector.go` | 2,231 | 71 | AppKit VM selector, new-VM wizard, recipe selector, selector-driven global state mutation |
| `vzscript.go` | 2,141 | 68 | recipe loading, execution orchestration, host/guest command handling |
| `installer.go` | 1,994 | 60 | macOS install flow, restore image handling, disk setup |
| `control_socket.go` | 1,982 | 78 | control socket server, VM state, agent state, screenshot/input routing, operation dispatch |
| `main.go` | 1,892 | 21 | global flags, startup environment, top-level command routing |
| `linux_installer.go` | 1,793 | 58 | Linux install/autoinstall, image and cloud-init preparation |
| `agent_control.go` | 1,481 | 48 | agent connection, health, exec/copy/mount routing |
| `proxy.go` | 1,073 | 51 | proxy setup, listeners, forwarding helpers |

The existing `internal/agent/routing.go` is the counterexample worth copying.
It is small, owns one policy, exports narrow functions, and documents why the
daemon/user-agent split exists. It is also already internal.

## 2. Hot Zones

### `main.go`

`main.go` owns two kinds of global state:

1. Runtime knobs: OS mode, GUI/headless mode, Linux distro, nested/NVMe/shell
   flags, CPU, memory, disk paths, network mode, sandbox level, volumes,
   displays, Rosetta, clipboard, recovery, runtime profile, save options, VNC,
   GDB, HTTP, and startup port forwards.
2. Command and provisioning state: `vmName`, `vmDir`, install/run legacy flags,
   clone/disposable/fork mode, provisioning user/password/admin/strategy,
   unattended install state, automation backends, auto-mount and auto-upgrade,
   force install, skip resume, and VM identity recovery.

Those globals are declared in one block at `main.go:32-183`. Flag registration
starts immediately after at `main.go:185`, with roughly 94 `flag.*Var` calls.
The startup path then performs several unrelated jobs before dispatch:

- parses global flags and legacy aliases;
- configures logging, pprof, terminal prompts, and UI thread state;
- infers Linux and desktop modes from flags;
- resolves `vmDir` and saved VM config;
- applies sandbox defaults and launch validation;
- handles fleet and early commands;
- dispatches about 55 top-level command cases.

The file should become a 50-line entry point, but not in the first slice. A thin
`cmd/cove/main.go` is only honest after there is a command registry and a small
environment object that can carry VM selection, host capabilities, stdio,
logging, and flag values explicitly.

First target: make `main()` delegate to `covecli.Main(os.Args[1:])`. Until that
exists, moving the file to `cmd/cove` only moves the monolith.

### `control_socket.go`

`ControlServer` is the runtime center of the product. Its struct at
`control_socket.go:46-94` includes:

- socket path, auth token, listener, and generated control server;
- VM view, AppKit window, `VZVirtualMachine`, and dispatch queue;
- daemon and user-agent clients;
- OCR service, screenshot cache, capture/input modes, and window geometry;
- iTerm2 proxy, port forwards, HTTP listeners, VNC and debug-stub status;
- agent health state and GUI session state;
- lifecycle context, policy counters, and operation registry.

The explicit locks are useful but show the breadth of the object:

| Lock | Protects |
|---|---|
| `mu` | broad VM/view/window/queue/runtime state, plus assorted fields used across handlers |
| `agentMu` | daemon/user-agent connection setup and reconnect state |
| `screenshotMu` | `lastScreenshot`, capture dimensions, and diff/click mapping state |
| `healthMu` | proactive agent health state and GUI session facts |
| `windowTitleMu` | title base/state/label updates |
| `policyMu` | policy start time, exec count, and stop-issued state |
| `lifecycleMu` | lifecycle context and cancellation |
| `opsMu` | lazy operation registry initialization |

The natural seam is not "move ControlServer into internal/control" in one pass.
That would import AppKit, Virtualization.framework, agent clients, OCR, port
forwarding, HTTP serving, policy, and operation storage into a new package all
at once. The better seam is to keep `ControlServer` as a facade and first
extract components whose invariants are smaller:

- agent bridge: agent clients, routing, health checks, upgrade;
- capture/input: screenshots, OCR, mouse/key/text dispatch;
- lifecycle: pause/resume/stop, policy ticker, status transitions;
- network: port forwards, HTTP listeners, VNC/debug status;
- operations: file-backed operation registry access.

`phase0_interfaces.go` already shows the intended direction: tiny interfaces
such as `guestPortConnector`, `runtimeAgentAvailabilityTarget`, and
`vmSelection` adapt the monolith into narrower contracts. That file is still in
package `main`, but it is a good staging pattern.

### `macos.go`

`macos.go` is the largest file and is more coupled than its name suggests. It
builds VM configurations, attaches devices, starts or restores VMs, manages
GUI/headless run behavior, coordinates suspend state, handles launch-order
experiments, and reads many package globals from `main.go`.

The first extraction should not be AppKit or ObjC callback code. The first
extraction should be data:

- `RunConfig`: normalized user intent from flags and saved VM config;
- `HostConfig`: host paths, entitlement-relevant helpers, and logging knobs;
- `DevicePlan`: disks, network, display, VirtioFS, Rosetta, clipboard, block
  devices, VNC/debug, and serial output.

Only after the run configuration is explicit should runtime methods move into
`internal/vmrun`. Otherwise the package boundary will just inherit the current
globals as a larger constructor.

### `ctl.go`

`ctl.go` is large, but it is a better early extraction candidate than
`macos.go`. Most of it is CLI parsing, control request construction, and
response formatting. It already talks to the runtime through the control socket
instead of directly owning VM objects.

The seam is `internal/controlclient`: a package that owns connection setup,
request construction, response formatting, and scriptable command helpers for
`cove ctl`. The tricky parts are the UI automation helpers and recovery/setup
assistant commands, which pull OCR and guest-specific policy into the same file.
Move the pure client and formatting layer first; leave UI automation in package
`main` until the capture/input boundary is smaller.

## 3. Extraction Sketch

The target shape should use internal packages for product internals and keep the
external binary surface unchanged.

| Package | Purpose | Approximate move | API surface | Hardest cycle |
|---|---|---:|---|---|
| `cmd/cove` | Binary entry point only | 1 new file, final `main.go` under 80 LOC | `main` calls `covecli.Main` | blocked until global flag state is represented by an explicit environment |
| `internal/covecli` | Top-level command registry, global flag normalization, usage text, command environment | 8-15 files over time | `Main(args []string) int`, `Run(ctx, env, args) error`, `Command` table | every command currently reads package globals and calls package-main helpers |
| `internal/controlclient` | Control socket client, request builders, response formatting for `cove ctl` | 8-12 files, mostly from `ctl.go` and `control_client.go` | `Client`, `Dial`, typed request helpers, format functions | setup-assist/OCR helpers pull in GUI and guest policy |
| `internal/provision` | Provisioning intent, staging paths, agent injection, password/reset flow helpers | 8-14 files | `Plan`, `Apply`, `InjectAgent`, small interfaces for auth and disk attach | uses `vmSelection`, global `vmDir/vmName`, helper auth, setup assistant, and guest OS branches |
| `internal/vmrun` | VM run/install configuration and pure validation/device planning | 10-18 files over several slices | `RunConfig`, `DevicePlan`, `Validate`, `PlanDevices` | `macos.go` mixes pure config, AppKit window setup, VZ object creation, and suspend/resume behavior |
| `internal/controlserver` | Control socket facade and smaller runtime components | late move, probably 15-25 files after subcomponents exist | `Server`, `Runtime`, `Agents`, `Capture`, `Lifecycle` | AppKit, VZ, OCR, agent state, network listeners, and policy currently meet in one struct |
| `internal/vzscript` | Recipe metadata, rendering, host/guest command execution policy | 4-6 files | `Recipe`, `Load`, `Run`, `List` | terminal/GUI helpers and control-client execution need to move first |
| `internal/covegui` | AppKit selector, VM window/controller helpers, GUI-only actions | late move, 10-20 files | small controller constructors and callbacks | main-thread locking, purego callback lifetime, selector mutation of package globals |

The names are intentionally specific. Avoid `pkg/`, `utils`, `helpers`, and a
generic `internal/vm`. `internal/vmrun` is narrower than `internal/vm`;
`internal/controlclient` is clearer than `internal/ctl`; `internal/covecli`
keeps command registration separate from runtime behavior.

The first implementation should also preserve the existing root package tests.
Tests can move only when the corresponding package has a real boundary.

## 4. Slice Plan

### Slice 1: Command registry inside package `main`

Cost: 1-2 days, roughly 400-700 LOC touched.

Add a table of top-level command specs in package `main`: name, aliases, help
summary, early/late dispatch class, and run function. Replace the long command
switches with table lookup while leaving all behavior and globals in place.

This is reversible because it changes dispatch shape only. It unblocks help
tests and makes later `internal/covecli` extraction mechanical.

Tests: existing CLI help tests, `go test ./...`, and a small table-driven test
for alias resolution and unknown-command suggestions.

### Slice 2: Explicit command environment

Cost: 2-3 days, roughly 600-1,000 LOC touched.

Introduce `type commandEnv struct` in package `main` with stdio, logger,
current VM selection, global options, and host capability hooks. Populate it
after flag parsing. Convert low-risk commands first: `version`, `list`, `logs`,
`runs`, `policy`, `image`, and other commands that do not start a VM.

This is the slice that decides whether `cmd/cove` can become thin. It is
reversible because each command can fall back to reading globals until converted.

Tests: command-env construction tests plus existing command tests.

### Slice 3: Extract `internal/controlclient`

Cost: 2-3 days, roughly 1,200-1,800 LOC moved or adjusted.

Move the pure control socket client, request builders, and output formatting out
of `ctl.go` and `control_client.go`. Leave setup-assist, OCR click helpers, and
GUI-specific recovery in package `main` until capture/input packages exist.

This slice is high value because `ctl` is a large user-facing surface that
already depends on a protocol boundary. It should not need VM runtime objects.

Tests: control-client request/format tests, existing `ctl` tests, and at least
one script-style CLI test around refusal/error output.

### Slice 4: Extract provisioning policy

Cost: 2-3 days, roughly 1,500-2,500 LOC moved or adjusted.

Create `internal/provision` around explicit values: VM selection, user/password
policy, admin policy, agent-injection options, and staging paths. Move pure
path/config helpers first; keep host authorization and setup-assistant glue at
the package-main edge until interfaces are narrow.

This slice should retire more global reads than it moves files. A package that
still reads `vmDir` and `vmName` through globals is not an extraction.

Tests: provisioning path tests, agent-injection unit tests, no live VM required
for the first version.

### Slice 5: Introduce `internal/vmrun` configuration

Cost: 2-3 days, roughly 1,200-2,200 LOC touched.

Define `RunConfig`, `HostConfig`, and `DevicePlan`. Convert `macos.go`,
`linux.go`, and `windows.go` entry points to accept these values instead of
reading globals directly. Move pure validation and planning helpers into
`internal/vmrun`; leave AppKit and VZ object creation in package `main` until
the data boundary holds.

This is a staging slice, not a move-everything slice. It reduces global coupling
before package movement.

Tests: config normalization table tests and existing install/run tests. Build
and sign the binary before any live VM smoke.

### Slice 6: Split ControlServer components

Cost: 3 days per component; likely multiple sub-slices.

Keep `ControlServer` as the facade. Extract one subcomponent at a time:

1. agent bridge and health;
2. capture/screenshot/OCR state;
3. input dispatch;
4. lifecycle and policy stop checks;
5. port-forward/HTTP/VNC/debug status.

Only after these components have local invariants should the facade move to
`internal/controlserver`. This should probably start in v0.6 unless v0.5 has
time after the earlier slices land.

Tests: targeted unit tests for each component plus live smoke only for the
component being moved.

## 5. Risks and Non-Goals

Non-goals:

- no CLI surface redesign;
- no control socket protocol redesign;
- no GUI redesign;
- no new features or removed flags;
- no package movement that requires users to change commands;
- no broad test relocation before package boundaries exist.

Risks:

- AppKit and purego callback lifetime may make GUI moves fragile even when the
  Go package name technically does not matter.
- `runtime.LockOSThread` and UI-thread dispatch are easy to preserve in a
  small entry point but easy to break during a large move.
- Import cycles are likely if `ControlServer` moves before agent, capture,
  lifecycle, network, and operation storage are split.
- Global state removal will expose behavior that currently depends on mutation
  order, especially in selector, install, and fork flows.
- Re-signing remains a runtime gate. Refactors that build but are not signed
  are not verified for Virtualization.framework behavior.
- Root tests are broad and valuable, but many are integration-style. Moving
  packages will require smaller unit tests before files move.

## 6. Recommendation

Do not promise "package main fixed" for v0.5. That is too big and would invite a
large risky move.

Do include the first three slices in v0.5 if the release has room:

1. command registry inside package `main`;
2. explicit command environment;
3. `internal/controlclient`.

Those slices are tractable, reviewable, and reduce the amount of new product
surface that lands directly in package `main`. Slice 4 (`internal/provision`)
is the stretch goal for v0.5 because provisioning has been the source of real
operator pain. Slices 5 and 6 later shipped as part of the same 2026-05-07
package-boundary arc; this recommendation is retained as design history.

The standard for each slice should be boring: one concern, tests passing, no
behavioral diff, and a revert that does not strand later work.

## 7. Go-Team Self-Review

This section applies the go-team-codebase-review and go-team-history-audit
lenses to the proposal itself.

### Russ Cox critique

The first draft wanted `internal/vm`, `internal/gui`, and
`internal/control` as broad buckets. That failed the one-concern test. The plan
above narrows those to `internal/vmrun`, `internal/covegui`,
`internal/controlclient`, and a late `internal/controlserver`.

Russ would also object to moving files before removing globals. That critique
changed the slice order. The first two slices now keep code in package `main`
and make command state explicit before any `cmd/cove` move.

### Bryan Mills critique

The original temptation was to split `ControlServer` early because it is the
most obvious god object. That maximizes churn. The revised plan treats
`ControlServer` as a facade and extracts one concurrency component at a time.
The important unit is an invariant, not a file.

The plan also keeps tests near the existing root package until there is a real
package boundary. Moving tests early would make failures harder to interpret.

### Ian Lance Taylor critique

Package names must carry ownership. `internal/covecli` is acceptable because it
is the product command layer. `internal/controlclient` is clearer than `ctl`
because the package speaks the control socket protocol. `internal/vmrun` is
better than `vm` because it excludes images, provisioning, config storage, and
fleet.

The hostile version of this critique is that `covecli` may still become a new
monolith. The guardrail is that it may own command registration and environment
normalization only. Runtime behavior belongs in domain packages.

### History-audit critique

Recent history shows product surfaces landing in clusters: coved observability,
Cirrus migration, image registry, network policy, runs, action cache, and agent
sandbox. Each individual commit may be atomic, but the combined effect has been
more surface area in the root package.

This proposal therefore avoids a heroic cleanup commit. It recommends reviewable
refactor slices that can land between feature work. The first slice is a command
registry because it makes future product additions pay a small boundary cost.

### Productized tradeoff

Cove is not the standard library. Purego AppKit/VZ integration, protobuf over
vsock, OCR automation, and GUI control are product requirements. A plan that
tries to make those disappear behind tiny pure-Go packages would be cargo-cult
minimalism.

The useful Go-team rule is narrower: product edges may be weird, but the
contracts between them should be boring. That is why the plan leaves AppKit
movement late and starts with explicit data, command registration, and protocol
client extraction.

### Self-review headline

The plan is intentionally less ambitious than "split package main." A hostile
reviewer would say it leaves the hardest object, `ControlServer`, for later.
That is true. The revision is to make v0.5 own the prerequisites that make the
hard split possible: command registry, explicit environment, and control-client
extraction. Moving the hardest object first would maximize churn and hide
behavior changes inside an architectural win.
