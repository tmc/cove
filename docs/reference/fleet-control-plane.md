---
title: Fleet Control Plane
---
# Fleet Control Plane

`cove-fleetd` is the first stateful fleet-control-plane boundary. It owns host
inventory, assignment leases, and the worker-facing protocol surface. `coved`
can now dial out as a worker and execute leased `cove` assignments;
controller-side placement can choose a ready worker by least-loaded or
image-affinity or bin-pack policy, and the controller reconciles stale workers
and expired assignment leases. Operators can cordon workers for maintenance
without dropping their heartbeat history, and can ask the controller to prepare
a base image across the fleet before job placement or keep a fork warm-pool
quota active.

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
| `-reconcile-interval <duration>` | `5s` | Reconciliation cadence; `0` disables the background loop |
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
| register | `POST /v1/workers/register` | `coved` sends host id, version, labels, CPU count, VM count, local image count, and local image refs; controller stores the host record. |
| heartbeat | `POST /v1/workers/heartbeat` | `coved` refreshes `last_seen` and capacity. |
| await-assignment | `GET /v1/workers/<id>/assignments` | `coved` polls for work; the controller leases one pending assignment and otherwise returns `204 No Content`. |
| report-status | `POST /v1/workers/<id>/reports` | `coved` records `noop` as complete, executes `cove` assignments with bounded stdout/stderr capture, sends `running` renewals while `cove` is active, and reports other verbs as unsupported. |

Inventory endpoints:

```bash
curl http://127.0.0.1:9758/healthz
curl http://127.0.0.1:9758/v1/workers
curl http://127.0.0.1:9758/v1/workers/mini-1
curl -X POST http://127.0.0.1:9758/v1/workers/mini-1/cordon \
  -H 'content-type: application/json' \
  -d '{"reason":"maintenance"}'
curl -X POST http://127.0.0.1:9758/v1/workers/mini-1/uncordon
curl -X POST http://127.0.0.1:9758/v1/reconcile
```

Image preparation endpoint:

```bash
curl -X POST http://127.0.0.1:9758/v1/images/prepare \
  -H 'content-type: application/json' \
  -d '{"source_ref":"registry.example/cove/macos-runner:latest","image_ref":"macos-runner:latest","required_labels":{"zone":"desk"}}'
```

Image preparation creates one `cove image pull -tag <image_ref> <source_ref>`
assignment for each non-cordoned ready worker that matches `required_labels` and
does not already report `image_ref`. Workers that already have the image, are
cordoned or stale, or already have an active preparation assignment are returned
in `skipped`. After a successful image preparation assignment, `coved` sends an
extra heartbeat so the controller can place later `image-affinity` work against
fresh image refs.

Placement planning endpoint:

```bash
curl -X POST http://127.0.0.1:9758/v1/placements/plan \
  -H 'content-type: application/json' \
  -d '{"policy":"bin-pack","image_ref":"macos-runner:latest","anti_affinity_key":"ci/buildkite","resources":{"vms":1},"limit":5}'
```

Placement planning returns the retained ranked feasible workers without storing
an assignment. It uses the same policy, required-label, image-affinity,
anti-affinity, and slot-cap logic as assignment creation. `limit` defaults to
five candidates.

Warm-pool endpoint:

```bash
curl -X POST http://127.0.0.1:9758/v1/warm-pools \
  -H 'content-type: application/json' \
  -d '{"name":"runner-14","image_ref":"macos-runner:14.5","size":3,"required_labels":{"zone":"desk"},"resources":{"vms":1}}'
curl http://127.0.0.1:9758/v1/warm-pools
```

A warm pool persists a desired number of active fork slots for one image. The
controller reconciles missing slots into `cove` assignments using the normal
placement scheduler and anti-affinity key `warm-pool/<name>`. Each slot runs:

```bash
cove run -fork-from <image_ref> -fork-name <generated> -ephemeral -keep -headless
```

The first slice keeps those fork assignments active and replenishes completed or
failed slots. Claiming a ready fork for guest `Exec`, stopping excess slots
after a downsize, and exposing per-slot agent readiness are later controller
work.

Assignment endpoints:

```bash
curl -X POST http://127.0.0.1:9758/v1/assignments \
  -H 'content-type: application/json' \
  -d '{"id":"probe-1","worker_id":"mini-1","verb":"noop"}'
curl -X POST http://127.0.0.1:9758/v1/assignments \
  -H 'content-type: application/json' \
  -d '{"id":"run-1","worker_id":"mini-1","verb":"cove","args":["run","-ephemeral","-headless"]}'
curl -X POST http://127.0.0.1:9758/v1/assignments \
  -H 'content-type: application/json' \
  -d '{"id":"placed-1","policy":"image-affinity","image_ref":"macos-runner:latest","verb":"cove","args":["run","-fork-from","macos-runner:latest","-ephemeral"]}'
curl -X POST http://127.0.0.1:9758/v1/assignments \
  -H 'content-type: application/json' \
  -d '{"id":"packed-1","policy":"bin-pack","anti_affinity_key":"ci/buildkite","resources":{"vms":1},"verb":"cove","args":["run","-ephemeral","-headless"]}'
curl http://127.0.0.1:9758/v1/assignments
curl http://127.0.0.1:9758/v1/assignments/probe-1
```

Assignments are stored with `pending`, `leased`, `running`, or worker-reported
terminal status. `coved` renews active `cove` assignments with `running`
reports. Reconciliation marks expired workers stale, requeues expired
assignment leases, rejects late reports for reclaimed leases, and can move a
policy-placed assignment from a stale worker to another ready worker.

Cordoned workers keep heartbeating and reporting, but controller placement
skips them for unbound and policy-placed assignments. Explicit `worker_id`
assignments can still target a cordoned worker.

When `worker_id` is empty and `policy` is set, the controller places the
assignment before storing it:

| Policy | Placement |
|--------|-----------|
| `least-loaded` | Choose the non-cordoned ready worker with the lowest VM count plus pending assignment count. |
| `image-affinity` | Prefer a non-cordoned ready worker that already reports `image_ref`; fall back to least-loaded. |
| `bin-pack` | Choose the densest non-cordoned ready worker that still fits the assignment's `resources.vms` under the worker's `max_vms` slot cap. |

`required_labels` can restrict placement to workers with exact matching labels.
Workers report current VM count as `vms`; `coved` defaults `max_vms` to host CPU
count. Assignment `resources.vms` defaults to one scheduling slot when omitted.
Set `anti_affinity_key` to spread active assignments for the same job, base, or
replica group across workers. `image-affinity` still prefers a warm worker
before applying the anti-affinity tie-break.
`POST /v1/placements/plan` exposes the same ranking as a read-only top-k plan.

Register a worker record manually:

```bash
curl -X POST http://127.0.0.1:9758/v1/workers/register \
  -H 'content-type: application/json' \
  -d '{"id":"mini-1","host":"mini.local","version":"dev","cpus":12,"max_vms":8,"memory_bytes":68719476736}'
```

This surface is intentionally private and local-first. It now has basic
controller reconciliation, worker cordon lifecycle, and fleet image
preparation, retained placement plans, and a first fork warm-pool quota
reconciler. Ready-slot claim and guest `Exec` handoff are not wired yet.
