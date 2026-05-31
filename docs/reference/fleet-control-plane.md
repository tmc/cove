---
title: Fleet Control Plane
---
# Fleet Control Plane

`cove-fleetd` is the first stateful fleet-control-plane boundary. It owns host
inventory, assignment leases, and the worker-facing protocol surface. `coved`
can now dial out as a worker and execute leased `cove` assignments;
controller-side scheduling is the next slice.

Start a private controller:

```bash
cove-fleetd -addr 127.0.0.1:9758
```

Options:

| Flag | Default | Description |
|------|---------|-------------|
| `-addr <addr>` | `127.0.0.1:9758` | HTTP listen address |
| `-store <path>` | `~/.vz/fleet-controller.json` | JSON host inventory store; empty keeps memory only |
| `-worker-ttl <duration>` | `30s` | Time before a worker heartbeat is marked stale |
| `-assignment-ttl <duration>` | `30s` | Time before a leased assignment can be reclaimed |
| `-version` | false | Print build version |

Start a worker:

```bash
coved -fleet-url http://127.0.0.1:9758 -fleet-id mini-1 -fleet-label zone=desk
```

Worker flags:

| Flag | Default | Description |
|------|---------|-------------|
| `-fleet-url <url>` | empty | Controller URL; empty disables worker dial-out |
| `-fleet-id <id>` | hostname | Stable worker id registered with the controller |
| `-fleet-cove-bin <path>` | sibling `cove` or `cove` on `PATH` | Binary used for `cove` assignments |
| `-fleet-heartbeat-interval <duration>` | `10s` | Heartbeat cadence |
| `-fleet-assignment-interval <duration>` | `5s` | Assignment poll cadence |
| `-fleet-assignment-timeout <duration>` | `30m` | Timeout for one `cove` assignment |
| `-fleet-label key=value` | none | Worker label; repeat for multiple labels |

Worker protocol:

| Verb | Endpoint | Shape |
|------|----------|-------|
| register | `POST /v1/workers/register` | `coved` sends host id, version, labels, CPU count, VM count, and local image count; controller stores the host record. |
| heartbeat | `POST /v1/workers/heartbeat` | `coved` refreshes `last_seen` and capacity. |
| await-assignment | `GET /v1/workers/<id>/assignments` | `coved` polls for work; the controller leases one pending or expired assignment and otherwise returns `204 No Content`. |
| report-status | `POST /v1/workers/<id>/reports` | `coved` records `noop` as complete, executes `cove` assignments with bounded stdout/stderr capture, and reports other verbs as unsupported. |

Inventory endpoints:

```bash
curl http://127.0.0.1:9758/healthz
curl http://127.0.0.1:9758/v1/workers
curl http://127.0.0.1:9758/v1/workers/mini-1
```

Assignment endpoints:

```bash
curl -X POST http://127.0.0.1:9758/v1/assignments \
  -H 'content-type: application/json' \
  -d '{"id":"probe-1","worker_id":"mini-1","verb":"noop"}'
curl -X POST http://127.0.0.1:9758/v1/assignments \
  -H 'content-type: application/json' \
  -d '{"id":"run-1","worker_id":"mini-1","verb":"cove","args":["run","-ephemeral","-headless"]}'
curl http://127.0.0.1:9758/v1/assignments
curl http://127.0.0.1:9758/v1/assignments/probe-1
```

Assignments are stored with `pending`, `leased`, or worker-reported terminal
status. Leases expire after the controller's assignment TTL and can then be
claimed by another eligible worker.

Register a worker record manually:

```bash
curl -X POST http://127.0.0.1:9758/v1/workers/register \
  -H 'content-type: application/json' \
  -d '{"id":"mini-1","host":"mini.local","version":"dev","cpus":12,"memory_bytes":68719476736}'
```

This surface is intentionally private and local-first. It does not yet replace
`cove fleet` SSH placement, choose hosts for new work, or provide Orchard-style
reconciliation.
