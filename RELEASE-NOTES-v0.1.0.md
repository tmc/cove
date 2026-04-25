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

### Guest agent (vsock)

- `vz-agent` runs as a LaunchDaemon inside the guest over vsock port
  1024. Exec, read, write, cp, self-upgrade, version reporting.
- Linux cloud-init auto-injects `vz-agent` at install time.
- Agent state + concurrent RPCs via RWMutex.

### CLI quality of life

- `cove rename | export | import | config` as top-level aliases for
  `cove vm <cmd>`.
- `cove -version` flag in addition to the `cove version` subcommand,
  for naive wrappers and CI checks that pass `-version`.
- "did you mean" typo suggester on unknown commands (exit 2).
- `cove dump-docs -type cli | api | mcp [-pretty]` for machine-readable
  discovery: 31 CLI commands, 26 HTTP endpoints, 19 MCP tools.

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

Both deferrals are tracked in `docs/blockers-next.md` on the `next`
branch with reproduction, evidence, and root-cause hypotheses.

- **`cove up -user <name>` from fresh install.** The disposable-test
  fix above eliminated the "ghost wipe" reproduction we had for this
  blocker, but a clean fresh-install end-to-end smoke run on the new
  diagnostics has not yet been completed against this tag. Resolver
  flow is instrumented and a regression test is in place. `cove up`
  on an already-installed VM still works.
- **`cove pull` of upstream lume-format OCI images.** Lume's public
  ghcr.io images ship `disk.img` as named tar parts assembled by
  filename; cove expects LZ4 chunks with role/size/chunk-index
  annotations. Cove-format pull works; lume-format importer and
  cove→lume `push --format lume` land in 0.2.

## Known warts (non-blocking)

Surfaced by the reduced 0.1 smoke on `hermes-mlx-go-60g-v10` and the
follow-on legacy-host pass; none of these block 0.1:

- `cove ctl snapshot save` over the control socket returns `i/o
  timeout` client-side for large `.vmstate` files (~9 GB) because the
  default socket read deadline fires before the server finishes. The
  save completes successfully; workaround is `-timeout 120s`. The
  async `/v1/operations/*` path is wired on the `next` branch and
  ships in 0.2.
- `cove serve` VM discovery only scans `~/.vz/vms/*/control.sock`.
  Running VMs placed at `~/.vz/<name>/control.sock` are not discovered
  by the HTTP gateway (the legacy-layout scan in `cove list` does
  not yet cover `cove serve`). VMs created by `cove up` / `cove
  install` land under `~/.vz/vms/` and are picked up correctly.
- `cove serve -port <n>` is not a valid flag. Use `-http <addr>`.
- `suspend` / `resume` are not stand-alone verbs; the lifecycle is
  bound to `cove run` (auto-suspend on quit, auto-resume on next
  run). README maturity table reflects this.

## In-flight on the `next` branch (preview of 0.2)

These items are merged onto `next` but not into the 0.1.0 tag.
They are listed here for orientation, not as 0.1 features:

- **Async snapshot save** — per-VM operations LRO; `ctl snapshot
  save -async`, `ctl operations get/list/wait`. (`feat/snapshot-save-async`,
  closes `cove ctl snapshot save -timeout` workaround above.)
- **Lume tar-split pull** — detect lume tar-split manifests and
  route to a separate importer; project lume config into cove's
  vmconfig with `lume-config.json` sidecar. (`feat/lume-tarsplit-pull`,
  the public-image-pull blocker.)
- **Agent auto-upgrade** — directional version compare prevents
  auto-downgrade; LaunchAgent + LaunchDaemon both bounced during
  upgrade. (`feat/agent-auto-upgrade`.)
- **Agent health monitor** — configurable health interval, slog
  reconnect logging, downtime tracking. (`feat/agent-health-monitor`.)
- **Linux fixes** — virtio-fs guest mount uses `cache=none`;
  cloud-init `Continue with autoinstall?` prompt suppressed via
  kernel cmdline + `interactive-sections`; per-OS virtio-fs mount
  command. (`fix/virtiofs-cache-none`, `fix/cloud-init-autoinstall-unattended`,
  `fix/agent-virtiofs-mount-linux`.)
- **`ctl agent exec --` separator** — `ctl` no longer scavenges
  payload flags past `--`. (`fix/agent-exec-dashdash`.)
- **`cove serve` legacy discovery** — list VMs by filesystem
  discovery, including legacy `~/.vz/<name>/` layout (matches the
  fix already in `cove list` for 0.1). (`fix/serve-discovery-scope`.)
- **TCC routing via user agent** — TCC-protected ops (VirtioFS
  mount access, etc.) routed through the user LaunchAgent on port
  1025; daemon on 1024 is path-aware. (Memory note
  `project_agent_routing.md`, branch in flight.)
- **`cove run -restore <name>`** — explicit named-snapshot restore
  on VM start, mutually exclusive with `-no-resume`. (TODO; in flight
  on `feat/cove-run-restore-snapshot`.)
- **Helper-daemon HOME fix** — fix HOME resolution for the privileged
  helper-daemon code path. (TODO; in flight on
  `fix/cove-helper-daemon-home`.)

## Commits included

Since the previous development cut (`1eb1623`):

```
706cf0d fix(test): stop wiping the real ~/.vz/vms during disposable tests
5d7eacb fix(up): instrument resolver flow + path-resolution regression test
01227a1 fix: strip Status:running lie from /v1/vms; polish README suspend/resume
1a636db fix(list): scan legacy ~/.vz/<name>/ layout and plant aliases on sight
a528635 feat(cli): add -version flag alias for the version subcommand
d4c8d9d fix(list): follow alias symlinks in vmconfig.List
e7c794b docs: keep next-as-staging branch model
ee098a9 docs: incorporate notebook review into v0.1 plan
f5fb611 docs: v0.1.0 publish checklist + post-0.1 roadmap
654e8b9 release: draft v0.1.0 release notes
7f115a4 docs: reduced 0.1 smoke test — all 12 items PASS on hermes
e23339f docs: cut cove up + lume-format pull from 0.1 ship gate
f57c087 docs: cove up smoke test result — FAIL (blocker)
bc5d0e5 password: move macOS hashing and kcpassword helpers into internal package
b63201b pcap: move libpcap writer into internal package
d384941 docs: mark -verbose crash as not reproducible
384b711 docs: cove pull smoke test — lume schema drift (fail)
99e9286 docs: clarify 0.1 ship gate — operate, not create; pause=suspend
441b9b7 docs: ship-readiness audit for cove 0.1 gate (2026-04-24)
4599ce2 cove: migrate background diagnostics to log/slog
96c7843 cmd/vz-agent: migrate logging to log/slog
24ca65e cli: suggest closest command on typo, exit 2
4ca5d28 cli: alias top-level rename/export/import/config to vm subcommands
93eff04 control: release s.mu while typing text to match ctl-key cadence
0e2952c boot: add missing punctuation aliases to keyNameToCode
4161789 input: inline vzkit/input helpers
```

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
