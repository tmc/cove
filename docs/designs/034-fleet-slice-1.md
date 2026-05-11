# Design 034: Fleet

Status: Implemented (2026-05-05; Slices 1-3). Slice 1 shipped at `622b571`,
`695ae2e`, `9f993a5`, and `366bfac`; Slice 2 at `afba1a5`, `0b4776f`,
`e59348d`, and `6e91044`; Slice 3 at `f13dae5`, `1273fcb`, and `e347836`.
Verified 2026-05-10 (R361): `runFleetCommandWithRunner` at `fleet_cli.go:34` dispatches add/ls/rm/vm/image/ps;
`runFleetAggregateCommand` at `fleet_aggregate_cli.go:24`; `runFleetPSCommand` at
`fleet_aggregate_cli.go:125`; config path `~/.vz/fleet.json` from
`internal/fleet/fleet.go:29` (`DefaultPath`).
Author: Travis Cline
Date: 2026-05-05

## Problem

Users with more than one Mac host need a single cove CLI to inspect and control
VMs running on another trusted host. Slice 1 is deliberately small: it registers
remote hosts and routes selected control-socket commands through SSH. It is not a
cluster, scheduler, or replicated state system.

## Architecture

```text
local cove
    |
    |  cove --fleet=studio ctl -vm linux gui status
    v
fleet config (~/.vz/fleet.json)
    |
    |  ssh user@host -L 0:0:/Users/<user>/.vz/vms/<vm>/control.sock
    v
localhost ephemeral listener
    |
    |  existing cove control-socket JSON protocol
    v
remote ~/.vz/vms/<vm>/control.sock
```

The remote VM owner keeps running the normal per-VM control socket. The local
CLI only creates an SSH tunnel to that socket and then uses the same request
shape it already uses locally.

## Config

Fleet entries live at:

```text
~/.vz/fleet.json
```

The JSON shape is:

```json
{
  "remotes": {
    "studio": {
      "host": "mac-studio.local",
      "user": "tmc",
      "ssh_args": ["-i", "~/.ssh/cove_fleet"],
      "default_vm": "ubuntu"
    }
  }
}
```

`name` is the local context name, similar to Docker contexts or Kubernetes
contexts. `default_vm` is optional and is used when a routed command does not
name a VM explicitly.

## Slice 1 Scope

Add:

```text
cove fleet add <name> <user@host> [-vm <default>]
cove fleet ls
cove fleet rm <name>
cove --fleet=<name> <subcmd>
```

Routed subcommands are limited to commands that use the existing control socket
or read remote metadata through a cove CLI invocation:

- `ctl`
- `shell`
- `cp`
- `logs`
- `vm list`
- `image list`
- `gui status` through `ctl`

Commands that create, mutate, schedule, or coordinate VMs across hosts remain
local in Slice 1. There is no fleet-wide `run`, no scheduler, no image
placement, no state replication, no gossip, and no leader election.

## Slice 2 Scope

Slice 2 adds fleet-wide read aggregation. The local cove CLI queries every
registered host in parallel, labels rows with the fleet host name, and returns a
unified view:

```text
cove fleet vm ls
cove fleet image ls
cove fleet ps
```

`fleet vm ls` runs the remote VM list operation on each host. `fleet image ls`
runs the remote image list operation on each host. `fleet ps` is a convenience
view that lists running VMs across all hosts.

Each host query runs in its own goroutine with a 10 second per-host timeout.
Results are fail-soft: a dead host produces an `(unreachable)` row with the
error string, while healthy hosts still print their data.

Default output is tabular. `--json` emits a machine-readable array with host,
kind, payload text, and error fields so scripts can keep partial results.

## Routing Rules

`--fleet=<name>` selects a remote from `fleet.json`. For control-socket
commands, the local process opens an SSH tunnel to the remote VM's
`control.sock`, then sends the existing JSON request over that local tunnel.

When a command needs a VM name, the VM comes from the command's `-vm` or
positional argument. If none is present, cove uses `default_vm`. If neither is
available, the command fails with an actionable error.

`cove --fleet=<name> vm list` and `cove --fleet=<name> image list` are routed by
running the remote cove CLI over SSH in Slice 1. That keeps remote filesystem
layout and registry state owned by the remote host.

Slice 2 aggregate commands intentionally use the same remote CLI route rather
than introducing a fleet daemon. This keeps host-local state authoritative and
keeps the failure boundary at one SSH command per host.

## Security

Slice 1 relies on SSH authentication and the existing cove control-token model.
The tunnel is per command, short-lived, and bound to localhost. cove does not
open a new network listener on the remote host and does not add a daemon-level
fleet API.

## Deferred

- Scheduler and placement.
- Fleet-wide image push/pull.
- Fleet-wide policy.
- Health checks and richer host inventory.
- Concurrent multi-host run.
- SSH connection pooling.
- Native Go SSH client.
- State replication or leader election.

## Cross-references

- [`docs/designs/033-cove-daemon.md`](033-cove-daemon.md) for the single-host
  coordinator that should not be confused with fleet routing.
- [`docs/features/gha-executor.md`](../features/gha-executor.md) for the
  private action surface that still runs on one trusted host at a time.
- [`docs/quickstart/fleet.md`](../quickstart/fleet.md) for the operator-facing
  fleet usage that this slice documents.
