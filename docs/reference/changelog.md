---
title: Changelog
---
# Changelog

All notable changes to cove are documented here. Format follows [Keep a Changelog](https://keepachangelog.com/).

## Unreleased

### Added
- Forked run bundles now emit best-effort `resource_sample` metrics on
  agent-ready and before cleanup, including guest memory totals when the agent
  reports them and Virtualization.framework memory/balloon fields when the
  runtime exposes them.
- `resource_sample` metrics now continue periodically after guest agent
  readiness for standalone runs and forked run bundles, and include owning cove
  process PID, CPU percent, RSS bytes, and start source when available.
- `resource_sample` metrics now harvest existing guest-agent load averages,
  uptime, disk capacity, and user-count fields, giving CI runs in-guest pressure
  signals without SSH or a new agent protocol.
- `vz-agent` info now includes a bounded top-process list, and
  `resource_sample` metrics include guest process count plus top process
  PID/CPU/RSS/command labels for in-guest attribution.
- `cove runs show`, `runs show --summary-json`, and GitHub summary exports now
  fold `resource_sample` events into resource summaries with
  memory/load/process/host peaks and pressure hints, while raw JSON exports keep
  the event array.
- `cove-action` now treats guest artifact copy-out as a first-class CI path:
  `artifacts` / `COVE_ACTION_ARTIFACTS` lists absolute guest paths, copies them
  into the run bundle under `guest/`, exposes the bundle path, and records
  `artifact_copy` metrics.
- `cove-action` now forwards guest-created GitHub annotations: guests append
  raw annotation workflow commands or JSON records to `COVE_GITHUB_ANNOTATIONS`,
  cove copies `github-annotations.log` into the run bundle, forwards only
  `error` / `warning` / `notice`, and records `github_annotation_forward`.
- `cove image inspect -remote` now accepts multiple registry refs for private
  catalog audits. Single-ref JSON remains an object; batch JSON is an array and
  includes per-ref errors while returning a failing exit status if any ref fails.
- `cove pull`, registry-base builds, build-cache imports, delta-base checks, and
  `cove image inspect -remote` now resolve OCI image indexes and Docker manifest
  lists to a same-repository image manifest before parsing cove/Tart/Lume
  metadata.
- `cove image inspect -remote [-json] <registry/ref:tag|digest>...` now fetches
  registry metadata without pulling disks and summarizes cove-native, Tart,
  Lume, and cove image-store artifacts with digest, format, disk/chunk/part
  counts, sidecar sizes, and base-manifest reuse.
- `cove-fleetd` now exposes `GET /v1/sandboxes/{id}/events` for sandbox-scoped
  audit history, and the Go/Python hosted sandbox clients expose matching event
  list helpers.
- `cove-fleetd` now exposes `GET /v1/sandboxes/{id}/reports` for sandbox-scoped
  worker report history, including exec/control stdout, stderr, errors, and
  exit codes; the Go/Python hosted sandbox clients expose matching report-list
  helpers.
- `cove-fleetd` audit queries now support actor, action, target type, target
  ID, sandbox ID, offset, limit, count, and next-offset metadata for
  namespace-scoped controller audit review.
- `cove-fleetd` now exposes `GET /v1/operations/summary` for unscoped viewers,
  aggregating worker readiness, active assignments, sandbox handles, warm-pool
  slots, and sandbox metering for operator dashboards.
- `cove-fleetd` now exposes `POST /v1/workers/{id}/drain`, which cordons a
  worker and stops or cancels its hosted sandbox handles through the existing
  same-worker cleanup path.
- `cove-fleetd` now exposes `POST /v1/sandboxes/{id}/control` for hosted
  screenshot, key, text, and mouse events through the backing VM control socket.
- The OpenAI Agents Python cloud provider now maps `ComputerTool`
  screenshot/key/text/mouse calls onto the sandbox control endpoint.
- The public Go `agentsandbox` package now includes a local/cloud client for
  hosted sandbox create/list/get/wait/lease/restart/exec/control/events,
  reports, metering, and delete flows, and the Python fleet client exposes
  matching lifecycle helpers.
- The public Go `agentsandbox` package and Python fleet client now expose hosted
  sandbox list filters and pagination for status, worker, image, namespace,
  offset, limit, count, and next-offset metadata.
- `cove-fleetd` now supports admin-managed RS256 OIDC bearer bindings that map
  verified issuer/subject/audience claims onto namespace-scoped controller roles.
- OIDC bindings can now fetch public keys from `jwks_url`, discover
  `jwks_uri` from the issuer, cache fetched key IDs, and refresh the JWKS on
  key misses for provider key rotation.
- `cove-fleetd` now exposes admin-managed `/v1/saml-bindings` records for
  fail-closed SAML setup: IdP entity ID, SSO URL, audience, namespace, role, and
  PEM signing certificate are validated, persisted, scoped, and audited while
  assertion authentication remains disabled until a real XML signature verifier
  lands.
- `cove fleet cordon` and `uncordon` now mark registered hosts as skipped for
  `fleet run` placement while keeping direct `--fleet=<name>` routing intact.
- `cove fleet run` now records short local placement leases and counts active
  leases as pending load during later least-loaded and image-affinity placement.
- `cove fleet health [--json]` now reports remote cove reachability and version
  across registered hosts before placement.
- `cove fleet run --all` now starts the same run concurrently on every
  non-cordoned registered host for stateless burst fan-out, pre-staging
  `-fork-from` images to cold hosts first.
- Fleet SSH calls now enable OpenSSH ControlMaster reuse by default, with
  `COVE_FLEET_SSH_MULTIPLEX=0` available for troubleshooting isolated
  transports.
- Lume-format `cove pull` now prefetches tar-split disk parts concurrently,
  writes them back in manifest order, and verifies each part's OCI
  size/digest before extraction.
- `cove-fleetd` now provides the first stateful fleet-control-plane boundary:
  a private host-inventory store plus worker register, heartbeat,
  await-assignment, and report-status HTTP endpoints.
- `coved -fleet-url` can now dial out to `cove-fleetd` as a fleet worker,
  register host capacity, send periodic heartbeats, poll assignments, and report
  unsupported assignment verbs until execution lands.
- `cove-fleetd` now persists controller assignments, exposes private
  assignment create/list/get endpoints, leases pending work to workers, and
  records worker reports back onto the assignment.
- `coved` now executes leased `cove` assignments with a configurable local cove
  binary, timeout, exit code, and bounded stdout/stderr reporting.
- `cove-fleetd` assignment creation can now place work on a ready worker with
  controller-side `least-loaded` or `image-affinity` policy using `coved`
  heartbeat image refs and pending assignment load.
- `cove-fleetd` now reconciles stale workers and expired assignment leases,
  exposes `POST /v1/reconcile`, and `coved` renews active `cove` assignments
  with `running` reports so long jobs do not get reclaimed while still alive.
- `cove-fleetd` now exposes controller-side worker cordon/uncordon endpoints;
  cordoned workers keep heartbeating but are skipped for policy placement while
  explicit `worker_id` assignments remain allowed.
- `cove-fleetd` now exposes `POST /v1/images/prepare` to queue `cove image
  pull` assignments only on matching ready workers missing a base image; `coved`
  refreshes image refs immediately after successful image-prep assignments.
- `cove-fleetd` now exposes `POST /v1/images/gc` to queue fleet-wide
  `cove image gc` assignments on matching ready workers, with dry-run by default,
  optional `apply:true`, active-operation skip reporting, and post-GC image-ref
  refreshes from `coved`.
- `cove-fleetd` now exposes `POST /v1/policies/lifecycle` to push
  `cove policy <vm> set ...` or `clear` assignments to matching ready workers,
  and `cove policy <vm> set` now accepts multiple `field=value` updates.
- `cove-fleetd` now exposes `POST /v1/storage/budget` and
  `POST /v1/storage/prune` to fan out storage budget set/clear and dry-run or
  applying prune assignments across matching ready workers.
- `cove-fleetd` now persists controller audit events and exposes `GET /v1/audit`
  with optional `limit`, covering worker registration, cordon lifecycle,
  assignment creation/lease/report, reconcile changes, policy/image/storage
  fan-out, and warm-pool lifecycle operations. The audit list API later gained
  actor/action/target filters and offset page metadata.
- `cove-fleetd` now persists service-account records with hashed bearer tokens;
  authenticated controller requests record audit actors as
  `service-account:<name>` while local unauthenticated requests still work as
  `controller`.
- `cove-fleetd` service accounts can now carry a `namespace`; scoped bearer
  tokens can only create, list, read, and audit assignment, warm-pool, sandbox,
  and service-account resources in that namespace, while unscoped local
  controller workflows remain unchanged and unknown bearer tokens are rejected.
- `cove-fleetd` service accounts now support `viewer`, `operator`, and `admin`
  roles so scoped tokens can be read-only, operational, or allowed to manage
  service accounts.
- `cove-fleetd` audit events now carry a deterministic `prev_hash`/`hash`
  chain, and `GET /v1/audit/verify` verifies the global audit log for
  unscoped viewers.
- `cove-fleetd` assignment placement now supports `policy:"bin-pack"` plus
  `resources.vms` hints and worker `max_vms` slot caps, packing work onto the
  densest ready worker that still fits.
- `cove-fleetd` assignment placement now supports `anti_affinity_key` to spread
  active assignments for the same job, base image, or replica group across
  workers before applying normal load tie-breaks.
- `cove-fleetd` now exposes `POST /v1/placements/plan` to return the retained
  top ranked feasible workers for a placement request without storing an
  assignment.
- `cove-fleetd` now exposes `POST /v1/warm-pools`,
  `GET /v1/warm-pools`, `GET /v1/warm-pools/{name}`, and
  `DELETE /v1/warm-pools/{name}` for durable fork warm-pool quotas;
  reconciliation creates replenishable `cove run -fork-from ... -ephemeral
  -keep -headless` assignments through `coved`.
- `coved -fleet-url` now marks warm-pool slots `ready` only after a successful
  `cove shell <vm> -- /bin/sh -c true` probe, so `POST /v1/warm-pools/claim`
  hands off agent-ready forks instead of merely live `cove run` processes.
- `cove-fleetd` now exposes `POST /v1/warm-pools/claim` to claim a ready warm
  fork, mark the slot unavailable, and queue same-worker guest execution through
  `cove shell <vm> -- ...` while replenishing the warm-pool quota.
- Warm-pool status responses now include open slot totals, per-state
  pending/leased/running/ready/claimed/draining/terminal counts, `by_status`,
  and claimed/draining slot visibility for operator lifecycle diagnostics.
- `coved -fleet-url` now stops a claimed warm-pool VM after the claimed
  guest-exec assignment finishes, allowing the original warm-run assignment to
  terminate and the controller to replenish capacity cleanly.
- `cove-fleetd` now downsizes warm-pools when `size` is lowered: never-started
  surplus slots are canceled, and already-started surplus slots get same-worker
  `cove ctl -vm <name> stop` cleanup assignments.
- Deleting a warm-pool now removes the desired pool, cancels pending slots,
  queues stop cleanup for idle started slots, and defers claimed slots until
  their guest-exec assignment finishes.
- `cove-fleetd` now exposes hosted-style sandbox handles at
  `POST/GET/DELETE /v1/sandboxes`: create maps to an image-affinity
  `cove run -fork-from ... -ephemeral -keep -headless` assignment, `coved`
  reports the sandbox ready after a `cove shell` probe, and stop/delete cancel
  pending sandboxes or queue same-worker stop cleanup.
- `GET /v1/sandboxes` now accepts `status`, `worker_id`, `image_ref`,
  non-negative `offset`, and non-negative `limit` filters, and returns
  `count`/`next_offset` pagination metadata so hosted clients can list only the
  handles they need without fetching the whole controller inventory.
- `POST /v1/sandboxes/{id}/wait` now waits for a sandbox handle to reach a
  terminal state, and sandbox stop-cleanup completion transitions the handle to
  `stopped`.
- `POST /v1/sandboxes/{id}/start` now requeues canceled sandboxes from their
  original fork assignment and starts stopped/complete handles from the retained
  VM on the same ready worker; `POST /v1/sandboxes/{id}/restart` queues
  same-worker stop cleanup and automatically requeues the retained VM start
  after cleanup completes.
- `POST/DELETE /v1/sandboxes/{id}/lease` now acquires, renews, and releases
  exclusive sandbox handle leases with TTLs, conflict responses, namespace
  scoping, and audit events. Active leases now guard hosted sandbox
  start/restart/stop/delete/exec/control mutations unless the caller supplies
  the matching holder, and the Go/Python SDKs carry held lease holders forward
  automatically.
- `POST /v1/sandboxes/{id}/exec` now queues same-worker `cove shell` work for a
  ready hosted sandbox and can wait for the worker report, giving SDKs a real
  create-exec-delete control-plane loop.
- `cove-fleetd` now persists per-sandbox metering records for active
  `running` and `ready` intervals, with scoped `GET
  /v1/metering/sandboxes` and `GET /v1/sandboxes/{id}/metering` summaries for
  VM, CPU, and memory-byte milliseconds.
- The OpenAI Agents Python adapter and public Go `agentsandbox` package now
  support cloud/control-plane sandboxes for create/wait/exec/delete workflows.
- `coved -fleet-url` now starts leased assignments asynchronously so a
  long-running `cove` assignment can keep renewing while the worker continues
  heartbeating and polling for additional assigned work.
- `cove build --cache-from` and `--cache-to` now import and export cove
  build-cache artifacts through OCI refs, carrying cache entries, layer
  manifests, and block-delta blobs between runners.
- Cirrus-displacement migration surface: private landing-page draft, five-step
  quickstart, full `.cirrus.yml` migration walkthrough, operator checklist,
  migration doctor VZScript, and May 2026 benchmark report. Public install and
  registry language remain gated until release, privacy, trademark, and tap
  availability checks clear.

## v0.3.0 - 2026-05-05

### Added
- `cove build` non-dry-run execution for local VM-directory bases: cache hits validate metadata and skip guest execution, misses fork a scratch VM and run vzscript steps through the agent, layer manifests are verified before apply, and the final VM directory can be handed to `cove push`.
- Cache-hit materialization for build layers, including complete step metadata validation, `# cache-ttl:` expiry, compact-mode matching, and layer manifest digest verification before apply.
- Cache-miss execution for build steps: scratch VM lifecycle, guest-agent execution, block-delta layer recording, failure cleanup, and `--keep-intermediate` inspection support.
- `# secret:` directive: host environment variables mounted through a guest tmpfs (Linux) or RAM disk (macOS) with guest swap disabled and unmounted before compaction and the layer diff.
- `# secret-from:` directive for build-time secret resolution through the URI resolver. v0.3 ships the MVP providers `env://` and `file://`; external secret stores are deferred.
- `cove secret probe <uri>` verifies that a secret URI resolves and prints only the resolved byte length.
- Build pipeline compaction: `fast`, `targeted` (default), and `thorough` modes run between guest execution and the layer diff, with the compact mode included in the cache key. `thorough` on macOS guests targets the writable Data volume, uses a `dd` + virtio-blk TRIM recipe, and preflights host capacity before inflating sparse images.
- Published fork-only and boot-to-agent fork benchmarks on named M4 hardware under `bench/fork-time/`.
- OpenAI Agents SDK adapter v1 plus live-smoke and package-check documentation under `adapters/openai-agents-python/`.
- Anthropic sandbox-runtime adapter for computer-use workflows.
- Run metrics for forked runs: `~/.vz/runs/<run-id>/metrics.jsonl` records structured lifecycle events for run start/end, fork materialization, VM start, and agent-ready timing. JSONL is the default local sink; OTLP export is available through `OTEL_EXPORTER_OTLP_ENDPOINT`. See [Run Metrics](../features/metrics.md).
- Minimal network policy surface for `cove run` and `cove up`: `-network` / `--net` modes for `nat`, `bridged:<iface>`, `host-only`, and `none`, plus `cove ctl port-forward start|stop|list` for host-to-guest TCP access. See [Networking](../features/networking.md).
- [Agent Sandbox Quickstart](../quickstart-agent-sandbox.md): a packaged computer-use quickstart covering OpenAI Agents SDK, Anthropic Claude computer use, Gemini computer use, fork-per-task isolation, and per-run artifacts.
- Private GitHub Actions executor verification for `cove-action`: simple commands, multiline scripts, and intentional guest-command failure have all been exercised end-to-end with the expected exit-code surface.

### Deferred
The following are explicitly deferred for this RC. Public docs keep this list
visible and consistent across CLI reference, roadmap, and release checklist:
- Registry cache import/export (`--cache-from`, `--cache-to`). The flags are reserved and fail before planning if used.
- Public curated `cove` image registry and signed agentkit image channels until trademark counsel clears the name or a rename lands.
- External secret stores beyond `env://` and `file://` (1Password, Vault, SOPS, age, cloud secret managers).
- BuildKit-style parallel step execution. v0.3 build execution is sequential.
- Packer plugin shim (sunset; see [Non-goals](../strategy/non-goals.md)).
- Product-name resolution before any public registry or signed channel ships.
- Fresh `agentkit/linux-base` image refresh is still in flight for this cycle and is not yet listed as shipped.

### Fixed
- Malformed build/store manifest digests now return validation errors instead of silent success.
- Build-cache entries and layer manifests now reject malformed digests before saving or reporting cache hits.
- Build cache hits now require complete step metadata, verify it before applying a layer, and honor `# cache-ttl:` expiry.
- `cove build <name> --base ... --script ... --dry-run` now accepts the documented command order.
- `cove build --dry-run` can use `--store-dir` to inspect cache hits in a specific content store.
- `cove build` can now materialize registry bases into build scratch for non-dry-run execution; `--push` requires at least one `--tag`.
- `--compact thorough` on macOS guests no longer targets the read-only System volume.

## v0.2.1 - 2026-05-05

### Added
- `cove image build/list/rm`: local image store at `~/.vz/images/<name>/<tag>/`. `cove image build -from <vm> -tag <name[:tag]>` snapshots a stopped VM bundle (manifest.json + clonefile-backed disk + aux + machine.id + hw.model; vmstate excluded per identity-binding rule). `cove image rm` refuses while live forks reference the image (gated by `ParentImage` on child config.json). Pure local; no registry, no push/pull, no signing in this slice -- design 024 Slice 2 ships push/pull in v0.4.
- `cove image inspect <ref> [-json]`: print manifest (size, sha256, base image, created-at, `hw.model` fingerprint) plus the live downstream fork list. `-json` emits a stable schema for tooling; reuses `VMsForkedFromImage` so the fork inventory stays in lockstep with `cove image rm`.
- `cove image push <ref> <file> [-gzip]` and `cove image load <file> [-tag <ref>] [-force]`: local-tarball image portability without a registry. Push tars an image directory to a single file with atomic temp + rename; load extracts back into `~/.vz/images/<ref>/` with a manifest-first stream layout. Tar entries are restricted to `manifest.json`, `disk.img`, `aux.img`, `hw.model`, `machine.id` (TypeReg only); zip-slip, symlink, and oversize entries are refused before any filesystem write. `ParentImage` is not preserved across hosts.
- `cove image gc [-dry-run] [-yes] [-older-than D]`: sweep images with zero live forks. Mirrors the disposable-VM GC pattern -- re-checks fork count immediately before deletion to close the planning -> remove TOCTOU window, and refuses to remove the image root.
- `cove run -fork-from <image-ref> -ephemeral`: fork-from accepts a local image ref. VM-parent RAM-overlay forks are not implemented; use `cove fork` or `cove clone --linked` for VM parents. `-ephemeral` drops the existing `.ephemeral` sentinel so `cove gc` sweeps the materialized child on stop.
- Per-run artifact bundling for `cove run -fork-from`: each ephemeral fork-from invocation lazily creates `~/.vz/runs/<run-id>/` with `manifest.json` (run id, vm name, fork ref, started/ended timestamps, exit status), `events.jsonl` (control-socket request log), `stdout.log`, `stderr.log`, and `screenshots/`. Manifest is written atomically (temp + rename) on shutdown for both success and failure paths. Plain `cove run <vm>` is unaffected.
- `cove shell <vm>`: Docker-shaped standalone exec client. `cove shell <vm>` opens an interactive `bash -l` against a running VM; `cove shell <vm> -- argv...` runs a one-shot command and propagates stdout/stderr/exit-code. SIGWINCH forwards to `agent-exec-resize`; SIGINT detaches the main cove handler so Ctrl-C goes to the guest. Current agents use `ExecAttach` for bidirectional stdin; older agents fall back to the v0.2 read-only stdin path with a warning.
- Server-side `cove shell` control-socket commands: `agent-exec-attach`, `agent-exec-resize`, `agent-exec-signal` on the per-VM control socket. Reuses existing `control.token` auth and the long-lived stream dispatch arm. ExecAttach streams stdin, stdout/stderr, signals, resize events, and exit status.
- `vzscripts/github-runner`: install and register a self-hosted GitHub Actions runner inside a long-lived cove VM. Solves the GH Actions billing-block escape hatch directly.
- `vzscripts/gitlab-runner`: same shape for GitLab CI projects (shell executor on macOS arm64).
- `vzscripts/tailscale`: install Tailscale via homebrew and bring the daemon up with `--ssh`; idempotent.
- `cove run -linux -shell`: attach the host terminal to an interactive guest shell after boot. Allocates a PTY in the guest via the agent, forwards host SIGWINCH to `ResizeExecTTY` and host SIGINT to `SignalExec(SIGINT)` (with the main cove shutdown handler temporarily detached so the first Ctrl-C reaches the guest only), and forwards stdin when the guest agent supports ExecAttach.

### Changed
- Linux installer VM configurations now share the same virtio socket setup as normal Linux runtime configurations.
- `cove recording list/export` adds a first-class path for finding and packaging run/session recording artifacts without inspecting `~/.vz/runs` by hand.
- `cove trace enable/start/stop/status/export` records eslogger trace session metadata and exports trace artifacts for macOS guests, with explicit unsupported diagnostics for other guest types.
- The macOS status item now uses clearer state labels and exposes error-state restart behavior through the tray menu model.

### Fixed
- GUI delegate and iTerm2 proxy paths snapshot shared state under lock before use, closing concurrency races found in the T39 audit.
- Linux installer configurations attach the virtio socket device during install, so control-socket probes no longer fail with `no socket devices configured on VM`.
- Linux install disks use durable attachments.
- Blank post-install disks report an explicit "installer produced no partition table" error with retry guidance.
- Disk I/O benchmark docs now record that the Ubuntu Desktop virtio-blk vs NVMe comparison is blocked by first-boot provisioning reliability, so no throughput claim is made in this release.
