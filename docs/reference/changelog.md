---
title: Changelog
---
# Changelog

All notable changes to cove are documented here. Format follows [Keep a Changelog](https://keepachangelog.com/).

## Unreleased

### Added
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
- `coved -fleet-url` now stops a claimed warm-pool VM after the claimed
  guest-exec assignment finishes, allowing the original warm-run assignment to
  terminate and the controller to replenish capacity cleanly.
- `cove-fleetd` now downsizes warm-pools when `size` is lowered: never-started
  surplus slots are canceled, and already-started surplus slots get same-worker
  `cove ctl -vm <name> stop` cleanup assignments.
- Deleting a warm-pool now removes the desired pool, cancels pending slots,
  queues stop cleanup for idle started slots, and defers claimed slots until
  their guest-exec assignment finishes.
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
