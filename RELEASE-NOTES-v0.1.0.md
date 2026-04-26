# cove v0.1.0 — release notes

First tagged release of cove: a purego Virtualization.framework VM tool
with an "operate a pre-existing VM" HTTP/MCP surface for macOS and Linux
guests on Apple Silicon.

## Highlights

- Native macOS and Linux VM lifecycle on Apple Silicon, built on
  Apple's Virtualization.framework via
  [purego](https://github.com/ebitengine/purego) — no cgo.
- Pre-existing-VM control surface over three channels: CLI, HTTP, and
  MCP stdio. Anything the CLI can do against a running VM, a script,
  agent, or SDK can do over HTTP or MCP.
- VM state snapshots, APFS clonefile disk snapshots, pause/resume,
  and per-VM control socket with token auth.
- `cove dump-docs` emits structured CLI / HTTP / MCP surface as JSON
  for SDK and agent wrappers — no help-text scraping.

## Install

```bash
brew install tmc/tap/cove
# or
go install github.com/tmc/vz-macos@latest
```

Build from source:

```bash
go build -o cove .
codesign -s - -f --entitlements internal/autosign/vz.entitlements ./cove
```

cove auto-signs itself with the Virtualization.framework entitlements on
first launch, so manual `codesign` is only needed when working from a
local build.

## What's in

### Operate a pre-existing VM

- `cove ctl ping | status | pause | resume | stop | request-stop`
- `cove ctl snapshot save | list | restore | delete`
- `cove ctl disk-snapshot save | list | restore | delete` (APFS
  clonefile)
- `cove ctl disk`, `ctl usb`, `ctl memory`, `ctl power`,
  `ctl screenshot`, `ctl key`, `ctl mouse`, `ctl network-info`
- `cove ctl gui open | close | status` — detach or reattach the
  AppKit window for a headless runtime

### HTTP gateway (`cove serve -http`)

- 26 REST endpoints under `/v1/vms/<name>/*` for status, pause,
  resume, stop, request-stop, screenshot, type, key, mouse,
  agent/exec, agent/read, agent/write, agent/cp, snapshot,
  snapshots, disk-snapshots, pit-snapshots, events (SSE).
- `/v1/operations` + `/v1/operations/<id>` + `/v1/operations/<id>/events`
  for long-running operation tracking and SSE progress.
- Bearer-token auth from macOS keychain or a token file.
- `-per-vm-auth` mode requires each VM's own `control.token`.

### MCP stdio (`cove serve --mcp`)

- 19 MCP tools for VM lifecycle, input, snapshots, agent exec/read/write,
  and disk/PIT snapshot listing. JSON-RPC 2.0 over stdin/stdout,
  protocol version 2024-11-05.

### Async operations (LRO)

- Per-VM long-running operations registry with `/v1/operations`,
  `/v1/operations/<id>`, and `/v1/operations/<id>/events` (SSE).
- `cove ctl snapshot save -async` returns an operation id immediately;
  `cove ctl operations get | list | wait` poll or block on completion.
  Closes the large-`.vmstate` `i/o timeout` workaround on the
  synchronous path. (`feat/snapshot-save-async`)

### OCI image pull

- `cove pull` of cove-format images: cove-native LZ4 chunks with
  role/size/chunk-index annotations.
- `cove pull` of upstream **lume** tar-split images: detects
  lume's `disk.img` named-tar-parts manifest, routes to a separate
  importer, and projects lume config into cove's vmconfig with a
  `lume-config.json` sidecar. (`feat/lume-tarsplit-pull`)
- `cove pull` of **cirruslabs/tart** OCI images: Apple-framed LZ4
  codec, tart manifest detection, and tart→cove vmconfig projection.
  Pull `ghcr.io/cirruslabs/macos-*` directly into cove.
  (`feat/cove-tart-compat`, foundation `9feef25` + wiring `7444dda`)
- `cove push --format lume` for cove→lume export (dry-run only in
  this tag). (`feat/cove-lume-export`)

### Guest agent (vsock)

- `vz-agent` runs as a LaunchDaemon inside the guest over vsock port
  1024 and as a per-user LaunchAgent on port 1025. Exec, read, write,
  cp, self-upgrade, version reporting.
- TCC-protected file operations (VirtioFS mounts, user home access)
  are path-routed through the user agent on 1025; the daemon on 1024
  remains for system-scope work. (`feat/route-tcc-through-user-agent`)
- Self-upgrade bounces both LaunchAgent and LaunchDaemon; directional
  version compare prevents auto-downgrade.
  (`feat/agent-auto-upgrade`)
- Configurable health-check interval, slog reconnect logging, and
  downtime tracking. (`feat/agent-health-monitor`)
- Linux cloud-init auto-injects `vz-agent` at install time.
- Agent state + concurrent RPCs via RWMutex.

### Linux guest quality

- VirtioFS guest mounts default to `cache=none` so writes from the
  host land in the guest immediately. (`fix/virtiofs-cache-none`)
- Per-OS VirtioFS mount command — Linux uses the right virtiofs
  mount syntax instead of the macOS form.
  (`fix/agent-virtiofs-mount-linux`)
- Cloud-init `Continue with autoinstall?` prompt suppressed via
  kernel cmdline + `interactive-sections` so unattended installs
  do not stall. (`fix/cloud-init-autoinstall-unattended`)

### Privileged-helper hardening

- `cove-helper` LaunchDaemon no longer crash-loops when invoked for
  VM-dir-independent subcommands: skips the `~/.vz/vms` ensure step
  in those cases, so the helper runs cleanly under root with no
  `HOME`. (`fix/cove-helper-daemon-home`)

### VM forking

- `cove fork <src> <child>` clones a VM via APFS clonefile (`ForkVMDisk`
  primitive), generates a fresh `machine.id` for the child, and shares
  storage copy-on-write with the source until first divergent write.
  A CoW divergence test pins the semantics. Disk-snapshot restore is
  reframed in the docs as a special case of this fork primitive.
  (`feat/cove-run-restore-snapshot`, design `docs/designs/013-vm-fork.md`)
- `cove run -restore <name>` — explicit named-snapshot restore on VM
  start, mutually exclusive with `-no-resume`. Lives on the same
  branch as `cove fork` and ships together.

### CLI quality of life

- `cove rename | export | import | config` as top-level aliases for
  `cove vm <cmd>`.
- `cove -version` flag in addition to the `cove version` subcommand,
  for naive wrappers and CI checks that pass `-version`.
- "did you mean" typo suggester on unknown commands (exit 2).
- `cove dump-docs -type cli | api | mcp [-pretty]` for machine-readable
  discovery: 31 CLI commands, 26 HTTP endpoints, 19 MCP tools.
- `cove ctl agent exec --` no longer scavenges payload flags past the
  separator, so `ctl agent exec -- ls -lah` reaches the guest intact.
  (`fix/agent-exec-dashdash`)
- `cove serve` discovers VMs by filesystem scan and includes the
  legacy `~/.vz/<name>/` layout, matching the discovery scope already
  in `cove list`. (`fix/serve-discovery-scope`)

### Misc

- Background diagnostics migrated to `log/slog`.
- Headless mode (`cove run -headless`) does not create an AppKit
  window; `cove ctl gui open` attaches one on demand.

## Fixes since the first 0.1 cut

A second smoke pass on a host with legacy `~/.vz/<name>/` VMs (no
alias migration history) surfaced four issues that would have shipped
broken. All are fixed in this tag:

- **`cove list` now sees legacy VMs.** `vmconfig.List` filtered alias
  symlinks via `entry.IsDir()` (false for symlinks-to-dirs) and never
  scanned the legacy `~/.vz/` root. `cove list` reported "No VMs
  found" on hosts with VMs at `~/.vz/<name>/`. List now follows alias
  symlinks and also scans the legacy root, planting aliases on sight
  so subsequent runs find them via the normal path.
- **`/v1/vms` reported every VM as "running".** The HTTP gateway's
  list response carried a hard-coded `Status: "running"` string.
  Removed; clients call `/v1/vms/<name>/status` for state.
- **`cove up` fresh-install path-resolution diagnostics.** The fresh-
  install warning ("disk not found at stopVMAndInject") had no live
  trace. This release ships a path-trace via `VZ_DEBUG_INSTALL=1`
  through every resolver step and a regression test
  (`up_path_resolution_test.go`) that catches drift in the
  install/stop contract.
- **Disposable test no longer wipes `~/.vz/vms`.** Two disposable-
  clone tests called `os.RemoveAll(vmconfig.BaseDir())` *before*
  `t.Setenv("HOME", t.TempDir())`, so the path resolved against the
  developer's real `~/.vz/vms` and deleted the entire tree. This
  was the actual root cause of the v0.1 smoke failure that originally
  looked like a `cove up` bug. Tests now never call `RemoveAll` on
  `BaseDir()`. An fsnotify-based `installer_watch.go` (gated by
  `VZ_DEBUG_INSTALL`) is in place as a permanent regression detector
  for any future code path that wipes vmDir during install.

## What's out (deferred to 0.2)

Tracked in `docs/blockers-next.md` on the `next` branch with
reproduction, evidence, and root-cause hypotheses.

- **`cove up -user <name>` from fresh install.** The disposable-test
  fix above eliminated the "ghost wipe" reproduction we had for this
  blocker, but a clean fresh-install end-to-end smoke run on the new
  diagnostics has not yet been completed against this tag. Resolver
  flow is instrumented and a regression test is in place. `cove up`
  on an already-installed VM still works.

## Known warts (non-blocking)

Surfaced by the reduced 0.1 smoke on `hermes-mlx-go-60g-v10` and the
follow-on legacy-host pass; none of these block 0.1:

- `cove serve -port <n>` is not a valid flag. Use `-http <addr>`.
- `suspend` / `resume` are not stand-alone verbs; the lifecycle is
  bound to `cove run` (auto-suspend on quit, auto-resume on next
  run). README maturity table reflects this.

The two warts called out at the first 0.1 cut — synchronous
snapshot-save `i/o timeout` on large `.vmstate` files, and `cove
serve` only scanning `~/.vz/vms/*` — are both resolved in this tag
(see "Async operations (LRO)" and "CLI quality of life" above).

## Commits included

The full `git log --oneline 1eb1623..v0.1.0` range is captured at
tag time and lists every commit in this release. Highlights:

- VM-operation HTTP/MCP surface, control socket, snapshot/restore,
  per-VM operations LRO.
- OCI pull for cove, lume tar-split, and cirruslabs/tart formats;
  cove→lume export (dry-run).
- Guest-agent dual-port (daemon 1024 / user 1025) routing for
  TCC-protected operations; auto-upgrade with directional version
  compare; configurable health monitor.
- Linux guest fixes: VirtioFS `cache=none`, per-OS mount command,
  unattended cloud-init.
- Privileged-helper hardening (`cove-helper` no longer crash-loops
  on VM-dir-independent subcommands).
- `cove fork` + `ForkVMDisk` APFS-clonefile primitive with fresh
  per-child machine identity; `cove run -restore <name>` for
  explicit named-snapshot restore on start.
- Smoke-blocker fixes: legacy `~/.vz/<name>/` discovery in `cove
  list` and `cove serve`; `Status: running` lie removed from
  `/v1/vms`; `cove up` resolver instrumentation; disposable tests
  no longer wipe `~/.vz/vms`.

The broader development arc (the beat-lume roadmap, the package
extractions — `internal/ociimage`, `internal/vmconfig`,
`internal/control/operations`, `internal/password`, `internal/pcap`,
`internal/bytefmt`, `internal/version`, `internal/diskimages2`, the
`agent`/`bpe`/`state` splits, and the vzkit helper inlining) arrived in
the hundreds of commits preceding `1eb1623`.

## Audit trail

Full ship-readiness audit: `docs/ship-audit-2026-04-24.md`.
Strategic roadmap: `docs/designs/011-beat-lume-roadmap.md`.
Deferred blockers: `docs/blockers-next.md` on the `next` branch.

## Caveats

- Apple Silicon only. macOS 14+ recommended on the host.
- Guest macOS 13, 14, 15 tested. FileVault may interfere with
  kcpassword-driven auto-login on macOS 15.
- `cove inject` still requires `sudo` for correct root:wheel ownership
  on LaunchDaemon plists; launchd silently ignores daemons with
  incorrect ownership.
