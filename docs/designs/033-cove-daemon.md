# Design 033: Cove Daemon Mode

**Status:** Slice 1 scaffold  
**Author:** Travis Cline  
**Date:** 2026-05-05

## Problem

Cove is currently command-driven. Each CLI process discovers VM state, applies
policy, starts or controls one VM, emits run metrics, and exits. That is simple
and remains the right fallback, but several roadmap items need a long-lived
coordinator:

- VM lifecycle policy enforcement, including the policy work tracked by #247.
- Scheduled local image GC that emits the metrics added by T57.
- Detection of networking configuration drift across VMs and runs.

Those jobs should not be reimplemented independently by every CLI command. They
need one local authority that can observe host state over time while preserving
the current direct CLI path for hosts that do not install a daemon.

## Decision

Add `coved`, a long-lived background coordinator for a single user-owned cove
installation. Slice 1 only establishes the contract:

- A daemon binary at `cmd/coved`.
- A Unix socket at `~/.vz/cove.sock`.
- A PID file at `~/.vz/cove.pid`.
- `STATUS` over the socket returns JSON:

  ```json
  {"version":"...","uptime_s":12,"vms_managed":0}
  ```

The daemon is a parent/coordinator for host-level policy and scheduling. It is
not a replacement for per-VM control sockets. VM control sockets remain the
runtime interface for a specific booted VM: screenshots, agent commands,
forwards, and guest operations still route through `~/.vz/vms/<name>/control.sock`
or the active VM's equivalent path. `coved` may discover and supervise those
sockets later, but it should not proxy every VM command in Slice 1.

## Launch Model

The default installation target is a user-session launchd service, not a system
LaunchDaemon. The socket, PID file, image store, VM store, and metrics directory
all live under the invoking user's `~/.vz`. A system LaunchDaemon has the wrong
ownership model for private user VM state unless a later admin-managed mode
explicitly maps users to stores.

Slice 1 still ships a plist template named
`internal/coved/com.cove.daemon.plist.tmpl`. The CLI installs a rendered copy
under `~/Library/LaunchAgents/com.cove.daemon.plist` and uses:

```text
launchctl load ~/Library/LaunchAgents/com.cove.daemon.plist
launchctl unload ~/Library/LaunchAgents/com.cove.daemon.plist
```

The template and CLI are intentionally conservative. If a future root-managed
mode is needed, it should use a different label and paths that make ownership
and entitlement requirements explicit.

No additional launchd entitlement is expected for this slice. `coved` listens on
a user-owned Unix socket and does not open Virtualization.framework resources.
The normal `cove` binary still needs the virtualization entitlement after build;
`coved` does not.

## Responsibilities

### VM Lifecycle Policy

`coved` should eventually own host-level lifecycle rules:

- Refuse conflicting starts when a policy says only one VM in a group may run.
- Enforce idle shutdown or suspend policies.
- Coordinate policy with #247 so CLI commands can ask the daemon for a decision
  before mutating VM state.

The first implementation should keep the policy surface read-only: `STATUS`
reports `vms_managed`, and later slices can add commands such as
`REGISTER_VM`, `EVALUATE_START`, or `LIST_POLICIES`.

### Image GC Scheduling

Image GC already has local reachability checks and emits metric events for keep
and evict decisions. `coved` should eventually run scheduled dry-run planning
and optional configured sweeps. It should reuse existing `image gc` code paths
rather than deleting image directories directly, so T57 metric behavior remains
the single source of truth.

The daemon should write scheduler decisions to the same run metrics root or a
daemon metrics stream under `~/.vz/runs`. That choice is deferred; Slice 1 only
creates the daemon process and status surface.

### Network Drift Detection

Cove now has named network policies and per-run audit logs. A daemon can detect
drift that a one-shot CLI process cannot:

- VM config defaults that disagree with the image manifest's default network.
- Running VMs whose effective network mode differs from policy.
- Port forwards or filehandle captures that remain active after the expected
  run lifecycle.

Drift detection should report before it repairs. Later commands can expose
`cove daemon status -json` or `cove daemon check` with structured drift entries.

## Daemon-less Fallback

The existing mode remains supported. If `coved` is not installed or not running,
`cove run`, `cove ctl`, `cove image gc`, `cove network`, and related commands
continue to operate directly. The daemon is an optimization and coordination
layer, not a new hard dependency.

CLI behavior:

- `cove daemon status` reports a connection error when no daemon answers.
- Other commands may opportunistically use the daemon in later slices, but must
  keep direct operation unless a command explicitly requires daemon mode.

## Slice Plan

Slice 1 closes #245 foundation:

1. This design.
2. `cmd/coved` with `STATUS`.
3. `cove daemon status`, `cove daemon start`, and `cove daemon stop`.

Later slices:

1. Add structured status with VM discovery.
2. Add lifecycle policy registration and enforcement.
3. Add scheduled image GC with metrics.
4. Add network drift reports.
5. Add installer hardening and operator docs for launchd mode.
