# Design 046: Fleet Control Plane (open-core, north-star + slices)

Status: Roadmap input, 2026-05-29. NotebookLM-backed design (notebook
`a455776b-a71b-4a69-80ea-ef708d058fa1`, seeded with cove designs 013/024/025/026/031/032/033/034/038/040,
the current `internal/fleet` + `internal/coved` code surface, the competitive
matrix, and cited prior-art digests for Orchard/Tart, Lume/Cua, Daytona, Nomad,
fly.io flyd, and open-core licensing). Every cove claim below was verified
against `origin/main`; every competitor claim carries a citation. This doc is a
direction, not an accepted slice contract — the v0.4 fleet that already shipped
(design 034) is the baseline it builds on.

Author: Travis Cline
Date: 2026-05-29

## Problem

cove already has a *fleet surface*, but not a *fleet control plane*. Design 034
shipped a deliberately stateless multi-host layer:

- SSH-tunnel routing to remote per-VM control sockets (`~/.vz/fleet.json`,
  `internal/fleet/ssh.go`, `fleet_cli.go`).
- Parallel read-aggregation: `cove fleet vm ls / image ls / ps / metrics`
  (`internal/fleet/query.go`, `fleet_aggregate_cli.go`), fail-soft with an
  `(unreachable)` row per dead host.
- Image push/pull/sync host-to-host over SSH (`internal/fleet/image_transfer.go`).
- Least-loaded run placement (`internal/fleet/placement.go:SelectLeastLoadedHost`,
  `fleet_run.go`).

And a single-host daemon, `coved` (design 033): lifecycle-policy enforcement by
scanning `~/.vz/vms/*/control.sock` (`internal/coved/lifecycle.go:114`),
scheduled image GC (`internal/coved/image_gc.go:372`), storage-budget polling
(design 040), and an observability stack. Its socket is `~/.vz/cove.sock`
(`cmd/coved/main.go:162`). `coved` is explicitly **single-host** and design 033
says it must remain "distinct from" fleet routing (`033:186`).

Design 034 explicitly **defers** everything that makes a control plane:
scheduler/placement intelligence, fleet-wide policy, "health checks and richer
host inventory", concurrent multi-host run, SSH connection pooling, a native Go
SSH client, and "state replication or leader election" (034 `## Deferred`).

The user now wants the next horizon, with three decisions already made:

1. **Use cases (all three):** a CI/agent-runner pool across many Macs; a team's
   self-hosted dev VMs on shared Mac hardware; and eventually a hosted/managed
   sandbox-by-API offering.
2. **Packaging: open-core.** The single-host core stays MIT inside cove
   (`LICENSE`: MIT, Travis Cline 2025). Multi-host orchestration ships as a
   separate / paid layer.
3. **Horizon: both** — a north-star control-plane design *and* the incremental
   slices that reach it without painting cove into a corner.

## Non-negotiable invariants (carried forward)

These come from cove's existing designs and are load-bearing for everything
below. They were verified, not assumed.

- **Host-local state is authoritative.** Design 034 already states aggregate
  commands "keep host-local state authoritative" and the failure boundary is one
  SSH command per host. fly.io's `flyd` independently validates this at real
  scale: a serious multi-region orchestrator runs with **no consensus and no
  leader between hosts**, each host authoritative for its own machines
  ([fly.io carving-the-scheduler-out](https://fly.io/blog/carving-the-scheduler-out-of-our-orchestrator/)).
  We treat this as a permanent invariant, not a temporary simplification.
- **Hard isolation is fork/restore, never soft reset.** Design 015 proved soft
  reset leaks across a warm guest (0 pass / 3 fail; System Keychain,
  GlobalPreferences, TCC residue), so the only supported tenant boundary is a
  fresh APFS-clonefile fork (design 013). Fork-only is ~132–140 ms on M4 for a
  60 GiB parent (`bench/fork-time/results-20260427.md`).
- **vsock+gRPC is the canonical guest-control path, not SSH** (ROADMAP "Wedges
  to protect", `:257`). The fleet layer must not regress cove into "SSH is how
  you talk to a guest."
- **Simplest correct design.** Nomad-simple, not Kubernetes. We borrow shapes
  from Nomad/Orchard/flyd but defer Raft/Serf/etcd until a real HA requirement
  exists.

## North-star topology: controller + dial-out workers

```text
[ PAID / SEPARATE FLEET REPO ]                  [ MIT / SINGLE-HOST cove REPO ]
                                    |
   +-----------------------------+  |   +------------------------------------+
   |  Fleet Controller           |  |   |  Mac Host (Worker)                 |
   |  (single binary, embedded   |  |   |                                    |
   |   SQLite/bbolt; SPOF)       |<====(worker dials OUT)== coved --fleet-url |
   |                             |  |   |          |                         |
   |  - host registry + heartbeat|  |   |          | (local fs + vsock)      |
   |  - placement scheduler      |  |   |          v                         |
   |  - fleet RBAC / SSO / audit |  |   |   ~/.vz/vms/<name>/control.sock     |
   |  - REST/RPC API + web UI    |  |   |   ~/.vz/images/<ref> (fork source)  |
   |  - metering (hosted only)   |  |   |                                    |
   +-----------------------------+  |   +------------------------------------+
            ^                       |
            | REST API (Bearer / SSO), 5-line SDK
        users / CI / agents
```

- **Workers dial OUT to the controller** and hold a long-lived stream; the
  controller pushes instructions down it. This is Orchard's verified model
  ([orchard `Watch()` gRPC stream](https://github.com/cirruslabs/orchard);
  [orchard SSH-over-gRPC post](https://github.com/cirruslabs/tart/blob/main/docs/blog/posts/2023-04-28-orchard-ssh-over-grpc.md))
  and the reason is decisive for cove: CI runners and shared team Macs are
  routinely behind NAT/firewalls, and dial-out means **the controller needs no
  inbound path to any host**. It also sidesteps macOS 15 Local Network
  permission prompts that Orchard's TCP-to-VM path hits, because cove pushes the
  command to `coved`, which talks to the guest over **vsock**, never TCP.
- **The worker is `coved` in a new mode, not a new process.** Nomad and Orchard
  both ship one binary whose role is chosen at runtime; `coved` is already the
  single-host authority. It gains a `--fleet-url`-style flag (name is an
  inference) to dial a controller and speak a small four-verb worker protocol —
  **register, heartbeat, poll/await-assignment, report-status** — lifted from
  Nomad ([Nomad architecture](https://developer.hashicorp.com/nomad/docs/architecture)).
  The worker stays *stateless about the rest of the fleet*: it never
  participates in consensus and holds only its own VM/image state.
- **Controller state is a single embedded KV/SQL store on one process.** This is
  Orchard's BadgerDB choice — simple, zero-ops, and an explicit single point of
  failure documented as "back up the data dir." The controller does **not**
  maintain a strongly-consistent global ledger of VM state; it stores fleet
  *configuration* (registered hosts, RBAC, SSO, scheduler policy) and treats
  live VM truth as something it materializes at query time from worker
  heartbeats and reports — the `flyd`/`flaps` split, where the write plane (the
  worker) is authoritative and the placement plane queries it.

### Why not Raft/Serf/etcd now

Nomad needs 3–5 servers + gossip because it targets datacenter-scale HA across
regions. cove is Apple-Silicon-only, single-operator-to-tens-of-hosts, usually
one LAN. We adopt Nomad's *shape* (leader computes placement; workers heartbeat)
but back it with one controller process + one durable file. **Escalate to Raft
only when a customer demands controller HA** — and even then, make each physical
site an independent island with no cross-site replication (Nomad's region model;
hashicorp/raft is a library bolt-on, not a rewrite). Until then, "the controller
is a SPOF, back it up" is the honest and correct posture, exactly as Orchard
ships.

## Scheduler & placement — where cove's edge lives

Two-phase feasibility-then-rank (Nomad), iterating sorted first-fit (Orchard).

- **Phase 1 — feasibility filter:** host online (recent heartbeat); free
  RAM + APFS quota headroom for the VM (reuse the design 032 quota/census
  signal); Apple-Silicon arch + macOS-version floor compatible (e.g. macOS 14
  for SCKit capture).
- **Phase 2 — rank:** bin-pack by default (densest host that still fits) plus a
  built-in **anti-affinity** term that spreads replicas of the same job/base so
  one host failure does not take out all forks of a base. Score the feasible set
  and keep a small top-k (Nomad's `MaxRetainedNodeScores = 5`); placement is
  O(k), not O(all-hosts).

cove's **unique scheduler dimension** — neither Orchard nor Daytona has it:

- **Base-image affinity.** A forked VM should land on a host that already holds
  the parent image in `~/.vz/images/`, because forking is a ~132–140 ms local
  APFS clonefile but a cold base means a multi-GB cross-host transfer. The
  scheduler applies a large affinity bonus for image-local hosts. When *no*
  feasible host has the image, the controller orchestrates a pre-flight
  `cove fleet image sync` (design 034 Slice 3 already streams images host-to-host
  over SSH) onto the chosen host before issuing the fork. This reuses shipped
  primitives instead of a central OCI registry. *(Inference: the sync-then-fork
  sequencing is a new control-plane behavior, not existing code.)*
- **Fork-warm-pool.** For the CI/agent and hosted use cases, let an operator
  declare a warm-pool quota ("keep 3 ready forks of `cove-runner-macos:14.5`
  across the fleet"). The controller directs workers to pre-fork and wait for the
  vsock agent to be reachable; on a job arrival it claims a ready VM and issues
  `Exec` immediately, then asks the worker to replenish. This mirrors Daytona's
  sub-90 ms `WarmPoolService`
  ([Daytona sandboxes](https://www.daytona.io/docs/en/sandboxes/)) but is
  *honest* about cove's two-part latency: fork-only is ~132–140 ms, but
  boot-to-agent on a base image is ~6–10 s — so the warm-pool's value is paying
  that 6–10 s ahead of time, not the fork.

  Current implementation note: `cove-fleetd` now has the first control-plane
  path for this shape: `coved` probes each warm slot through `cove shell` before
  marking it `ready`; the controller then claims that ready slot, marks it
  unavailable, queues same-worker guest execution, and the worker stops the
  claimed warm VM after the guest command returns. Pool status responses report
  open slots plus pending/leased/running/ready/claimed/draining/terminal counts,
  so operators can separate usable ready capacity from in-flight claims and
  cleanup.

**Correctness / serialization.** The one thing that must be serialized is the
final "commit this VM to this host" decision, so two placements cannot
oversubscribe one Mac's RAM. With a single controller process this is just an
in-memory critical section before the durable write — no plan queue, no
optimistic-concurrency machinery (Nomad needs those only for multiple concurrent
schedulers). The host's `coved` remains the ground truth for what is actually
running, reported via heartbeat.

## The open-core line

The line is drawn on **multi-host orchestration + governance + scale**, never by
crippling the single-host core — the Teleport pattern
([Teleport feature matrix](https://goteleport.com/docs/feature-matrix/)), which
keeps RBAC/audit/session-recording free and gates SSO/FIPS/device-trust/support.
The MIT core stays a fully usable product, not crippleware. Critically, the
already-shipped design 034 SSH fleet **stays MIT and is not removed**: the paid
control plane sits *above* it, it does not replace it.

| Capability | MIT (in `cove`) | Paid / separate (fleet control plane) |
|---|---|---|
| run / fork / restore, vsock control, per-VM control sockets | ✅ | delegates to worker |
| `coved` single-host daemon (lifecycle, image GC, storage budget, observability) | ✅ | delegates to worker |
| `coved` **worker mode** (dial-out, heartbeat, accept-assignment) | ✅ | — |
| design 034 SSH-tunnel routing + read-aggregation | ✅ (stays) | optionally brokered centrally |
| image build / push / pull / sync, fork-from | ✅ | fleet-coordinated distribution |
| client SDKs (Go, Python) + the control-surface protocol | ✅ | — |
| host registry + heartbeat inventory | — (034 deferred) | ✅ |
| stateful multi-host scheduler (feasibility/rank, base-image affinity, warm-pool) | — | ✅ |
| fleet-wide policy / GC push | — | ✅ |
| fleet RBAC, SAML/OIDC SSO, namespaces | — | ✅ |
| fleet-wide audit log (who-did-what) | — (single-host `metrics.jsonl` only) | ✅ |
| multi-tenant web UI | single-host coved UI only | ✅ |
| metering / billing | — | ✅ (hosted) |

### License choice for the paid layer

- **Recommended: clean proprietary/commercial split.** It cleanly separates the
  MIT execution engine from the closed fleet orchestration (Teleport model) and
  keeps the "MIT core stays trustworthy" message unambiguous. Keep the worker
  protocol and SDKs permissively licensed even though the controller is
  commercial — HashiCorp's deliberate move to keep SDKs/APIs MPL kept the
  ecosystem unencumbered.
- **Fallback if source-availability is required: Fair Core License (FCL)**, not
  plain FSL. FSL springs to OSS after 2 years and (per Sentry's own caveat) gives
  "little protection for self-hostable projects with commercial features" because
  a fork can re-enable enterprise gates; FCL adds ELv2-style protection.
- **Avoid BSL** for cove's situation: HashiCorp's BSL switch triggered the
  OpenTofu fork and broad mistrust. **Do not relicense the cove core away from
  MIT** under any circumstances — the whole open-core bet depends on the core
  being permanently trustworthy.

### Pricing and the strategic reframing (the important part)

The most decisive prior-art data point is also a warning. Orchard — cove's exact
prior art (controller + Apple-Silicon workers, embedded DB, first-fit scheduler)
— monetized the **same paid Apple-Silicon fleet niche** under a Fair Source
License: free below 4 workers, then $12,000/yr (20 workers) and $36,000/yr
(200 workers), Diamond at $12/core/yr — and *enforced it in court in 2025*
([tart.run/licensing](https://tart.run/licensing/)).

Then on ~2026-04-07 Cirrus Labs joined OpenAI, **stopped charging licensing
fees**, announced **relicensing Tart/Vetu/Orchard to a more permissive OSS
license**, and is **shutting down Cirrus CI on 2026-06-01**, with the maintaining
team moving to OpenAI (verified live:
[MacStadium](https://macstadium.com/blog/cirrus-labs-is-joining-openai),
[cirruslabs.org](https://cirruslabs.org/),
[WarpBuild](https://www.warpbuild.com/blog/cirrus-ci-shutting-down)).

What this means for cove, argued both ways:

- **More viable:** the only paid Apple-Silicon fleet vendor just vacated the
  niche, leaving displaced Cirrus CI customers who need macOS CI orchestration.
  cove can position as the maintained successor with a *stronger* primitive
  (sub-150 ms stateful fork/restore vs. Tart clones / cold boots) and no Local
  Network permission pain (vsock).
- **Less viable:** a mature, free, permissively-licensed Orchard becomes direct
  OSS competition to a paid cove scheduler — and if the category leader couldn't
  sustain the business standalone, copying its exact model is a high-risk bet.

**Recommendation: do not sell the scheduler.** Permissive-Orchard will give that
away. Monetize on the two axes a free OSS orchestrator structurally cannot match:

1. **Enterprise governance** (Teleport model): fleet SSO, RBAC, namespaces,
   tamper-evident audit, support SLA.
2. **Zero-ops hosted sandboxes** (Daytona/Cua model): API-key access, no hardware
   to manage, instant **macOS** sandboxes — see below.

A defensible free anchor for a self-hostable fleet binary is Orchard's proven
"4 workers free, paid above" host-count gate. But treat fleet revenue as
governance/hosted, with scheduling as the free adoption driver — not the SKU.

## Security & trust

- **Controller↔worker auth:** workers register with a one-time bearer token
  (Orchard Service Accounts / Daytona one-time runner key), then hold a
  dialed-out stream wrapped in **mTLS** *(inference: mTLS is recommended, not
  shipped)*. SSH transport can remain the MIT/stateless path; the stateful
  control plane moves to dialed-out gRPC so it degrades gracefully on dead hosts
  instead of hanging on SSH connects.
- **Tenant boundary = VM, never shared.** Hosts are trusted substrate; each CI
  job or dev environment is a fresh fork (design 013). The vsock control channel
  is host→guest only, so guest code cannot dial the host's `coved` to escape —
  *the fleet design must preserve this: `coved` worker mode must refuse arbitrary
  host-shell instructions from the controller and accept only VM-lifecycle +
  vsock-guest payloads.*
- **RBAC/SSO/audit are paid** (Teleport line). The controller maps SSO identities
  to namespaces and filters who may place/see/`cove shell` which VMs.
- **Blast radius:** the controller is a high-value target (it can place/control
  VMs fleet-wide). The worker-dials-out topology is the structural mitigation: a
  compromised controller still has **no inbound network path to the Mac
  hardware** — it cannot port-scan or SSH into hosts, only push bounded
  instructions a hardened worker may reject.
- **Audit log:** single-host `~/.vz/metrics.jsonl` stays MIT; the fleet-wide
  "user X forced-stopped tenant Z's VM" log lives on the controller (paid),
  appending the authenticated SSO/service-account identity to worker-reported
  lifecycle events. The first controller audit feed exists now with
  `controller`/`worker:<id>` actors, and service-account bearer tokens now bind
  operator requests to `service-account:<name>` actors. Service accounts can
  now carry a namespace, and scoped bearer tokens are filtered to that namespace
  for assignment, warm-pool, sandbox, service-account, and audit controller
  resources.
  Service-account roles now split read-only `viewer`, operational `operator`,
  and service-account-managing `admin` verbs. Audit events now carry a
  deterministic `prev_hash`/`hash` chain, `GET /v1/audit/verify` verifies
  the global chain for unscoped viewers, and `/v1/oidc-bindings` now maps
  RS256-verified OIDC JWTs into namespace-scoped viewer/operator/admin
  identities with issuer discovery, JWKS caching, and key-miss refresh. The
  remaining work is SAML identity binding.

## Hosted offering (use case C)

- **Unified control surface.** Steal Cua's `provider_type` move: a caller writes
  `cove.create(provider="local")` (SDK dials `~/.vz/cove.sock`) or
  `cove.create(provider="cloud", api_key=…)` (SDK hits the control-plane REST
  API) — local and hosted look identical. SDKs (Go, Python) stay MIT; the REST
  gateway is paid.
- **API shape**, modeled on the fly.io Machines API
  ([fly machines API](https://fly.io/docs/machines/api/machines-resource/)) and
  Daytona's sandbox lifecycle, hiding the host entirely:
  `POST /v1/sandboxes` (create = fork-from a snapshot), `GET /v1/sandboxes/{id}`,
  `POST /v1/sandboxes/{id}/{start|stop|wait}`, `DELETE /v1/sandboxes/{id}`, plus
  `lease` (exclusive-modify lock) and controller-backed `cordon` (prevent
  placement across all clients) plus `drain` (cordon a worker and stop/cancel
  hosted sandbox handles already placed there). The first scaffold now exists:
  `POST/GET/DELETE /v1/sandboxes` creates a tracked fork-run assignment, probes
  readiness through `cove shell`, waits on terminal status, starts stopped
  handles from the retained VM, restarts active handles through stop cleanup
  plus requeue, stops/deletes by canceling pending work or queueing same-worker
  stop cleanup, and exposes `POST/DELETE /v1/sandboxes/{id}/lease` for
  TTL-based exclusive modify locks, `POST /v1/sandboxes/{id}/exec` for
  same-worker `cove shell` execution, `POST /v1/sandboxes/{id}/control` for
  same-worker screenshot/key/text/mouse control-socket events, plus `GET
  /v1/sandboxes/{id}/metering` and `GET /v1/metering/sandboxes` for scoped usage
  records. `GET /v1/sandboxes` now filters by status, worker, image, namespace,
  and limit, so hosted clients can narrow the response to the handles they need;
  the Go and Python SDKs expose the same filters. The OpenAI Agents Python adapter now has a `provider="cloud"`
  `SandboxRunConfig` path over this REST surface, and the public Go
  `agentsandbox` package now has a matching local/cloud client for hosted
  create/list/get/wait/lease/restart/exec/control/metering/delete and
  screenshot/key/text/mouse flows.
  `POST /v1/workers/{id}/drain` now gives operators a host-maintenance control
  operation over hosted sandboxes without hand-enumerating handles.
  `GET /v1/operations/summary` now gives unscoped viewers a reconciled
  operations view across worker readiness, assignment state, hosted sandbox
  handles, warm-pool slots, and aggregate metering.
  The bar to beat is Daytona's create→exec→delete in under six lines with an
  opaque handle.
- **The wedge.** Cua Cloud runs **Linux/Windows only** — Apple-Silicon macOS does
  not scale horizontally cheaply, so they abandoned hosted macOS — and Cua users
  are openly asking for remote/multi-host macOS (Cua issues #1446, #1021) that
  Cua structurally cannot serve. cove is Apple-Silicon-macOS-native; position the
  hosted offering as **"sub-second resume of a stateful macOS machine"**, a
  fork/restore primitive namespace sandboxes cannot match.
- **Metering** lives only in the paid control plane: per-minute/per-second per
  vCPU+RAM, no charge while stopped/archived, **BYO-LLM-key** so compute is the
  only SKU (sidesteps the "are you marking up tokens" objection). The initial
  control-plane records now derive VM, CPU, and memory-byte milliseconds from
  worker-reported `running` and `ready` lifecycle intervals.
- **Deployment: both, identical binary.** Avoid the "Daytona trap" (single-host
  Docker-Compose OSS vs. heavyweight Helm/Terraform-K8s cloud). Follow
  Nomad/Orchard: ship one ~single-binary fleet control plane that runs the same
  on a laptop or in production. Offer (1) a self-hostable commercial/source-avail
  fleet binary others run on their own Macs, host-count-gated; and (2) a fully
  managed "cove Cloud" where cove runs the hardware. Both keep `cove`/`coved`/
  fork-restore MIT forever.

## Incremental slice plan

Each slice is independently shippable. The MIT/paid boundary is at **Slice 5**.

| Slice | Name | Ships | License | Depends on | Why now / trigger |
|---|---|---|---|---|---|
| 4 | Maximize stateless SSH | SSH connection pooling; concurrent multi-host `cove run`; parallel health/inventory probes over SSH | MIT | 034 S1–3 | Exhausts the stateless design space (all 034-deferred). Burst CI capacity with zero new operational surface or trust change. |
| 5 | **Stateful fleet controller (the boundary)** | new control-plane binary (working name `cove-fleetd`) accepts worker dial-ins; `coved` gains `--fleet-url` outbound stream + four-verb worker protocol; embedded host-inventory store plus operations summary | **PAID** (controller) / **MIT** (worker mode) | Slice 4 | NAT traversal for remote Macs; N-way SSH fan-out becomes the bottleneck; establishes the monetization boundary. |
| 6 | Fleet-wide policy & GC | controller pushes lifecycle policy, image-GC, and storage budget/prune down worker streams; workers report results | PAID | Slice 5 | Fulfills 034-deferred fleet-wide policy; image-GC, VM lifecycle-policy, and storage budget/prune push have landed. |
| 7 | Top-k bin-pack scheduler + base-image affinity + warm-pool | controller-side placement replacing client-side least-loaded; base-image affinity; fork-warm-pool quota plus agent-ready slot claim, downsize, delete, cleanup, and lifecycle status counts | PAID | Slice 5 | Resolves oversubscription races; lights up cove's image-locality edge for CI/agent pools. |
| 8 | Hosted API + SDK provider abstraction + metering | REST `/v1/sandboxes` surface; create/list/get/delete/start/restart/stop/wait/lease/exec/control/metering scaffold has started; Python `provider=local\|cloud` SDK path; BYO-LLM-key | PAID (hosted) | Slice 7 | Use case C; competes with Daytona/Cua on the macOS wedge. |
| 9 | Fleet RBAC / SSO / audit | SAML/OIDC, namespaces, fleet audit log; initial persisted audit feed, service-account actor binding, namespace filters, service-account roles, audit-chain verification, and RS256 OIDC bearer bindings with discovery/JWKS refresh have started | PAID | Slice 5 | Enterprise-governance monetization axis (Teleport line); the durable revenue, not the scheduler. |
| — | Federation (deferred) | independent per-site controllers, no cross-site consensus | PAID | Slice 7 | Multi-datacenter scale; deliberately deferred to avoid premature distributed-systems complexity. |

*(Inferences in this plan: the `cove-fleetd` binary name, the `--fleet-url`
flag, and the exact slice ordering are design proposals derived from
Nomad/Orchard prior art, not existing cove code or shipped commitments.)*

## Open questions

1. Does the design 034 SSH fleet stay the *recommended* path for ≤4 hosts even
   after the controller ships, or does the controller become the default at some
   host count? (Lean: SSH stays the MIT default; controller is opt-in above the
   fan-out bottleneck.)
2. Worker protocol transport: gRPC stream (Orchard) vs. long-poll (Daytona).
   gRPC bidi stream is the better fit for controller-push; confirm against
   purego/connect-go constraints already used by the guest agent.
3. Is "cove Cloud" (cove runs Macs) in scope at all, or only the self-hostable
   controller? This is a business decision, gated by the same privacy/brand gate
   as the public registry.
4. Trademark/brand: a paid product surface re-raises the unresolved "cove" name
   question (ROADMAP Product Decisions). Resolve before any public fleet/hosted
   launch.

## Cross-references

- [`034-fleet-slice-1.md`](034-fleet-slice-1.md) — the shipped stateless fleet
  this builds on; its Deferred list is this design's input.
- [`033-cove-daemon.md`](033-cove-daemon.md) — `coved`, the single-host daemon
  that becomes the fleet worker.
- [`013-vm-fork.md`](013-vm-fork.md) / [`archive/015-soft-reset-empirical.md`](archive/015-soft-reset-empirical.md)
  — the fork/restore isolation primitive and why soft reset is rejected.
- [`031-vm-lifecycle.md`](031-vm-lifecycle.md), [`032-vm-quotas.md`](032-vm-quotas.md),
  [`040-storage-budget.md`](040-storage-budget.md) — the per-host signals the
  scheduler consumes.
- [`026-ephemeral-self-hosted-runners.md`](026-ephemeral-self-hosted-runners.md)
  — `cove runner job` (fork-per-job on one host); the fleet coordinates a pool of
  these across hosts.
- [`../strategy/competitive-2026-05.md`](../strategy/competitive-2026-05.md) and
  [`../strategy/non-goals.md`](../strategy/non-goals.md) — the competitive
  position and the "do not build a generic hosted queue" constraint this design
  honors (cove orchestrates VM/image/fork execution; it is not a CI queue).
