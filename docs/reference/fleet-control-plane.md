---
title: Fleet Control Plane
---
# Fleet Control Plane

`cove-fleetd` is the first stateful fleet-control-plane boundary. It owns host
inventory, assignment leases, and the worker-facing protocol surface. `coved`
can now dial out as a worker and execute leased `cove` assignments;
controller-side placement can choose a ready worker by least-loaded or
image-affinity or bin-pack policy, and the controller reconciles stale workers
and expired assignment leases. Operators can cordon or drain workers for
maintenance without dropping their heartbeat history, and can ask the controller
for an operations summary across workers, assignments, sandboxes, warm pools,
and metering. They can also prepare a base image across the fleet before job
placement, push VM lifecycle policy updates, fan out storage budget/prune
policy, or keep a fork warm-pool quota active.

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
| await-assignment | `GET /v1/workers/<id>/assignments` | `coved` polls for work; the controller leases one pending assignment and otherwise returns `204 No Content`. The daemon starts leased assignments asynchronously so long `cove` runs do not block later polls or heartbeats. |
| report-status | `POST /v1/workers/<id>/reports` | `coved` records `noop` as complete, executes `cove` assignments with bounded stdout/stderr capture, sends `running` renewals while `cove` is active, and reports other verbs as unsupported. |

Inventory endpoints:

```bash
curl http://127.0.0.1:9758/healthz
curl http://127.0.0.1:9758/v1/operations/summary
curl http://127.0.0.1:9758/v1/operations/summary?namespace=team-a
curl http://127.0.0.1:9758/v1/workers
curl http://127.0.0.1:9758/v1/workers/mini-1
curl -X POST http://127.0.0.1:9758/v1/workers/mini-1/cordon \
  -H 'content-type: application/json' \
  -d '{"reason":"maintenance"}'
curl -X POST http://127.0.0.1:9758/v1/workers/mini-1/drain \
  -H 'content-type: application/json' \
  -d '{"reason":"maintenance"}'
curl -X POST http://127.0.0.1:9758/v1/workers/mini-1/uncordon
curl -X POST http://127.0.0.1:9758/v1/reconcile
```

`POST /v1/workers/{id}/drain` is the controller maintenance path for hosted
sandbox workloads. It cordons the worker to prevent new placement, then walks
hosted sandbox handles assigned to that worker: pending sandbox runs are
canceled, and leased/running/ready handles are marked `draining` with a
same-worker `cove ctl -vm <vm> stop` cleanup assignment. The response includes
the updated worker, per-sandbox stop results, and skipped terminal or
lease-held sandboxes. Active sandbox modify leases are honored during drain:
the holder must release the lease, let it expire, or stop/delete the handle
directly with the holder before a later drain can stop it. The operation
records both `worker.drain` and per-sandbox `sandbox.drain` audit events for
stopped handles. Like cordon/uncordon, drain is a fleet-global operator action
and requires an unscoped operator/admin identity.

`GET /v1/operations/summary` is the dashboard entry point for operators. It
reconciles first, then returns worker readiness/cordon/stale counts with
attention workers, assignment status counts with active assignments, hosted
sandbox status counts with active and draining handles, warm-pool desired/slot
counts, and aggregate sandbox metering. The optional `namespace` query filters
assignment, sandbox, warm-pool, and metering counts; worker inventory stays
fleet-global. Because the response includes fleet-global worker state, scoped
service-account tokens cannot read it.

Service-account and audit endpoints:

```bash
curl -X POST http://127.0.0.1:9758/v1/service-accounts \
  -H 'content-type: application/json' \
  -d '{"name":"ci","namespace":"team-a","role":"operator","token":"local-secret"}'
curl http://127.0.0.1:9758/v1/service-accounts
curl -H 'authorization: Bearer local-secret' \
  -X POST http://127.0.0.1:9758/v1/assignments \
  -H 'content-type: application/json' \
  -d '{"id":"probe-1","worker_id":"mini-1","verb":"noop"}'
curl -H 'authorization: Bearer local-secret' \
  http://127.0.0.1:9758/v1/assignments
curl http://127.0.0.1:9758/v1/assignments?namespace=team-a
curl -X POST http://127.0.0.1:9758/v1/oidc-bindings \
  -H 'content-type: application/json' \
  -d '{
    "name":"github-main",
    "issuer":"https://token.actions.githubusercontent.com",
    "subject":"repo:tmc/cove:ref:refs/heads/main",
    "audience":"cove-fleet",
    "namespace":"team-a",
    "role":"operator",
    "jwks_url":"https://token.actions.githubusercontent.com/.well-known/jwks"
  }'
curl http://127.0.0.1:9758/v1/oidc-bindings
curl -X POST http://127.0.0.1:9758/v1/saml-bindings \
  -H 'content-type: application/json' \
  -d '{
    "name":"okta",
    "entity_id":"https://idp.example/saml",
    "sso_url":"https://idp.example/sso",
    "audience":"https://fleet.example/saml/acs",
    "namespace":"team-a",
    "role":"operator",
    "certificate_pem":"-----BEGIN CERTIFICATE-----\n...\n-----END CERTIFICATE-----"
  }'
curl http://127.0.0.1:9758/v1/saml-bindings
curl http://127.0.0.1:9758/v1/audit
curl http://127.0.0.1:9758/v1/audit?limit=50
curl 'http://127.0.0.1:9758/v1/audit?action=assignment.create&actor=service-account:ci&target_type=assignment&limit=50'
curl 'http://127.0.0.1:9758/v1/audit?offset=50&limit=50'
curl http://127.0.0.1:9758/v1/audit/verify
curl -X DELETE http://127.0.0.1:9758/v1/service-accounts/ci
```

The controller persists audit events in the fleet store for high-value state
changes: worker registration, cordon lifecycle, assignment creation, assignment
leases, terminal assignment reports, fleet reconcile changes, image/policy/
storage fan-out, and warm-pool ensure/claim/delete operations. Each new event
carries `prev_hash` and `hash` fields that chain the global audit log;
`GET /v1/audit/verify` recomputes the chain and returns `ok`, `events`,
`head_hash`, and any chain issues. `GET /v1/audit` returns `events`, `count`,
`offset`, `limit`, and `next_offset`; query filters include `namespace`,
`actor`, `action`, `target_type`, `target_id`, and `sandbox_id`. `limit`
preserves the existing latest-events behavior, and `offset` pages backward
through matching events. Service-account tokens are stored only as
SHA-256 hashes, so operators should provide high-entropy random tokens and keep
the plaintext in their own secret manager. Supplying a matching bearer token on
operator requests records audit actor `service-account:<name>`;
unauthenticated local requests still record `controller`, and worker protocol
events record `worker:<id>`.
`/v1/oidc-bindings` adds an initial OIDC identity-binding path: admin users bind
one issuer, subject, audience, namespace, role, and one or more RS256 public
keys or a `jwks_url`. If neither keys nor `jwks_url` are supplied, the
controller discovers `jwks_uri` from `<issuer>/.well-known/openid-configuration`
the first time a matching token arrives. A matching bearer JWT must verify with
one of those public keys, must not be expired, and records audit actor
`oidc:<binding-name>`. Cached JWKS keys persist in the fleet store and refresh
on key misses so provider key rotation does not require rewriting the binding.
`/v1/saml-bindings` adds the fail-closed SAML configuration half: admin users
can store an IdP entity ID, SSO URL, service-provider audience, namespace, role,
and PEM X.509 signing certificate. The controller validates and persists the
certificate and exposes only its SHA-256 fingerprint in API responses. SAML
assertion authentication is not accepted yet; it remains disabled until XML
signature verification, recipient/audience checks, and replay protection land.
If a service account has `namespace` set, assignment, warm-pool, sandbox,
service-account, and audit list/read/mutation requests through that bearer token
are scoped to that namespace; attempts to write another namespace are rejected.
Service accounts also carry a `role`: `viewer` can list/read scoped resources
and plan placements, `operator` can mutate operational resources such as
assignments, warm-pools, image/policy/storage fan-out, and claims, and `admin`
can manage service accounts, OIDC bindings, and SAML binding records. Omitted
role defaults to `admin` for compatibility.
Service accounts without `namespace` and unauthenticated local requests remain
unscoped for the local-first controller workflow. Requests with an unknown bearer token
are rejected instead of falling back to local controller identity. Worker
registration, heartbeat, reports, host inventory, and audit-chain verification
remain fleet-global in this slice, so namespace-scoped service accounts can
read their filtered audit events but cannot verify the global chain.

Image preparation endpoint:

```bash
curl -X POST http://127.0.0.1:9758/v1/images/prepare \
  -H 'content-type: application/json' \
  -d '{"source_ref":"registry.example/cove/macos-runner:latest","image_ref":"macos-runner:latest","image_manifest_digest":"sha256:...","image_digest_ref":"registry.example/cove/macos-runner@sha256:...","image_platform":"darwin/arm64","required_labels":{"zone":"desk"}}'
```

Image preparation creates one `cove image pull -tag <image_ref> <source_ref>`
assignment for each non-cordoned ready worker that matches `required_labels` and
does not already report `image_ref`. Workers that already have the image, are
cordoned or stale, or already have an active preparation assignment are returned
in `skipped`. After a successful image preparation assignment, `coved` sends an
extra heartbeat so the controller can place later `image-affinity` work against
fresh image refs.
When `image_manifest_digest` is supplied, a worker counts as already prepared
only if its heartbeat reports the same `image_ref` and
`source_manifest_digest`; a stale mutable ref is refreshed with a forced pull.
The optional `image_digest_ref` and `image_platform` fields are stored on the
queued assignments and returned in the preparation result for offline bundle
audits.

Image GC endpoint:

```bash
curl -X POST http://127.0.0.1:9758/v1/images/gc \
  -H 'content-type: application/json' \
  -d '{"required_labels":{"zone":"desk"},"older_than":"168h","apply":true}'
```

Image GC creates one `cove image gc` assignment for each non-cordoned ready
worker that matches `required_labels`. `apply` defaults to false, which queues
`cove image gc -dry-run`; set `apply:true` to queue `cove image gc -yes`.
`older_than` is an optional Go duration string passed through as `-older-than`.
Workers that are cordoned or stale, or already have an active image-GC
assignment, are returned in `skipped`. After a successful image-GC assignment,
`coved` sends an extra heartbeat so the controller's image refs reflect the
post-GC store.

Lifecycle policy endpoint:

```bash
curl -X POST http://127.0.0.1:9758/v1/policies/lifecycle \
  -H 'content-type: application/json' \
  -d '{"vm_name":"ci-runner","required_labels":{"zone":"desk"},"idle_timeout":"30m","max_age":"24h","run_budget":100}'
curl -X POST http://127.0.0.1:9758/v1/policies/lifecycle \
  -H 'content-type: application/json' \
  -d '{"vm_name":"ci-runner","required_labels":{"zone":"desk"},"clear":true}'
```

Lifecycle policy push creates one `cove policy <vm> set ...` assignment for
each non-cordoned ready worker that matches `required_labels`. The controller
passes `idle_timeout` and `max_age` as Go duration strings and `run_budget` as
the guest exec count. `clear:true` queues `cove policy <vm> clear` and cannot be
combined with thresholds. Workers that are cordoned or stale, or already have
an active lifecycle-policy assignment for the same VM, are returned in
`skipped`.

Storage policy endpoints:

```bash
curl -X POST http://127.0.0.1:9758/v1/storage/budget \
  -H 'content-type: application/json' \
  -d '{"required_labels":{"zone":"desk"},"target":"750GB","warn_pct":80,"hard_pct":95}'
curl -X POST http://127.0.0.1:9758/v1/storage/budget \
  -H 'content-type: application/json' \
  -d '{"required_labels":{"zone":"desk"},"clear":true}'
curl -X POST http://127.0.0.1:9758/v1/storage/prune \
  -H 'content-type: application/json' \
  -d '{"required_labels":{"zone":"desk"},"older_than":"168h","apply":true}'
```

Storage budget push creates one `cove storage budget set -target <target>`
assignment per matching ready worker. `warn_pct` and `hard_pct` are optional
and default to the local CLI defaults when omitted. `clear:true` queues
`cove storage budget clear` and cannot be combined with thresholds.
Storage prune push creates one `cove storage prune` assignment per matching
ready worker. It is dry-run by default; `apply:true` adds `-apply`, and
`older_than` is an optional Go duration string passed through as `-older-than`.
Set `category:"build-scratch"` to target `cove storage prune build-scratch`.
Workers that are cordoned or stale, or already have an active storage
budget/prune assignment for the same operation, are returned in `skipped`.

Placement planning endpoint:

```bash
curl -X POST http://127.0.0.1:9758/v1/placements/plan \
  -H 'content-type: application/json' \
  -d '{"policy":"image-affinity","image_ref":"macos-runner:latest","image_manifest_digest":"sha256:...","anti_affinity_key":"ci/buildkite","resources":{"vms":1},"limit":5}'
```

Placement planning returns the retained ranked feasible workers without storing
an assignment. It uses the same policy, required-label, image-affinity,
anti-affinity, and slot-cap logic as assignment creation. `limit` defaults to
five candidates.
If `image_manifest_digest` is set, the plan only includes workers whose
heartbeat reports that exact source manifest digest for the requested
`image_ref`; stale or unknown mutable refs are not warm candidates.

Warm-pool endpoint:

```bash
curl -X POST http://127.0.0.1:9758/v1/warm-pools \
  -H 'content-type: application/json' \
  -d '{"name":"runner-14","image_ref":"macos-runner:14.5","image_manifest_digest":"sha256:...","size":3,"required_labels":{"zone":"desk"},"resources":{"vms":1}}'
curl -X POST http://127.0.0.1:9758/v1/warm-pools/claim \
  -H 'content-type: application/json' \
  -d '{"name":"runner-14","command":["/bin/sh","-lc","make test"],"env":{"CI":"1"}}'
curl http://127.0.0.1:9758/v1/warm-pools
curl http://127.0.0.1:9758/v1/warm-pools/runner-14
curl -X DELETE http://127.0.0.1:9758/v1/warm-pools/runner-14
```

A warm pool persists a desired number of active fork slots for one image. The
controller reconciles missing slots into `cove` assignments using the normal
placement scheduler and anti-affinity key `warm-pool/<name>`. Each slot runs:

```bash
cove run -fork-from <image_ref> -fork-name <generated> -ephemeral -keep -headless
```

Warm pools accept the same optional `image_manifest_digest`, `image_digest_ref`,
and `image_platform` fields as assignments. With a manifest digest, the
controller only replenishes slots on workers that report the exact image
provenance; use `/v1/images/prepare` first to refresh stale mutable refs.

The first slice keeps those fork assignments active, probes the warmed VM with
`cove shell <generated> -- /bin/sh -c true`, and replenishes completed or failed
slots. `POST /v1/warm-pools/claim` claims only a `ready` slot, marks that slot
`claimed`, and queues a zero-slot same-worker guest-exec assignment that runs
`cove shell <generated> -- <command...>`. The claimed VM continues counting
against host capacity; when the guest-exec assignment finishes, `coved` stops
the claimed warm VM with `cove ctl -vm <generated> stop`, and reconciliation
creates a replacement warm slot when capacity allows. Lowering `size`
downsizes the pool during the same reconcile pass: pending surplus slots are
returned in `canceled`, and already-started surplus slots are marked `draining`
while the controller queues same-worker `cove ctl -vm <generated> stop`
assignments returned in `cleanup`. `DELETE /v1/warm-pools/{name}` removes the
pool definition, cancels pending slots, queues the same cleanup for idle
started slots, and returns claimed slots in `deferred` so in-flight guest work
can finish and use its existing claimed-slot stop path.

Warm-pool status responses include `slots` for all non-terminal slot
assignments, `active` for slots still counting toward the desired pool quota,
per-state counts (`pending`, `leased`, `running`, `ready`, `claimed`,
`draining`, `terminal`), a `by_status` map, and the open slot assignments.
Claimed and draining slots stay visible even after a replacement slot is
queued, so operators can distinguish ready capacity from in-flight guest work
and cleanup.

Sandbox endpoint:

```bash
curl -X POST http://127.0.0.1:9758/v1/sandboxes \
  -H 'content-type: application/json' \
  -d '{"id":"job-1","image_ref":"macos-runner:14.5","image_manifest_digest":"sha256:...","required_labels":{"zone":"desk"},"args":["--net","nat"]}'
curl http://127.0.0.1:9758/v1/sandboxes
curl 'http://127.0.0.1:9758/v1/sandboxes?status=ready&image_ref=macos-runner:14.5&offset=0&limit=20'
curl http://127.0.0.1:9758/v1/sandboxes/job-1
curl -X POST http://127.0.0.1:9758/v1/sandboxes/job-1/lease \
  -H 'content-type: application/json' \
  -d '{"holder":"runner-42","ttl":"30s"}'
curl -X POST http://127.0.0.1:9758/v1/sandboxes/job-1/start \
  -H 'content-type: application/json' \
  -d '{"holder":"runner-42"}'
curl -X POST http://127.0.0.1:9758/v1/sandboxes/job-1/restart \
  -H 'content-type: application/json' \
  -d '{"holder":"runner-42"}'
curl -X POST 'http://127.0.0.1:9758/v1/sandboxes/job-1/wait?timeout=30s'
curl -X POST 'http://127.0.0.1:9758/v1/sandboxes/job-1/exec?timeout=30s' \
  -H 'content-type: application/json' \
  -d '{"holder":"runner-42","command":["/usr/bin/sw_vers"],"env":{"CI":"1"}}'
curl -X POST 'http://127.0.0.1:9758/v1/sandboxes/job-1/control?timeout=30s' \
  -H 'content-type: application/json' \
  -d '{"holder":"runner-42","type":"screenshot","screenshot":{"format":"png","scale":1}}'
curl 'http://127.0.0.1:9758/v1/sandboxes/job-1/events?action=sandbox.exec&limit=20'
curl 'http://127.0.0.1:9758/v1/sandboxes/job-1/reports?role=exec&limit=20'
curl 'http://127.0.0.1:9758/v1/sandboxes/job-1/metering'
curl 'http://127.0.0.1:9758/v1/metering/sandboxes?sandbox_id=job-1'
curl -X POST http://127.0.0.1:9758/v1/sandboxes/job-1/stop \
  -H 'content-type: application/json' \
  -d '{"holder":"runner-42"}'
curl -X DELETE 'http://127.0.0.1:9758/v1/sandboxes/job-1?holder=runner-42'
curl -X DELETE 'http://127.0.0.1:9758/v1/sandboxes/job-1/lease?holder=runner-42'
```

The sandbox API is the first hosted-style handle surface. `POST /v1/sandboxes`
creates one image-affinity `cove run -fork-from <image_ref> -fork-name <vm>
-ephemeral -keep -headless` assignment and returns an opaque sandbox id, VM
name, worker, status, and backing assignment. `id` is optional; if omitted the
controller generates `sandbox-<timestamp>`. `vm_name` is optional and defaults
to `cove-sandbox-<id>`. The controller records the backing assignment with
`sandbox_id` and `sandbox_role:"run"` so the handle can be listed and fetched
without a separate scheduler. Extra `args` are appended to `cove run`, but
fork/source/lifetime/headless flags are reserved by the controller.
Create requests accept optional `image_manifest_digest`, `image_digest_ref`,
and `image_platform` fields. The returned sandbox status and backing assignment
keep those fields, and exact-digest requests are admitted only onto workers that
report the matching image provenance.

`GET /v1/sandboxes` accepts `namespace`, `status`, `worker_id`, `image_ref`,
`offset`, and `limit` query parameters. Namespace-scoped bearer tokens still
force their own namespace, and `offset`/`limit` must be non-negative. Filters
apply after reconciliation, then `offset` skips matching handles and `limit`
caps the response. The result includes `sandboxes`, `count`, `offset`, `limit`,
and `next_offset` when another page is available, so clients can ask for ready
handles, draining cleanup, a single worker, or a base image without fetching
the whole controller inventory.

`POST /v1/sandboxes/{id}/lease` acquires or renews an exclusive client lease on
the sandbox handle. The optional `holder` defaults to the authenticated actor;
the optional `ttl` defaults to `30s`. A different holder receives `409
Conflict` until the current lease expires or is released. `DELETE
/v1/sandboxes/{id}/lease?holder=<holder>` releases the lease and returns the
updated sandbox status. Lease state is embedded in both the sandbox status and
the backing assignment, and lease acquire/release operations are audited. While
a lease is active, sandbox mutations require the matching holder: start,
restart, stop, exec, control, and delete return `409 Conflict` when the holder
is missing or different. Reads, waits, metering, and lease release remain
available; `holder` can be sent in the JSON body for `POST` mutations or as a
query parameter on `DELETE /v1/sandboxes/{id}`.

`coved -fleet-url` probes sandbox run assignments with `cove shell <vm> --
/bin/sh -c true` before reporting `ready`, matching warm-pool readiness
semantics. `POST /v1/sandboxes/{id}/wait` waits until the sandbox reaches a
terminal status or `timeout` expires; `timeout=0` returns the current status
without polling. `POST /v1/sandboxes/{id}/start` requeues a terminal sandbox
handle: canceled handles run the original fork assignment again, while stopped
or completed handles start the retained VM on the same ready worker with
`cove run -vm <vm> -headless`. `POST /v1/sandboxes/{id}/restart` marks a
started sandbox `restarting`, queues same-worker stop cleanup, and requeues the
retained VM start when cleanup reports complete. `POST
/v1/sandboxes/{id}/stop` cancels a pending sandbox, or marks a started sandbox
`draining` and queues a same-worker `cove ctl -vm <vm> stop` cleanup assignment
with `sandbox_role:"stop"`. When that stop assignment reports complete, the
sandbox handle becomes `stopped`. Start, restart, and stop accept optional
JSON `holder` fields for leased sandboxes.
`DELETE /v1/sandboxes/{id}` uses the same stop/cancel path and records a delete
audit event; pass the lease holder as `?holder=<holder>` when the sandbox is
leased.

`POST /v1/sandboxes/{id}/exec` queues a same-worker `cove shell <vm> -- ...`
assignment for a `ready` sandbox and returns the worker report when it finishes.
The body requires `command`; `env` is optional. `timeout` can be supplied in the
query string or JSON body. `timeout=0` returns the queued assignment without
polling, which lets clients watch `/v1/assignments/{assignment_id}` themselves.
Pass `holder` in the JSON body when the sandbox has an active lease.

`POST /v1/sandboxes/{id}/control` queues a same-worker VM control-socket request
for a `ready` sandbox. Supported types are `screenshot`, `key`, `mouse`, and
`text`; the body uses the same typed payloads as the local control socket and
accepts `timeout` in the query string or JSON body. Screenshot responses expose
the base64 image data as `data` and preserve the full control-socket response in
`response`, so hosted OpenAI `ComputerTool` calls can use the same
screenshot/key/text/mouse methods as local VMs. Pass `holder` in the JSON body
when the sandbox has an active lease.

`GET /v1/sandboxes/{id}/metering` and `GET /v1/metering/sandboxes` return
append-only records for metered sandbox run intervals. The controller records
time spent in `running` and `ready`, derives VM, CPU, and memory-byte
millisecond totals from the sandbox resources, and does not meter pending,
stopped, draining, or restarting time. Namespace-scoped service accounts only
see records in their namespace.

`GET /v1/sandboxes/{id}/events` returns the sandbox-scoped slice of the
hash-chained controller audit feed. It includes lifecycle, lease, exec, control,
and worker report events for that sandbox; `actor`, `action`, `offset`, and
`limit` query filters match the global audit-list semantics. Namespace-scoped
service accounts receive `404` for sandboxes outside their namespace.

`GET /v1/sandboxes/{id}/reports` returns the sandbox-scoped worker reports for
run, stop, exec, and control assignments that have reported at least once.
`role`, `status`, `offset`, and `limit` filters let clients retrieve the latest
exec/control output without following raw assignment IDs; report rows include
exit code, stdout, stderr, error text, worker ID, assignment ID, and timestamps.

This is a scaffold for the hosted `/v1/sandboxes` lifecycle:
create/list/get/delete/start/restart/stop/wait/exec/control, leases, events,
reports, and metering are present. The OpenAI Agents Python adapter and public Go
`agentsandbox` package can switch between local VM control and this
cloud/control-plane path, including hosted lifecycle helpers, leases,
metering, sandbox event and report history, and ComputerTool-style
screenshot/key/text/mouse events. Both SDKs remember the holder returned by
`lease` and include it on later hosted sandbox mutations until
`release_lease`/`ReleaseLease` succeeds.

Go SDK example:

```go
ctx := context.Background()
sb, err := agentsandbox.Create(ctx, agentsandbox.ClientOptions{
	Provider: agentsandbox.ProviderCloud,
	FleetURL: "https://fleet.internal.example",
	APIKey:   os.Getenv("COVE_API_KEY"),
	ImageRef: "macos-base:latest",
})
if err != nil {
	log.Fatal(err)
}
defer sb.Delete(ctx)
page, err := sb.ListPage(ctx, agentsandbox.SandboxListOptions{
	Status:   "ready",
	ImageRef: "macos-base:latest",
	Offset:   0,
	Limit:    10,
})
if err != nil {
	log.Fatal(err)
}
_ = page.NextOffset
lease, err := sb.Lease(ctx, "runner-42", 30*time.Second)
if err != nil {
	log.Fatal(err)
}
defer sb.ReleaseLease(ctx, lease.Lease.Holder)
if err := sb.WaitReady(ctx, 2*time.Minute); err != nil {
	log.Fatal(err)
}
result, err := sb.Shell(ctx, "sw_vers")
if err != nil {
	log.Fatal(err)
}
fmt.Print(result.Stdout)
metering, err := sb.Metering(ctx)
if err != nil {
	log.Fatal(err)
}
fmt.Printf("metered records: %d\n", metering.Summary.Records)
events, err := sb.Events(ctx, agentsandbox.SandboxEventListOptions{Action: "sandbox.exec", Limit: 20})
if err != nil {
	log.Fatal(err)
}
fmt.Printf("exec events: %d\n", events.Count)
reports, err := sb.Reports(ctx, agentsandbox.SandboxReportListOptions{Role: "exec", Limit: 20})
if err != nil {
	log.Fatal(err)
}
fmt.Printf("exec reports: %d\n", reports.Count)
```

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
  -d '{"id":"placed-1","policy":"image-affinity","image_ref":"macos-runner:latest","image_manifest_digest":"sha256:...","image_digest_ref":"registry.example/cove/macos-runner@sha256:...","image_platform":"darwin/arm64","verb":"cove","args":["run","-fork-from","macos-runner:latest","-ephemeral"]}'
curl -X POST http://127.0.0.1:9758/v1/assignments \
  -H 'content-type: application/json' \
  -d '{"id":"packed-1","policy":"bin-pack","anti_affinity_key":"ci/buildkite","resources":{"vms":1},"verb":"cove","args":["run","-ephemeral","-headless"]}'
curl http://127.0.0.1:9758/v1/assignments
curl http://127.0.0.1:9758/v1/assignments/probe-1
```

Assignments are stored with `pending`, `leased`, `running`, `ready`, `claimed`,
`draining`, `canceled`, or worker-reported terminal status. `ready` is used for
a warm-pool slot whose guest agent accepted a probe through `cove shell`;
`claimed` is used for a ready warm-pool slot that has been handed to a job, and
`draining` is used for a surplus warm slot while its stop assignment is pending.
A claimed slot still consumes host capacity but is no longer counted as an
available warm slot. `coved` renews active `cove` assignments with `running` or
`ready` reports. Claimed warm-pool guest-exec assignments stop the claimed VM
after the guest command returns.
Reconciliation marks expired workers stale, requeues expired assignment leases,
rejects late reports for reclaimed leases, and can move a policy-placed
assignment from a stale worker to another ready worker.

Cordoned workers keep heartbeating and reporting, but controller placement
skips them for unbound and policy-placed assignments. Explicit `worker_id`
assignments can still target a cordoned worker.

When `worker_id` is empty and `policy` is set, the controller places the
assignment before storing it:

| Policy | Placement |
|--------|-----------|
| `least-loaded` | Choose the non-cordoned ready worker with the lowest VM count plus pending assignment count. |
| `image-affinity` | Prefer a non-cordoned ready worker that already reports `image_ref`; fall back to least-loaded. If `image_manifest_digest` is set, only workers that report the matching source manifest digest for that ref are feasible. |
| `bin-pack` | Choose the densest non-cordoned ready worker that still fits the assignment's `resources.vms` under the worker's `max_vms` slot cap. |

`required_labels` can restrict placement to workers with exact matching labels.
Workers report current VM count as `vms`; `coved` defaults `max_vms` to host CPU
count. Assignment `resources.vms` defaults to one scheduling slot when omitted.
Set `anti_affinity_key` to spread active assignments for the same job, base, or
replica group across workers. `image-affinity` still prefers a warm worker
before applying the anti-affinity tie-break.
Worker heartbeats include both legacy `image_refs` and `image_details` entries
with optional `source_manifest_digest`, so mutable tags can be scheduled with a
digest-exact provenance check.
`POST /v1/placements/plan` exposes the same ranking as a read-only top-k plan.

Register a worker record manually:

```bash
curl -X POST http://127.0.0.1:9758/v1/workers/register \
  -H 'content-type: application/json' \
  -d '{"id":"mini-1","host":"mini.local","version":"dev","image_refs":["macos-runner:latest"],"image_details":[{"ref":"macos-runner:latest","source_manifest_digest":"sha256:..."}],"cpus":12,"max_vms":8,"memory_bytes":68719476736}'
```

This surface is intentionally private and local-first. It now has basic
controller reconciliation, worker cordon lifecycle, fleet image preparation,
fleet image-GC push, lifecycle-policy push, storage budget/prune push, retained
placement plans, and a first fork warm-pool quota reconciler with agent-ready
slot claim and guest `Exec` handoff through the `cove shell` path plus
claimed-slot stop and downsize cleanup, plus a persistent fleet audit feed.
