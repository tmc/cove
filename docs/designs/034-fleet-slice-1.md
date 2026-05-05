# Design 034: Fleet Slice 1

Status: Implemented (2026-05-05; Slice 1)
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

## Security

Slice 1 relies on SSH authentication and the existing cove control-token model.
The tunnel is per command, short-lived, and bound to localhost. cove does not
open a new network listener on the remote host and does not add a daemon-level
fleet API.

## Deferred

- Scheduler and placement.
- Fleet-wide image push/pull.
- Health checks and host inventory.
- Concurrent multi-host run.
- SSH connection pooling.
- Native Go SSH client.
- State replication or leader election.
