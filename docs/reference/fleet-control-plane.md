---
title: Fleet Control Plane
---
# Fleet Control Plane

`cove-fleetd` is the first stateful fleet-control-plane boundary. It owns host
inventory, assignment leases, and the worker-facing protocol surface. `coved`
can now dial out as a worker and execute leased `cove` assignments;
controller-side placement can choose a ready worker by least-loaded,
image-affinity, or bin-pack policy, including required worker capabilities, and
the controller reconciles stale workers, expired assignment leases, and missed
queue deadlines. Operators
can cordon, quarantine, evacuate, or drain workers for maintenance and incident
response without dropping their heartbeat history, and can ask the controller
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
| `-fleet-capability <name>` | auto-detected plus none | Extra worker capability; repeat for custom traits |

`coved` discovers standard worker capabilities at startup and merges them with
any repeated `-fleet-capability` values. Darwin workers automatically report
`ram-overlay`; report `asif` when DiskImages2 is available; and report
`apfs-quota` when `diskutil apfs setQuota` is available and the daemon is
running as root. Hosted sandbox and warm-pool requests can target cove's
RAM-backed ephemeral forks, sparse ASIF disks, or live APFS directory quotas
without per-host flag drift. Use manual capabilities for operator-defined
traits such as GUI seats, lab hardware, or reserved host classes.

Worker protocol:

| Verb | Endpoint | Shape |
|------|----------|-------|
| register | `POST /v1/workers/register` | `coved` sends host id, version, labels, capabilities, CPU count, VM count, local image count, and local image refs; controller stores the host record. |
| heartbeat | `POST /v1/workers/heartbeat` | `coved` refreshes `last_seen` and capacity. |
| await-assignment | `GET /v1/workers/<id>/assignments` | `coved` polls for work; the controller leases one pending assignment and otherwise returns `204 No Content`. The daemon starts leased assignments asynchronously so long `cove` runs do not block later polls or heartbeats. |
| report-status | `POST /v1/workers/<id>/reports` | `coved` records `noop` as complete, executes `cove` assignments with bounded stdout/stderr capture, sends `running` renewals while `cove` is active, and reports other verbs as unsupported. |

Inventory endpoints:

```bash
curl http://127.0.0.1:9758/healthz
curl http://127.0.0.1:9758/v1/operations/summary
curl http://127.0.0.1:9758/v1/operations/summary?namespace=team-a
curl 'http://127.0.0.1:9758/v1/operations/runs?kind=storage.prune&limit=20'
curl 'http://127.0.0.1:9758/v1/operations/runs?target_type=image&target_id=macos-runner:latest'
curl 'http://127.0.0.1:9758/v1/operations/runs?image_manifest_digest=sha256:...&required_capability=ram-overlay&limit=20'
curl 'http://127.0.0.1:9758/v1/operations/runs?worker_id=mini-1&assignment_id=assignment-...&limit=20'
curl 'http://127.0.0.1:9758/v1/operations/runs?assignment_status=running&has_active_assignments=true&limit=20'
curl 'http://127.0.0.1:9758/v1/operations/runs?skip_reason=capability&missing_capability=ram-overlay&has_skips=true&limit=20'
curl http://127.0.0.1:9758/v1/operations/runs/image-prepare-...
curl http://127.0.0.1:9758/v1/workers
curl 'http://127.0.0.1:9758/v1/workers?status=ready&label=zone=desk&capability=ram-overlay&image_ref=macos-runner:latest&limit=50'
curl http://127.0.0.1:9758/v1/workers/mini-1
curl 'http://127.0.0.1:9758/v1/workers/mini-1/events?limit=50'
curl 'http://127.0.0.1:9758/v1/workers/mini-1/reports?limit=50'
curl 'http://127.0.0.1:9758/v1/workers/mini-1/metering?status=running'
curl 'http://127.0.0.1:9758/v1/workers/mini-1/sandboxes?status=ready&limit=50'
curl -X POST http://127.0.0.1:9758/v1/workers/mini-1/cordon \
  -H 'content-type: application/json' \
  -d '{"reason":"maintenance"}'
curl -X POST http://127.0.0.1:9758/v1/workers/mini-1/quarantine \
  -H 'content-type: application/json' \
  -d '{"reason":"bad disk"}'
curl -X POST http://127.0.0.1:9758/v1/workers/mini-1/evacuate \
  -H 'content-type: application/json' \
  -d '{"reason":"maintenance"}'
curl -X POST http://127.0.0.1:9758/v1/workers/mini-1/evacuate \
  -H 'content-type: application/json' \
  -d '{"reason":"maintenance","apply":true}'
curl -X POST http://127.0.0.1:9758/v1/workers/mini-1/drain \
  -H 'content-type: application/json' \
  -d '{"reason":"maintenance"}'
curl -X POST http://127.0.0.1:9758/v1/workers/mini-1/decommission \
  -H 'content-type: application/json' \
  -d '{"reason":"retired"}'
curl -X POST http://127.0.0.1:9758/v1/workers/mini-1/decommission \
  -H 'content-type: application/json' \
  -d '{"reason":"retired","force":true}'
curl -X POST http://127.0.0.1:9758/v1/workers/mini-1/uncordon
curl -X POST http://127.0.0.1:9758/v1/workers/mini-1/unquarantine
curl http://127.0.0.1:9758/v1/reconcile/plan
curl -X POST http://127.0.0.1:9758/v1/reconcile
```

`GET /v1/reconcile/plan` is the operator dry-run for the next reconcile pass.
It returns the same stale-worker, requeued assignment, replacement, expired
assignment, warm-pool-created, warm-pool-canceled, and warm-pool-cleanup fields as
`POST /v1/reconcile`, but computes them against a cloned controller snapshot,
so it does not persist changes or write audit events. Planned generated
assignment IDs reflect the snapshot time and can change if state changes before
the later apply. Like apply, the plan endpoint is fleet-global and requires an
unscoped operator/admin identity.

`GET /v1/workers` returns a paginated `workers` response with `count`,
`offset`, `limit`, and `next_offset`. It accepts `status`, `host`, `version`,
`image_ref`, `source_manifest_digest` or `image_manifest_digest`, repeated
`label=key=value`, repeated `capability=<name>`, `offset`, and `limit`.
Capability filters require the worker to report every requested capability.
Worker inventory is fleet-global, so scoped service-account tokens cannot read
it.

`GET /v1/workers/{id}/events` returns the worker-scoped slice of the
hash-chained controller audit feed. It includes worker lifecycle events plus
assignment, sandbox, and report events that carry the same `worker_id`;
`actor`, `action`, `target_type`, `target_id`, `sandbox_id`, `offset`, and
`limit` query filters match the global audit-list semantics. Worker event
history is fleet-global and requires an unscoped viewer/operator/admin
identity.

`GET /v1/workers/{id}/reports` returns persisted assignment worker reports for
that worker, including active renewals and terminal stdout/stderr/error/exit
details. It accepts `assignment_id`, `status`, `offset`, and `limit`. Worker
report history is fleet-global and requires an unscoped viewer/operator/admin
identity.

`GET /v1/workers/{id}/metering` returns the persisted sandbox active-interval
metering records for a worker plus the aggregate duration, VM, CPU, and memory
summary. It accepts `namespace`, `sandbox_id` or `sandbox`, and `status`; the
endpoint is fleet-global and requires an unscoped viewer/operator/admin
identity.

`GET /v1/workers/{id}/sandboxes` returns hosted sandbox handles placed on that
worker using the same paginated response shape as `GET /v1/sandboxes`. It
accepts `namespace`, `status`, `image_ref`, `offset`, and `limit`, and is
fleet-global like worker inventory, so scoped service-account tokens cannot
read it.

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

`POST /v1/workers/{id}/quarantine` isolates a suspected bad worker without
deleting its inventory. Quarantined workers keep heartbeat and report history,
but the controller excludes them from placement and returns `204 No Content`
from worker assignment polling even when a pending assignment names that worker
directly. `POST /v1/workers/{id}/unquarantine` clears the isolation state; if
the worker is still cordoned it remains unavailable for placement, but direct
assignments can be leased again. Quarantine and unquarantine record
`worker.quarantine` and `worker.unquarantine` audit events and require an
unscoped operator/admin identity.

`POST /v1/workers/{id}/evacuate` is the maintenance planner for a worker. By
default it is a dry run: the response lists each active or pending assignment,
the planned action, replacement candidates for movable pending work, hosted
sandbox drains, and blockers such as live non-sandbox assignments or pinned
work without a replacement. With `{"apply":true}`, the controller cordons the
worker, reassigns movable pending work to the best non-target placement
candidate, drains hosted sandboxes through the same cleanup path as sandbox
stop, and records `assignment.evacuate`, `sandbox.evacuate`, and
`worker.evacuate` audit events. Pending work is canceled only with
`{"force":true}` when there is no replacement candidate; active non-sandbox
work remains blocked.

`POST /v1/workers/{id}/decommission` removes an idle or already-drained worker
from controller inventory and records `worker.decommission`. By default it
refuses removal while any pending, leased, running, ready, claimed, draining,
or restarting assignment is still bound or leased to that worker; drain or let
those assignments finish first. With `{"force":true}`, decommission atomically
cancels pending assignments that are bound to the worker and have not been
leased yet, then removes the worker and returns their assignment ids in
`canceled`. Leased or already-started work still blocks removal; the HTTP
response is `409 Conflict` with `blocked`, and no pending assignment is
canceled. Force-canceled assignments also record `assignment.cancel` audit
events. A later heartbeat with the same worker id registers a fresh worker
record. Decommission is fleet-global and requires an unscoped operator/admin
identity.

`GET /v1/operations/summary` is the dashboard entry point for operators. It
reconciles first, then returns worker readiness/cordon/quarantine/stale counts
with attention workers and per-capability coverage, assignment status counts
with active assignments, hosted sandbox status counts with active and draining
handles, warm-pool desired/slot counts, retained controller-run counts by kind
with live assignment-status rollups, active runs, attention runs, skip-reason
counts, skipped-worker rollups, and aggregate sandbox metering. The
capability coverage section shows each reported worker capability, status
counts, and the workers carrying it, so operators can see whether traits such
as `ram-overlay`, `asif`, or `apfs-quota` are actually available before
admitting capability-constrained work. The optional `namespace` query filters
assignment, sandbox, warm-pool, and metering counts; worker inventory stays
fleet-global. Because the response includes fleet-global worker state, scoped
service-account tokens cannot read it.
`GET /v1/operations/runs` is the retained controller-run feed. It merges
placement plans, image preparations, image-GC runs, lifecycle-policy pushes,
and storage budget/prune runs into one paginated timeline with `kind`,
`target_type`, `target_id`, `source_ref`, `image_ref`,
`image_manifest_digest`, `image_digest_ref`, `image_platform`,
`required_capability`, `assignment_id`, `assignment_status`,
`has_active_assignments`, `worker_id`,
`candidate_worker_id`, `skipped_worker_id`, `skip_reason`,
`missing_capability`, `has_skips`, `offset`, and `limit` filters.
Scoped service-account tokens see only runs in their namespace. Run summaries
include the source run `id`, `kind`, creation time, assignment/skip/candidate
counts, common target fields, and kind-specific metadata in `fields`;
placement-plan run summaries count both feasible candidates and skipped
workers. The image provenance, capability, assignment, and worker filters
match summary or retained detail metadata when the source run carries it, so
operators can collapse placement, preparation, and maintenance history around a
specific immutable image, worker trait, assignment, or affected worker.
`GET /v1/operations/runs/{id}`
returns the same summary plus the retained source object under one of
`placement_plan`, `image_preparation`, `image_gc`, `lifecycle_policy`,
`storage_budget`, or `storage_prune`, giving dashboards a single drill-down
path from the aggregate timeline. The detail response also normalizes common
operator fields as `assignment_ids`, `assignments`, `assignment_statuses`,
`assignment_status_counts`, `active_assignment_ids`, `worker_ids`,
`candidate_worker_ids`, and `skipped_worker_ids`, so dashboards can render the
run's affected work and workers without first switching on the source kind.
The normalized assignment fields prefer the current assignment record when it is
still present, while the retained source object remains the original controller
run snapshot for audit and replay.

Service-account and audit endpoints:

```bash
curl -X POST http://127.0.0.1:9758/v1/service-accounts \
  -H 'content-type: application/json' \
  -d '{"name":"ci","namespace":"team-a","role":"operator","token":"local-secret"}'
curl http://127.0.0.1:9758/v1/service-accounts
curl -H 'authorization: Bearer local-secret' \
  -X POST http://127.0.0.1:9758/v1/assignments \
  -H 'content-type: application/json' \
  -d '{"id":"probe-1","worker_id":"mini-1","priority":10,"queue_ttl":"2m","verb":"noop"}'
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
    "subject":"ci@example.com",
    "sso_url":"https://idp.example/sso",
    "audience":"https://fleet.example/saml/acs",
    "namespace":"team-a",
    "role":"operator",
    "certificate_pem":"-----BEGIN CERTIFICATE-----\n...\n-----END CERTIFICATE-----"
  }'
curl -X POST http://127.0.0.1:9758/v1/saml-bindings \
  -H 'content-type: application/json' \
  -d '{
    "name":"okta-from-metadata",
    "metadata_url":"https://idp.example/metadata.xml",
    "audience":"https://fleet.example/saml/acs",
    "namespace":"team-a",
    "role":"operator"
  }'
curl http://127.0.0.1:9758/v1/saml-bindings
curl -X POST http://127.0.0.1:9758/v1/saml-bindings/okta-from-metadata/refresh
curl http://127.0.0.1:9758/v1/saml-bindings/okta/metadata
curl 'http://127.0.0.1:9758/v1/saml-bindings/okta/login?relay_state=cli'
curl -i 'http://127.0.0.1:9758/v1/saml-bindings/okta/login?redirect=true&relay_state=cli'
curl -X POST http://127.0.0.1:9758/v1/saml/acs \
  -H 'content-type: application/json' \
  -d '{"saml_response":"<base64-saml-response>","relay_state":"cli","ttl":"1h"}'
curl http://127.0.0.1:9758/v1/audit
curl http://127.0.0.1:9758/v1/audit?limit=50
curl 'http://127.0.0.1:9758/v1/audit?action=assignment.create&actor=service-account:ci&target_type=assignment&limit=50'
curl 'http://127.0.0.1:9758/v1/audit?worker_id=mini-1&limit=50'
curl 'http://127.0.0.1:9758/v1/audit?assignment_id=probe-1&limit=50'
curl 'http://127.0.0.1:9758/v1/audit?offset=50&limit=50'
curl http://127.0.0.1:9758/v1/audit/verify
curl -X DELETE http://127.0.0.1:9758/v1/service-accounts/ci
```

The controller persists audit events in the fleet store for high-value state
changes: worker registration, cordon/quarantine lifecycle, assignment
creation, assignment leases, assignment evacuation/reassignment, assignment
cancellation from forced decommission or evacuation, terminal assignment
reports, fleet reconcile changes, image/policy/storage fan-out, and warm-pool
ensure/claim/delete operations. Each new event carries `prev_hash` and `hash`
fields that chain the global audit log;
`GET /v1/audit/verify` recomputes the chain and returns `ok`, `events`,
`head_hash`, and any chain issues. `GET /v1/audit` returns `events`, `count`,
`offset`, `limit`, and `next_offset`; query filters include `namespace`,
`actor`, `action`, `target_type`, `target_id`, `worker_id`, `assignment_id`,
and `sandbox_id`. `limit` preserves the existing latest-events behavior, and
`offset` pages backward through matching events. Service-account tokens are stored only as
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
`/v1/saml-bindings` lets admin users store an IdP entity ID, optional subject,
SSO URL, service-provider audience, namespace, role, and PEM X.509 signing
certificate. Instead of hand-entering IdP fields, admins can provide
`metadata_xml` or `metadata_url`; the controller imports the IdP entity ID,
preferred HTTP-Redirect SSO URL, and signing certificate from the SAML metadata.
Bindings created with `metadata_url` persist the URL plus `metadata_fetched`,
and `POST /v1/saml-bindings/{name}/refresh` re-fetches that URL, rotates the
imported IdP fields, and records `saml_binding.refresh`. The controller
validates and persists the certificate and exposes only its SHA-256 fingerprint
in API responses. A request can authenticate with
`Authorization: Bearer saml:<base64-saml-xml>` when the payload is a signed
SAML response or assertion whose XML signature verifies against the binding
certificate, issuer matches `entity_id`, audience matches `audience`, optional
subject matches, and assertion conditions are currently valid. Matching
assertions record audit actor `saml:<binding-name>` and inherit the binding's
namespace and role. `GET /v1/saml-bindings/{name}/metadata` returns SAML 2.0 SP
metadata XML using the binding audience as the SP entity ID and HTTP-POST ACS
location, so IdP setup can be driven from the controller record.
`GET /v1/saml-bindings/{name}/login` creates an unsigned SAML 2.0
AuthnRequest, deflates and base64-encodes it for the HTTP-Redirect binding,
and returns the IdP redirect URL plus the request ID and XML. Add
`redirect=true` to receive a `302 Found` to the IdP SSO URL; `relay_state` or
`RelayState` is copied into the redirect query when present.
`POST /v1/saml/acs` accepts either JSON fields `saml_response` /
`saml_assertion` or browser-style form fields `SAMLResponse` /
`SAMLAssertion`, plus optional `RelayState` and `ttl`. It consumes the same
signed, replay-checked assertion path and returns a short-lived
`saml-session:` bearer token stored only as a SHA-256 hash. Requests made with
that session token record audit actor `saml-session:<binding-name>` and inherit
the binding's namespace and role. The controller records accepted assertion IDs
until their time window expires and rejects replayed assertions across process
restarts. Keep assertions short lived.
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
  -d '{"manifest_bundle":"manifests","image_ref":"macos-runner:latest","image_platform":"darwin/arm64","required_labels":{"zone":"desk"},"required_capabilities":["ram-overlay"]}'
curl -X POST http://127.0.0.1:9758/v1/images/prepare \
  -H 'content-type: application/json' \
  -d '{"manifest_bundle":"manifests","image_ref":"macos-runner:latest","image_platform":"darwin/arm64","required_capabilities":["ram-overlay"],"dry_run":true}'
curl 'http://127.0.0.1:9758/v1/images/preparations?image_manifest_digest=sha256:...&required_capability=ram-overlay&limit=20'
curl http://127.0.0.1:9758/v1/images/preparations/image-prepare-...
```

Image preparation creates one `cove image pull -tag <image_ref> <source_ref>`
assignment for each non-cordoned ready worker that matches `required_labels` and
`required_capabilities`, and does not already report `image_ref`. The
`skipped` list includes structured reasons for status, label, capability,
present-image, or active-prepare mismatches; label skips include
`missing_labels`, and capability skips include `missing_capabilities`. After a
successful image preparation assignment, `coved` sends an extra heartbeat so
the controller can place later `image-affinity` work against fresh image refs.
When `image_manifest_digest` is supplied, a worker counts as already prepared
only if its heartbeat reports the same `image_ref` and
`source_manifest_digest`; a stale mutable ref is refreshed with a forced pull.
The optional `image_digest_ref` and `image_platform` fields are stored on the
queued assignments and returned in the preparation result for offline bundle
audits.
`manifest_bundle` points at a bundle written by `cove image inspect -remote
-manifest-dir` or `cove pull --dry-run --fetch-manifest --manifest-dir`; the
controller verifies the bundle, selects `image_platform` when supplied,
populates the digest fields, and queues the pull from the selected digest ref
instead of the mutable source tag.
Each successful preparation response is persisted with `id`, `created`, label
selector, and required-capability selector, including skipped-only no-op runs.
Set `dry_run:true` to return the same planned pull assignments and skipped
workers without creating assignments, audit events, or retained preparation
history.
`GET /v1/images/preparations` returns
paginated preparation history with `source_ref`, `image_ref`,
`image_manifest_digest`, `image_digest_ref`, `image_platform`,
`required_capability`, `offset`, and `limit` filters; scoped service-account
tokens only see preparations in their namespace. The `GET
/v1/images/preparations/{id}` endpoint returns one retained preparation result
or `404` across namespace boundaries.

Image GC endpoint:

```bash
curl -X POST http://127.0.0.1:9758/v1/images/gc \
  -H 'content-type: application/json' \
  -d '{"required_labels":{"zone":"desk"},"required_capabilities":["ram-overlay"],"older_than":"168h","apply":true}'
curl -X POST http://127.0.0.1:9758/v1/images/gc \
  -H 'content-type: application/json' \
  -d '{"required_labels":{"zone":"desk"},"required_capabilities":["ram-overlay"],"older_than":"168h","apply":true,"dry_run":true}'
curl 'http://127.0.0.1:9758/v1/images/gc/runs?older_than=168h&apply=true&limit=20'
curl http://127.0.0.1:9758/v1/images/gc/runs/image-gc-...
```

Image GC creates one `cove image gc` assignment for each non-cordoned ready
worker that matches `required_labels` and `required_capabilities`. `apply`
defaults to false, which queues `cove image gc -dry-run`; set `apply:true` to
queue `cove image gc -yes`.
`older_than` is an optional Go duration string passed through as `-older-than`.
The `skipped` list includes structured reasons for status, label, capability,
or active image-GC mismatches; status skips include `status`, label skips
include `missing_labels`, and capability skips include
`missing_capabilities`. After a successful image-GC assignment, `coved` sends
an extra heartbeat so the controller's image refs reflect the post-GC store.
Each successful image-GC response is persisted with `id`, `created`, label
selector, required-capability selector, `older_than`, and `apply`, including
skipped-only no-op runs. Set `dry_run:true` to return planned assignments and
skipped workers without creating assignments, audit events, or retained GC
history. `GET /v1/images/gc/runs` returns paginated GC history with
`older_than`, `apply`, `offset`, and `limit` filters; scoped service-account
tokens only see runs in their namespace. The `GET /v1/images/gc/runs/{id}`
endpoint returns one retained GC result or `404` across namespace boundaries.

Lifecycle policy endpoint:

```bash
curl -X POST http://127.0.0.1:9758/v1/policies/lifecycle \
  -H 'content-type: application/json' \
  -d '{"vm_name":"ci-runner","required_labels":{"zone":"desk"},"required_capabilities":["ram-overlay"],"idle_timeout":"30m","max_age":"24h","run_budget":100}'
curl -X POST http://127.0.0.1:9758/v1/policies/lifecycle \
  -H 'content-type: application/json' \
  -d '{"vm_name":"ci-runner","required_labels":{"zone":"desk"},"required_capabilities":["ram-overlay"],"clear":true}'
curl -X POST http://127.0.0.1:9758/v1/policies/lifecycle \
  -H 'content-type: application/json' \
  -d '{"vm_name":"ci-runner","required_labels":{"zone":"desk"},"required_capabilities":["ram-overlay"],"idle_timeout":"30m","dry_run":true}'
curl 'http://127.0.0.1:9758/v1/policies/lifecycle/runs?vm_name=ci-runner&clear=false&limit=20'
curl http://127.0.0.1:9758/v1/policies/lifecycle/runs/lifecycle-policy-...
```

Lifecycle policy push creates one `cove policy <vm> set ...` assignment for
each non-cordoned ready worker that matches `required_labels` and
`required_capabilities`. The controller passes `idle_timeout` and `max_age` as
Go duration strings and `run_budget` as the guest exec count. `clear:true`
queues `cove policy <vm> clear` and cannot be combined with thresholds. Workers
that are excluded by status, label, capability, or an active lifecycle-policy
assignment for the same VM are returned in `skipped` with structured status,
`missing_labels`, or `missing_capabilities` details when applicable.
Each successful lifecycle-policy response is persisted with `id`, `created`,
the VM name, label selector, required-capability selector, clear/set mode,
thresholds, and queued assignment or skip details, including skipped-only
no-op runs. Set `dry_run:true` to return planned assignments and skipped
workers without creating assignments, audit events, or retained policy
history. `GET /v1/policies/lifecycle/runs` returns paginated policy history
with `vm_name`, `clear`, `offset`, and `limit` filters; scoped service-account
tokens only see runs in their namespace. The `GET
/v1/policies/lifecycle/runs/{id}` endpoint returns one retained policy result
or `404` across namespace boundaries.

Storage policy endpoints:

```bash
curl -X POST http://127.0.0.1:9758/v1/storage/budget \
  -H 'content-type: application/json' \
  -d '{"required_labels":{"zone":"desk"},"required_capabilities":["ram-overlay"],"target":"750GB","warn_pct":80,"hard_pct":95}'
curl -X POST http://127.0.0.1:9758/v1/storage/budget \
  -H 'content-type: application/json' \
  -d '{"required_labels":{"zone":"desk"},"required_capabilities":["ram-overlay"],"clear":true}'
curl -X POST http://127.0.0.1:9758/v1/storage/prune \
  -H 'content-type: application/json' \
  -d '{"required_labels":{"zone":"desk"},"required_capabilities":["ram-overlay"],"older_than":"168h","apply":true}'
curl -X POST http://127.0.0.1:9758/v1/storage/prune \
  -H 'content-type: application/json' \
  -d '{"required_labels":{"zone":"desk"},"required_capabilities":["ram-overlay"],"older_than":"168h","apply":true,"dry_run":true}'
curl 'http://127.0.0.1:9758/v1/storage/budget/runs?target=750GB&clear=false&limit=20'
curl http://127.0.0.1:9758/v1/storage/budget/runs/storage-budget-...
curl 'http://127.0.0.1:9758/v1/storage/prune/runs?older_than=168h&apply=true&limit=20'
curl http://127.0.0.1:9758/v1/storage/prune/runs/storage-prune-...
```

Storage budget push creates one `cove storage budget set -target <target>`
assignment per ready worker matching the requested labels and capabilities.
`warn_pct` and `hard_pct` are optional and default to the local CLI defaults
when omitted. `clear:true` queues
`cove storage budget clear` and cannot be combined with thresholds.
Storage prune push creates one `cove storage prune` assignment per matching
ready worker. It is dry-run by default; `apply:true` adds `-apply`, and
`older_than` is an optional Go duration string passed through as `-older-than`.
Set `category:"build-scratch"` to target `cove storage prune build-scratch`.
Workers that are excluded by status, label, capability, or an active storage
budget/prune assignment for the same operation are returned in `skipped` with
structured status, `missing_labels`, or `missing_capabilities` details when
applicable.
Each successful storage budget response is persisted with `id`, `created`,
label selector, required-capability selector, clear/set mode, target, warn and
hard percentages, and queued assignment or skip details, including
skipped-only no-op runs. Set `dry_run:true` to return planned budget
assignments and skipped workers without creating assignments, audit events, or
retained budget history. `GET /v1/storage/budget/runs` returns paginated
budget history with `target`, `clear`, `offset`, and `limit` filters; scoped
service-account tokens only see runs in their namespace. The `GET
/v1/storage/budget/runs/{id}` endpoint returns one retained budget result or
`404` across namespace boundaries.
Each successful storage prune response is persisted with `id`, `created`, label
selector, required-capability selector, category, `older_than`, `apply`, and
queued assignment or skip details, including skipped-only no-op runs. Set
`dry_run:true` to return planned prune assignments and skipped workers without
creating assignments, audit events, or retained prune history. `GET
/v1/storage/prune/runs` returns paginated prune history with `category`,
`older_than`, `apply`, `offset`, and `limit` filters; scoped service-account
tokens only see runs in their namespace. The `GET
/v1/storage/prune/runs/{id}` endpoint returns one retained prune result or
`404` across namespace boundaries.

Placement planning endpoint:

```bash
curl -X POST http://127.0.0.1:9758/v1/placements/plan \
  -H 'content-type: application/json' \
  -d '{"policy":"image-affinity","image_ref":"macos-runner:latest","manifest_bundle":"manifests","image_platform":"darwin/arm64","required_capabilities":["ram-overlay"],"anti_affinity_key":"ci/buildkite","resources":{"vms":1},"limit":5}'
curl 'http://127.0.0.1:9758/v1/placements/plans?policy=image-affinity&image_manifest_digest=sha256:...&required_capability=ram-overlay&limit=20'
curl http://127.0.0.1:9758/v1/placements/plans/placement-plan-...
```

Placement planning returns the retained ranked feasible workers without storing
an assignment. It uses the same policy, required-label, required-capability,
image-affinity, anti-affinity, and slot-cap logic as assignment creation.
`limit` defaults to five candidates. The response also includes skipped
workers with structured reasons for status, label, capability, capacity, or
exact-image mismatch, so a dry-run can explain why a `ram-overlay`, `asif`, or
`apfs-quota` request would not land before work is admitted.
If `image_manifest_digest` is set, the plan only includes workers whose
heartbeat reports that exact source manifest digest for the requested
`image_ref`; stale or unknown mutable refs are not warm candidates.
`manifest_bundle` may be used instead of hand-supplying digest fields; the
handler verifies the offline bundle and resolves the selected manifest digest
before ranking workers.
Each plan response is persisted with `id`, `created`, and the requested
candidate `limit`. `GET /v1/placements/plans` returns paginated plan history
with `policy`, `image_ref`, `image_manifest_digest`, `image_digest_ref`,
`image_platform`, `required_capability`, `offset`, and `limit` filters; scoped
service-account tokens only see plans in their namespace. The
`GET /v1/placements/plans/{id}` endpoint returns one retained plan or `404`
across namespace boundaries.

Warm-pool endpoint:

```bash
curl -X POST http://127.0.0.1:9758/v1/warm-pools \
  -H 'content-type: application/json' \
  -d '{"name":"runner-14","image_ref":"macos-runner:14.5","manifest_bundle":"manifests","image_platform":"darwin/arm64","size":3,"required_labels":{"zone":"desk"},"required_capabilities":["ram-overlay"],"resources":{"vms":1}}'
curl -X POST http://127.0.0.1:9758/v1/warm-pools/claim \
  -H 'content-type: application/json' \
  -d '{"name":"runner-14","command":["/bin/sh","-lc","make test"],"env":{"CI":"1"}}'
curl http://127.0.0.1:9758/v1/warm-pools
curl 'http://127.0.0.1:9758/v1/warm-pools?image_manifest_digest=sha256:...&required_capability=ram-overlay&limit=20'
curl http://127.0.0.1:9758/v1/warm-pools/runner-14
curl 'http://127.0.0.1:9758/v1/warm-pools/runner-14/events?limit=20'
curl -X DELETE http://127.0.0.1:9758/v1/warm-pools/runner-14
```

A warm pool persists a desired number of active fork slots for one image. The
controller reconciles missing slots into `cove` assignments using the normal
placement scheduler and anti-affinity key `warm-pool/<name>`. Each slot runs:

```bash
cove run -fork-from <image_ref> -fork-name <generated> -ephemeral -keep -headless
```

Warm pools accept the same optional `manifest_bundle`, `image_manifest_digest`,
`image_digest_ref`, `image_platform`, and `required_capabilities` fields as
assignments. With a manifest digest or bundle, the controller only replenishes
slots on workers that report the exact image provenance; with required
capabilities, replenishment only uses workers that report every requested
capability. Use `/v1/images/prepare` first to refresh stale mutable refs.

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
`GET /v1/warm-pools` returns `warm_pools`, `count`, `offset`, `limit`, and
`next_offset`, and accepts `namespace`, `image_ref`,
`image_manifest_digest`, `image_digest_ref`, `image_platform`,
`required_capability`, `offset`, and `limit` filters. Scoped service-account
tokens only see warm pools in their namespace.

`GET /v1/warm-pools/{name}/events` returns the warm-pool-scoped slice of the
hash-chained controller audit feed. It includes ensure, claim, and delete
events for the pool and accepts `actor`, `action`, `worker_id`, `assignment_id`,
`offset`, and `limit`; scoped service-account tokens can only read warm-pool
events in their namespace.

Sandbox endpoint:

```bash
curl -X POST http://127.0.0.1:9758/v1/sandboxes \
  -H 'content-type: application/json' \
  -d '{"id":"job-1","image_ref":"macos-runner:14.5","manifest_bundle":"manifests","image_platform":"darwin/arm64","required_labels":{"zone":"desk"},"required_capabilities":["ram-overlay"],"max_active_sandboxes":20,"priority":10,"queue_ttl":"2m","run_timeout":"45m","max_attempts":3,"retry_delay":"20s","args":["--net","nat"]}'
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
Create requests accept optional `manifest_bundle`, `image_manifest_digest`,
`image_digest_ref`, `image_platform`, `required_capabilities`,
`max_active_sandboxes`, `priority`, `queue_ttl`, `queue_expires`,
`run_timeout`, `max_attempts`, and `retry_delay` fields. The returned sandbox status and backing
assignment keep the resolved digest and capability fields, exact-digest
requests are admitted only onto workers that report the matching image
provenance, and capability-constrained requests are admitted only onto workers
that report every required capability. When `max_active_sandboxes` is greater
than zero, the controller reconciles current state and rejects the create
before placement if the request namespace already has that many non-terminal
sandbox run handles. `priority` is copied to the backing run assignment, so
urgent hosted sandboxes lease before older lower-priority pending work on the
selected worker. `queue_ttl` is a positive Go duration such as `2m`, while
`queue_expires` is an absolute timestamp; they are mutually exclusive. Pending
hosted sandbox work that misses its queue deadline is reconciled to `expired`
before worker lease and stops counting against namespace admission caps.
`run_timeout` is a positive Go duration that limits the backing `cove run`
assignment after lease; if omitted, `coved` uses its
`-fleet-assignment-timeout` value.
`max_attempts` is the total number of worker leases allowed for the backing run
assignment, including the first attempt; worker-reported `failed` reports are
automatically requeued while `attempt < max_attempts`. `retry_delay` is an
optional positive Go duration that delays the next lease and is surfaced as
`retry_at` on the assignment.

`GET /v1/sandboxes` accepts `namespace`, `status`, `worker_id`, `image_ref`,
`image_manifest_digest`, `image_digest_ref`, `image_platform`,
`required_capability`, `has_open_assignments`, `retrying`, `has_cleanup`,
`has_lease`, `lease_holder`, `offset`, and `limit` query parameters.
Namespace-scoped bearer tokens still force their own namespace, and
`offset`/`limit` must be non-negative. The digest and platform filters let
operators inventory handles by immutable registry provenance, and
`required_capability` matches handles that requested a capability such as
`ram-overlay`. The boolean `retrying` filter matches pending handles that have
already consumed at least one worker lease. `has_cleanup` finds handles with
active stop cleanup, while `has_lease` and `lease_holder` find currently
lease-held handles. Filters apply after reconciliation, then `offset` skips
matching handles and `limit` caps the response. The result includes
`sandboxes`, `count`, `offset`, `limit`, and `next_offset` when another page is
available, so clients can ask for ready handles, draining cleanup, retry
backoff, held leases, exact image digests, required capabilities, a single
worker, or a base image without fetching the whole controller inventory.

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
`release_lease`/`ReleaseLease` succeeds. Hosted create helpers expose the same
exact-image fields as the REST API: pass `manifest_bundle`/`ManifestBundle` or
the resolved digest fields to request a registry-verified mutable image ref,
pass `required_labels`/`RequiredLabels` for operator-defined selectors, and
pass `required_capabilities`/`RequiredCapabilities` to target workers that
advertise runtime traits such as `ram-overlay`, `asif`, or `apfs-quota`. The
same SDKs can dry-run hosted placement before creating a sandbox:
`agentsandbox.Plan` in Go and `CoveFleetClient.plan_sandbox` in Python return
the controller's feasible candidates and skipped-worker reasons. They also
expose retained placement-plan history, image-preparation, maintenance fan-out,
the operations dashboard summary, reconcile plan/apply controls, generic
assignment submission, worker/assignment inventory, scoped
worker/assignment observability, retained controller-run history, service-account
management, and fork warm-pool lifecycle controls, so
hosted agent clients can pre-stage exact images, push image GC/lifecycle/storage
policy work, inspect fleet pressure, preview and apply controller repairs,
queue one-off controller work, enumerate current work, drill into per-worker and
per-assignment timelines, verify audit-chain continuity, rotate
scoped controller credentials, inspect operations history, prewarm RAM-overlay
slots, claim a ready slot for a guest command, read pool events, and delete the
pool through the same fleet credentials.
Placement helpers include `Plan`, `ListPlacementPlans`, and `GetPlacementPlan`
for read-only admission checks plus the retained placement history that records
feasible candidates and skipped-worker diagnostics.
Maintenance helpers include `PushImageGC`, `PushLifecyclePolicy`,
`PushStorageBudget`, `PushStoragePrune`, their list/get run companions, and
`GetOperationsSummary` plus `ListControllerRuns` for the aggregate operations
dashboard and timeline. Reconcile helpers include `PlanReconcile` and
`Reconcile` for the same unscoped dry-run/apply repair path as
`/v1/reconcile/plan` and `/v1/reconcile`. Assignment helpers include
`CreateAssignment`, `ListAssignments`, and `GetAssignment`, with the same
generic submission path, priority, queue deadline, image-provenance filters,
capability filters, and pagination as the REST API. Worker
inventory helpers include `ListWorkers` and `GetWorker`. Assignment control
helpers include `CancelAssignment` and `RetryAssignment` for audited
force-cancel and retry/replan operations. Worker
lifecycle helpers include `CordonWorker`,
`UncordonWorker`, `QuarantineWorker`, `UnquarantineWorker`, `EvacuateWorker`,
`DrainWorker`, and `DecommissionWorker`, so hosted operators can plan or apply
maintenance without dropping to raw HTTP. Audit helpers include
`ListAuditEvents` for the filtered hash-chained feed and `VerifyAuditLog` for
global chain verification. Identity helpers include `ListServiceAccounts`,
`UpsertServiceAccount`, `DeleteServiceAccount`, `ListOIDCBindings`,
`UpsertOIDCBinding`, `DeleteOIDCBinding`, `ListSAMLBindings`,
`UpsertSAMLBinding`, `RefreshSAMLBinding`, `GetSAMLMetadata`,
`SAMLBindingLogin`, and `CreateSAMLSession` for scoped bearer-token bootstrap,
federated identity bindings, metadata export, and browserless SAML session
exchange. Scoped observability helpers include
`ListWorkerSandboxes`, `ListWorkerEvents`, `ListWorkerReports`,
`GetWorkerMetering`, `ListSandboxMetering`, `ListAssignmentEvents`,
`ListAssignmentReports`, and `GetAssignmentMetering`. Pass `DryRun` to maintenance pushes to inspect planned
assignments and structured skipped-worker diagnostics without mutating the
controller.

Go SDK example:

```go
ctx := context.Background()
prep, err := agentsandbox.PrepareImage(ctx, agentsandbox.ImagePrepareOptions{
	FleetURL:             "https://fleet.internal.example",
	APIKey:               os.Getenv("COVE_API_KEY"),
	Namespace:            "team-a",
	ImageRef:             "macos-base:latest",
	ManifestBundle:       "manifests",
	ImagePlatform:        "darwin/arm64",
	RequiredLabels:       map[string]string{"zone": "desk"},
	RequiredCapabilities: []string{"ram-overlay"},
	DryRun:               true,
})
if err != nil {
	log.Fatal(err)
}
log.Printf("image prepare assignments=%d skipped=%d", len(prep.Assignments), len(prep.Skipped))

prune, err := agentsandbox.PushStoragePrune(ctx, agentsandbox.StoragePruneOptions{
	FleetURL:             "https://fleet.internal.example",
	APIKey:               os.Getenv("COVE_API_KEY"),
	Namespace:            "team-a",
	RequiredLabels:       map[string]string{"zone": "desk"},
	RequiredCapabilities: []string{"ram-overlay"},
	Category:             "build-scratch",
	OlderThan:            "168h",
	Apply:                true,
	DryRun:               true,
})
if err != nil {
	log.Fatal(err)
}
log.Printf("storage prune assignments=%d skipped=%d", len(prune.Assignments), len(prune.Skipped))

summary, err := agentsandbox.GetOperationsSummary(ctx, agentsandbox.OperationsSummaryOptions{
	FleetURL:  "https://fleet.internal.example",
	APIKey:    os.Getenv("COVE_API_KEY"),
	Namespace: "team-a",
})
if err != nil {
	log.Fatal(err)
}
log.Printf("ready workers=%d active sandboxes=%d active controller runs=%d", summary.Workers.Ready, summary.Sandboxes.Active, summary.ControllerRuns.Active)

reconcilePlan, err := agentsandbox.PlanReconcile(ctx, agentsandbox.ReconcileOptions{
	FleetURL: "https://fleet.internal.example",
	APIKey:   os.Getenv("COVE_API_KEY"),
})
if err != nil {
	log.Fatal(err)
}
log.Printf("reconcile stale=%d requeue=%d expired=%d", len(reconcilePlan.StaleWorkers), len(reconcilePlan.RequeuedAssignments), len(reconcilePlan.ExpiredAssignments))

reconciled, err := agentsandbox.Reconcile(ctx, agentsandbox.ReconcileOptions{
	FleetURL: "https://fleet.internal.example",
	APIKey:   os.Getenv("COVE_API_KEY"),
})
if err != nil {
	log.Fatal(err)
}
log.Printf("reconciled requeue=%d expired=%d warm-cleanup=%d", len(reconciled.RequeuedAssignments), len(reconciled.ExpiredAssignments), len(reconciled.WarmPoolCleanup))

workers, err := agentsandbox.ListWorkers(ctx, agentsandbox.WorkerListOptions{
	FleetURL:             "https://fleet.internal.example",
	APIKey:               os.Getenv("COVE_API_KEY"),
	Status:               "ready",
	ImageRef:             "macos-base:latest",
	SourceManifestDigest: "sha256:...",
	Labels:               map[string]string{"zone": "desk"},
	Capabilities:         []string{"ram-overlay"},
	Limit:                20,
})
if err != nil {
	log.Fatal(err)
}
log.Printf("matching workers=%d", workers.Count)

created, err := agentsandbox.CreateAssignment(ctx, agentsandbox.AssignmentCreateOptions{
	FleetURL:             "https://fleet.internal.example",
	APIKey:               os.Getenv("COVE_API_KEY"),
	Namespace:            "team-a",
	Policy:               "bin-pack",
	ImageRef:             "macos-base:latest",
	ManifestBundle:       "manifests",
	RequiredCapabilities: []string{"ram-overlay"},
	Resources:            agentsandbox.Capacity{VMs: 1},
	Priority:             10,
	QueueTTL:             2 * time.Minute,
	MaxAttempts:          3,
	RetryDelay:           20 * time.Second,
	Verb:                 "cove",
	Args:                 []string{"run", "-fork-from", "macos-base:latest", "-ephemeral"},
})
if err != nil {
	log.Fatal(err)
}
log.Printf("queued assignment=%s worker=%s", created.ID, created.WorkerID)

sandboxMetering, err := agentsandbox.ListSandboxMetering(ctx, agentsandbox.SandboxMeteringOptions{
	FleetURL:  "https://fleet.internal.example",
	APIKey:    os.Getenv("COVE_API_KEY"),
	Namespace: "team-a",
	SandboxID: "job-123",
})
if err != nil {
	log.Fatal(err)
}
log.Printf("sandbox metered records=%d", sandboxMetering.Summary.Records)

assignments, err := agentsandbox.ListAssignments(ctx, agentsandbox.AssignmentListOptions{
	FleetURL:            "https://fleet.internal.example",
	APIKey:              os.Getenv("COVE_API_KEY"),
	Namespace:           "team-a",
	Status:              "running",
	WorkerID:            "mini-1",
	ImageManifestDigest: "sha256:...",
	RequiredCapability:  "ram-overlay",
	Limit:               20,
})
if err != nil {
	log.Fatal(err)
}
log.Printf("running assignments=%d", assignments.Count)

workerEvents, err := agentsandbox.ListWorkerEvents(ctx, agentsandbox.WorkerEventListOptions{
	FleetURL: "https://fleet.internal.example",
	APIKey:   os.Getenv("COVE_API_KEY"),
	ID:       "mini-1",
	Limit:    20,
})
if err != nil {
	log.Fatal(err)
}
assignmentReports, err := agentsandbox.ListAssignmentReports(ctx, agentsandbox.AssignmentReportListOptions{
	FleetURL: "https://fleet.internal.example",
	APIKey:   os.Getenv("COVE_API_KEY"),
	ID:       "assignment-123",
	Limit:    20,
})
if err != nil {
	log.Fatal(err)
}
log.Printf("worker events=%d assignment reports=%d", workerEvents.Count, assignmentReports.Count)

retry, err := agentsandbox.RetryAssignment(ctx, agentsandbox.AssignmentRetryOptions{
	FleetURL: "https://fleet.internal.example",
	APIKey:   os.Getenv("COVE_API_KEY"),
	ID:       "assignment-123",
	Reason:   "transient host failure",
	Replan:   true,
})
if err != nil {
	log.Fatal(err)
}
log.Printf("retried assignment=%s worker=%s", retry.Assignment.ID, retry.Assignment.WorkerID)

evacuation, err := agentsandbox.EvacuateWorker(ctx, agentsandbox.WorkerEvacuationOptions{
	FleetURL: "https://fleet.internal.example",
	APIKey:   os.Getenv("COVE_API_KEY"),
	ID:       "mini-1",
	Reason:   "maintenance",
})
if err != nil {
	log.Fatal(err)
}
log.Printf("evacuation plan assignments=%d blocked=%d", len(evacuation.Assignments), len(evacuation.Blocked))

drain, err := agentsandbox.DrainWorker(ctx, agentsandbox.WorkerLifecycleOptions{
	FleetURL: "https://fleet.internal.example",
	APIKey:   os.Getenv("COVE_API_KEY"),
	ID:       "mini-1",
	Reason:   "maintenance",
})
if err != nil {
	log.Fatal(err)
}
log.Printf("drained sandboxes=%d skipped=%d", len(drain.Sandboxes), len(drain.Skipped))

hasActive := true
hasSkips := true
runs, err := agentsandbox.ListControllerRuns(ctx, agentsandbox.ControllerRunListOptions{
	FleetURL:             "https://fleet.internal.example",
	APIKey:               os.Getenv("COVE_API_KEY"),
	Namespace:            "team-a",
	Kind:                 "image.prepare",
	ImageManifestDigest:  "sha256:...",
	RequiredCapability:   "ram-overlay",
	AssignmentStatus:     "running",
	HasActiveAssignments: &hasActive,
	SkipReason:           "capability",
	MissingCapability:    "ram-overlay",
	HasSkips:             &hasSkips,
	WorkerID:             "mini-1",
	Limit:                20,
})
if err != nil {
	log.Fatal(err)
}
log.Printf("controller runs=%d", runs.Count)
if runs.Count > 0 {
	detail, err := agentsandbox.GetControllerRun(ctx, agentsandbox.ControllerRunGetOptions{
		FleetURL: "https://fleet.internal.example",
		APIKey:   os.Getenv("COVE_API_KEY"),
		ID:       runs.Runs[0].ID,
	})
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("controller run %s kind=%s", detail.Summary.ID, detail.Summary.Kind)
}

plans, err := agentsandbox.ListPlacementPlans(ctx, agentsandbox.PlacementPlanListOptions{
	FleetURL:            "https://fleet.internal.example",
	APIKey:              os.Getenv("COVE_API_KEY"),
	Namespace:           "team-a",
	Policy:              "image-affinity",
	ImageRef:            "macos-base:latest",
	ImageManifestDigest: "sha256:...",
	RequiredCapability:  "ram-overlay",
	Limit:               20,
})
if err != nil {
	log.Fatal(err)
}
log.Printf("placement plans=%d", plans.Count)

planHistory, err := agentsandbox.GetPlacementPlan(ctx, agentsandbox.PlacementPlanGetOptions{
	FleetURL: "https://fleet.internal.example",
	APIKey:   os.Getenv("COVE_API_KEY"),
	ID:       "placement-plan-123",
})
if err != nil {
	log.Fatal(err)
}
log.Printf("placement plan candidates=%d skipped=%d", len(planHistory.Candidates), len(planHistory.Skipped))

audit, err := agentsandbox.ListAuditEvents(ctx, agentsandbox.AuditListOptions{
	FleetURL:     "https://fleet.internal.example",
	APIKey:       os.Getenv("COVE_API_KEY"),
	Namespace:    "team-a",
	Action:       "assignment.create",
	AssignmentID: "assignment-123",
	Limit:        20,
})
if err != nil {
	log.Fatal(err)
}
verify, err := agentsandbox.VerifyAuditLog(ctx, agentsandbox.AuditVerifyOptions{
	FleetURL: "https://fleet.internal.example",
	APIKey:   os.Getenv("COVE_API_KEY"),
})
if err != nil {
	log.Fatal(err)
}
log.Printf("audit events=%d chain_ok=%t", audit.Count, verify.OK)

accounts, err := agentsandbox.ListServiceAccounts(ctx, agentsandbox.ServiceAccountListOptions{
	FleetURL:  "https://fleet.internal.example",
	APIKey:    os.Getenv("COVE_API_KEY"),
	Namespace: "team-a",
})
if err != nil {
	log.Fatal(err)
}
rotated, err := agentsandbox.UpsertServiceAccount(ctx, agentsandbox.ServiceAccountUpsertOptions{
	FleetURL:  "https://fleet.internal.example",
	APIKey:    os.Getenv("COVE_API_KEY"),
	Name:      "ci",
	Namespace: "team-a",
	Role:      "operator",
	Token:     os.Getenv("COVE_CI_TOKEN"),
})
if err != nil {
	log.Fatal(err)
}
log.Printf("service accounts=%d rotated=%s", accounts.Count, rotated.ServiceAccount.Name)

oidc, err := agentsandbox.UpsertOIDCBinding(ctx, agentsandbox.OIDCBindingUpsertOptions{
	FleetURL:  "https://fleet.internal.example",
	APIKey:    os.Getenv("COVE_API_KEY"),
	Name:      "github-main",
	Issuer:    "https://token.actions.githubusercontent.com",
	Subject:   "repo:tmc/cove:ref:refs/heads/main",
	Audience:  "cove-fleet",
	Namespace: "team-a",
	Role:      "operator",
	JWKSURL:   "https://token.actions.githubusercontent.com/.well-known/jwks",
})
if err != nil {
	log.Fatal(err)
}
samlLogin, err := agentsandbox.SAMLBindingLogin(ctx, agentsandbox.SAMLBindingLoginOptions{
	FleetURL:   "https://fleet.internal.example",
	APIKey:     os.Getenv("COVE_API_KEY"),
	Name:       "okta",
	RelayState: "cli",
})
if err != nil {
	log.Fatal(err)
}
metadata, err := agentsandbox.GetSAMLMetadata(ctx, agentsandbox.SAMLBindingNameOptions{
	FleetURL: "https://fleet.internal.example",
	APIKey:   os.Getenv("COVE_API_KEY"),
	Name:     "okta",
})
if err != nil {
	log.Fatal(err)
}
log.Printf("oidc=%s saml_redirect=%s metadata=%d", oidc.Binding.Name, samlLogin.RedirectURL, len(metadata))

pool, err := agentsandbox.EnsureWarmPool(ctx, agentsandbox.WarmPoolOptions{
	FleetURL:             "https://fleet.internal.example",
	APIKey:               os.Getenv("COVE_API_KEY"),
	Namespace:            "team-a",
	Name:                 "runner-14",
	ImageRef:             "macos-base:latest",
	ManifestBundle:       "manifests",
	ImagePlatform:        "darwin/arm64",
	Size:                 3,
	RequiredLabels:       map[string]string{"zone": "desk"},
	RequiredCapabilities: []string{"ram-overlay"},
	Resources:            agentsandbox.Capacity{VMs: 1},
})
if err != nil {
	log.Fatal(err)
}
log.Printf("warm pool ready=%d active=%d", pool.Pool.Ready, pool.Pool.Active)
poolPage, err := agentsandbox.ListWarmPoolsPage(ctx, agentsandbox.WarmPoolListOptions{
	FleetURL:            "https://fleet.internal.example",
	APIKey:              os.Getenv("COVE_API_KEY"),
	Namespace:           "team-a",
	ImageRef:            "macos-base:latest",
	ImagePlatform:       "darwin/arm64",
	RequiredCapability:  "ram-overlay",
	Limit:               20,
})
if err != nil {
	log.Fatal(err)
}
log.Printf("warm pool page count=%d next=%d", poolPage.Count, poolPage.NextOffset)

plan, err := agentsandbox.Plan(ctx, agentsandbox.ClientOptions{
	Provider:             agentsandbox.ProviderCloud,
	FleetURL:             "https://fleet.internal.example",
	APIKey:               os.Getenv("COVE_API_KEY"),
	ImageRef:             "macos-base:latest",
	ManifestBundle:       "manifests",
	ImagePlatform:        "darwin/arm64",
	RequiredLabels:       map[string]string{"zone": "desk"},
	RequiredCapabilities: []string{"ram-overlay"},
	PlacementLimit:       5,
})
if err != nil {
	log.Fatal(err)
}
log.Printf("placement candidates=%d skipped=%d", len(plan.Candidates), len(plan.Skipped))
claim, err := agentsandbox.ClaimWarmPool(ctx, agentsandbox.WarmPoolClaimOptions{
	FleetURL:  "https://fleet.internal.example",
	APIKey:    os.Getenv("COVE_API_KEY"),
	Namespace: "team-a",
	Name:      "runner-14",
	Command:   []string{"/bin/sh", "-lc", "make test"},
})
if err != nil {
	log.Fatal(err)
}
log.Printf("claimed warm vm %s on %s", claim.VMName, claim.Assignment.WorkerID)

sb, err := agentsandbox.Create(ctx, agentsandbox.ClientOptions{
	Provider: agentsandbox.ProviderCloud,
	FleetURL: "https://fleet.internal.example",
	APIKey:   os.Getenv("COVE_API_KEY"),
	ImageRef: "macos-base:latest",
	ManifestBundle: "manifests",
	ImagePlatform:  "darwin/arm64",
	RequiredLabels: map[string]string{"zone": "desk"},
	RequiredCapabilities: []string{"ram-overlay"},
	MaxActiveSandboxes:   20,
	Priority:             10,
	QueueTTL:             2 * time.Minute,
	MaxAttempts:          3,
	RetryDelay:           20 * time.Second,
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
  -d '{"id":"probe-1","worker_id":"mini-1","priority":10,"queue_ttl":"2m","run_timeout":"5m","max_attempts":3,"retry_delay":"20s","verb":"noop"}'
curl -X POST http://127.0.0.1:9758/v1/assignments \
  -H 'content-type: application/json' \
  -d '{"id":"run-1","worker_id":"mini-1","verb":"cove","args":["run","-ephemeral","-headless"]}'
curl -X POST http://127.0.0.1:9758/v1/assignments \
  -H 'content-type: application/json' \
  -d '{"id":"placed-1","policy":"image-affinity","image_ref":"macos-runner:latest","manifest_bundle":"manifests","image_platform":"darwin/arm64","verb":"cove","args":["run","-fork-from","macos-runner:latest","-ephemeral"]}'
curl -X POST http://127.0.0.1:9758/v1/assignments \
  -H 'content-type: application/json' \
  -d '{"id":"packed-1","policy":"bin-pack","anti_affinity_key":"ci/buildkite","resources":{"vms":1},"verb":"cove","args":["run","-ephemeral","-headless"]}'
curl http://127.0.0.1:9758/v1/assignments
curl 'http://127.0.0.1:9758/v1/assignments?status=failed&worker_id=mini-1&limit=50'
curl 'http://127.0.0.1:9758/v1/assignments?verb=cove&image_ref=macos-runner:latest&offset=50&limit=50'
curl http://127.0.0.1:9758/v1/assignments/probe-1
curl 'http://127.0.0.1:9758/v1/assignments/probe-1/events?limit=50'
curl 'http://127.0.0.1:9758/v1/assignments/probe-1/reports?limit=50'
curl 'http://127.0.0.1:9758/v1/assignments/probe-1/metering?status=running'
curl -X POST http://127.0.0.1:9758/v1/assignments/probe-1/cancel \
  -H 'content-type: application/json' \
  -d '{"reason":"bad input"}'
curl -X POST http://127.0.0.1:9758/v1/assignments/probe-1/retry \
  -H 'content-type: application/json' \
  -d '{"reason":"transient host issue","replan":true}'
```

Assignments are stored with `pending`, `leased`, `running`, `ready`, `claimed`,
`draining`, `canceled`, or worker-reported terminal status. `ready` is used for
a warm-pool slot whose guest agent accepted a probe through `cove shell`;
`claimed` is used for a ready warm-pool slot that has been handed to a job, and
`draining` is used for a surplus warm slot while its stop assignment is pending.
A claimed slot still consumes host capacity but is no longer counted as an
available warm slot. `coved` renews active `cove` assignments with `running` or
`ready` reports. Claimed warm-pool guest-exec assignments stop the claimed VM
after the guest command returns. `priority` is optional and non-negative; worker
polls lease higher-priority pending assignments before older lower-priority
assignments, with creation time and assignment id as stable tie-breakers.
`queue_ttl` and `queue_expires` are optional, mutually exclusive queue
deadlines. `queue_ttl` must be a positive Go duration, `queue_expires` must be
a future timestamp, and pending assignments that miss the deadline are
reconciled to `expired` before lease; worker lease clears the queue deadline.
`run_timeout` is an optional positive Go duration that overrides
`coved -fleet-assignment-timeout` for the leased `cove` or `cove-control`
assignment.
`attempt` increments on every worker lease. If `max_attempts` is greater than
one and a worker reports `failed` before the limit is reached, the controller
automatically clears the lease and requeues the assignment as `pending`; when
`retry_delay` is set, workers skip the assignment until `retry_at`.
`GET /v1/assignments` returns a paginated `assignments` response with `count`,
`offset`, `limit`, and `next_offset`. It accepts `status`, `worker_id`,
`leased_to`, `verb`, `image_ref`, `image_manifest_digest`, `image_digest_ref`,
`image_platform`, `required_capability`, `sandbox_id` or `sandbox`,
`warm_pool`, `offset`, and `limit`; scoped service-account tokens are still
limited to their namespace, and unscoped callers can use the existing
`namespace` query.
`GET /v1/assignments/{id}/events` returns the assignment-scoped slice of the
hash-chained controller audit feed. It includes create, lease, report, cancel,
retry, evacuation, and reassignment events carrying the same `assignment_id`;
`actor`, `action`, `target_type`, `target_id`, `worker_id`, `sandbox_id`,
`offset`, and `limit` filters match the global audit-list semantics. Scoped
service-account tokens can only read assignment events in their namespace.
`GET /v1/assignments/{id}/reports` returns the persisted worker report stream
for the assignment, including active `running` or `ready` renewals and terminal
status reports with captured stdout, stderr, error, and exit code. It accepts
`worker_id`, `status`, `offset`, and `limit`, and is namespace-scoped like the
assignment itself.
`GET /v1/assignments/{id}/metering` returns persisted sandbox active-interval
metering records for the assignment plus aggregate duration, VM, CPU, and
memory summaries. It accepts `status` and is namespace-scoped like the
assignment itself.
Reconciliation marks expired workers stale, requeues expired assignment leases,
rejects late reports for reclaimed leases, and can move a policy-placed
assignment from a stale worker to another ready worker.
`POST /v1/assignments/{id}/cancel` gives operators a precise stuck-work control:
pending unleased assignments can be marked `canceled` directly, while leased,
running, ready, claimed, draining, or restarting assignments require
`{"force":true}` because the controller is only changing its assignment state.
Hosted sandbox run assignments are rejected by this endpoint; use sandbox
stop/delete so the controller can create the required cleanup assignment.
`coved` observes forced cancellation while a `cove` or `cove-control`
assignment is active, stops the local command or control request, and sends a
terminal `canceled` report; a late non-canceled worker report is rejected
because the lease has already been cleared.
Cancellation is audited as `assignment.cancel` and scoped service-account
tokens can only cancel assignments in their namespace.
`POST /v1/assignments/{id}/retry` requeues a terminal generic assignment with
the same assignment id, clears stale lease/report state, and records
`assignment.retry`. It preserves the previous worker by default; set
`replan:true` to run the normal placement policy again, or set `worker_id` to
pin the retry to a registered worker. Hosted sandbox and warm-pool assignments
are rejected because their retry semantics are owned by sandbox
start/restart/delete and warm-pool reconciliation.

Cordoned workers keep heartbeating and reporting, but controller placement
skips them for unbound and policy-placed assignments. Explicit `worker_id`
assignments can still target a cordoned worker. Quarantined workers also keep
heartbeating and reporting, but they do not receive new assignment leases until
unquarantined, including explicit `worker_id` assignments.

When `worker_id` is empty and `policy` is set, the controller places the
assignment before storing it:

| Policy | Placement |
|--------|-----------|
| `least-loaded` | Choose the non-cordoned, non-quarantined ready worker with the lowest VM count plus pending assignment count. |
| `image-affinity` | Prefer a non-cordoned, non-quarantined ready worker that already reports `image_ref`; fall back to least-loaded. If `image_manifest_digest` is set, only workers that report the matching source manifest digest for that ref are feasible. |
| `bin-pack` | Choose the densest non-cordoned, non-quarantined ready worker that still fits the assignment's `resources.vms` under the worker's `max_vms` slot cap. |

`required_labels` can restrict placement to workers with exact matching labels;
`required_capabilities` restricts placement to workers that report every named
capability. Explicit `worker_id` assignments that include
`required_capabilities` are rejected when the target worker lacks a required
capability. Workers report current VM count as `vms`; `coved` defaults
`max_vms` to host CPU count. Assignment `resources.vms` defaults to one
scheduling slot when omitted. Set `anti_affinity_key` to spread active
assignments for the same job, base, or replica group across workers.
`image-affinity` still prefers a warm worker before applying the anti-affinity
tie-break.
Worker heartbeats include both legacy `image_refs` and `image_details` entries
with optional `source_manifest_digest`, so mutable tags can be scheduled with a
digest-exact provenance check.
Assignment creation and placement planning also accept `manifest_bundle`; the
HTTP handler verifies it and stores only the resolved digest identity on the
assignment or plan response.
`POST /v1/placements/plan` exposes the same ranking as a read-only top-k plan.

Register a worker record manually:

```bash
curl -X POST http://127.0.0.1:9758/v1/workers/register \
  -H 'content-type: application/json' \
  -d '{"id":"mini-1","host":"mini.local","version":"dev","capabilities":["ram-overlay","asif"],"image_refs":["macos-runner:latest"],"image_details":[{"ref":"macos-runner:latest","source_manifest_digest":"sha256:..."}],"cpus":12,"max_vms":8,"memory_bytes":68719476736}'
```

This surface is intentionally private and local-first. It now has basic
controller reconciliation, worker cordon/quarantine/evacuate/drain/decommission
lifecycle, fleet image preparation, fleet image-GC push, lifecycle-policy push
with retained history, storage budget/prune push with retained history,
retained placement plans, and a first fork warm-pool
quota reconciler with agent-ready slot claim and guest `Exec` handoff through
the `cove shell` path plus claimed-slot stop and downsize cleanup, plus a
persistent fleet audit feed.
