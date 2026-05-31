---
title: Fleet Control Plane
---
# Fleet Control Plane

`cove-fleetd` is the first stateful fleet-control-plane boundary. It owns host
inventory and the worker-facing protocol surface. `coved` can now dial out as a
worker; controller-side scheduling is the next slice.

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
| `-fleet-heartbeat-interval <duration>` | `10s` | Heartbeat cadence |
| `-fleet-assignment-interval <duration>` | `5s` | Assignment poll cadence |
| `-fleet-label key=value` | none | Worker label; repeat for multiple labels |

Worker protocol:

| Verb | Endpoint | Shape |
|------|----------|-------|
| register | `POST /v1/workers/register` | `coved` sends host id, version, labels, CPU count, VM count, and local image count; controller stores the host record. |
| heartbeat | `POST /v1/workers/heartbeat` | `coved` refreshes `last_seen` and capacity. |
| await-assignment | `GET /v1/workers/<id>/assignments` | `coved` polls for work; the controller returns `204 No Content` until scheduler assignments exist. |
| report-status | `POST /v1/workers/<id>/reports` | `coved` records `noop` assignments as complete and reports other verbs as unsupported until assignment execution ships. |

Inventory endpoints:

```bash
curl http://127.0.0.1:9758/healthz
curl http://127.0.0.1:9758/v1/workers
curl http://127.0.0.1:9758/v1/workers/mini-1
```

Register a worker record manually:

```bash
curl -X POST http://127.0.0.1:9758/v1/workers/register \
  -H 'content-type: application/json' \
  -d '{"id":"mini-1","host":"mini.local","version":"dev","cpus":12,"memory_bytes":68719476736}'
```

This surface is intentionally private and local-first. It does not yet replace
`cove fleet` SSH placement, execute VM assignments, or provide Orchard-style
reconciliation.
