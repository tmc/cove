---
title: Fleet Control Plane
---
# Fleet Control Plane

`cove-fleetd` is the first stateful fleet-control-plane boundary. It owns host
inventory and the worker-facing protocol surface; `coved` worker dial-out and
controller-side scheduling are the next slices.

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

Worker protocol:

| Verb | Endpoint | Shape |
|------|----------|-------|
| register | `POST /v1/workers/register` | Worker sends host id, version, labels, and capacity; controller stores the host record. |
| heartbeat | `POST /v1/workers/heartbeat` | Worker refreshes `last_seen` and capacity. |
| await-assignment | `GET /v1/workers/<id>/assignments` | Returns `204 No Content` until scheduler assignments exist. |
| report-status | `POST /v1/workers/<id>/reports` | Worker reports assignment status; controller records the latest report. |

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
`cove fleet` SSH placement, execute assignments, or provide Orchard-style
reconciliation.
