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
- "did you mean" typo suggester on unknown commands (exit 2).
- `cove dump-docs -type cli | api | mcp [-pretty]` for machine-readable
  discovery: 31 CLI commands, 26 HTTP endpoints, 19 MCP tools.

### Misc

- Background diagnostics migrated to `log/slog`.
- Headless mode (`cove run -headless`) does not create an AppKit
  window; `cove ctl gui open` attaches one on demand.

## What's out (deferred to 0.2)

Both deferrals are tracked in `docs/blockers-next.md` on the `next`
branch with reproduction, evidence, and root-cause hypotheses.

- **`cove up -user <name>` from fresh install.** The fresh-install path
  has a known path-resolution bug: install reports 100%, but the VM
  directory is missing before provisioning. `cove up` on an
  already-installed VM still works.
- **`cove pull` of upstream lume-format OCI images.** Lume's public
  ghcr.io images ship `disk.img` as named tar parts assembled by
  filename; cove expects LZ4 chunks with role/size/chunk-index
  annotations. Cove-format pull works; lume-format importer lands in
  0.2.

## Known warts (non-blocking)

Surfaced by the reduced 0.1 smoke on `hermes-mlx-go-60g-v10`; none of
these block 0.1:

- `cove ctl snapshot save` over the control socket returns `i/o
  timeout` client-side for large `.vmstate` files (~9 GB) because the
  default socket read deadline fires before the server finishes. The
  save completes successfully; workaround is `-timeout 120s`. The real
  fix is to route `snapshot save` through the async
  `/v1/operations/*` path.
- `cove serve` VM discovery only scans `~/.vz/vms/*/control.sock`.
  Running VMs placed at `~/.vz/<name>/control.sock` are not discovered
  by the HTTP gateway. VMs created by `cove up` / `cove install` land
  under `~/.vz/vms/` and are picked up correctly.
- `cove serve -port <n>` is not a valid flag. Use `-http <addr>`.

## Commits included

Since the previous development cut (`1eb1623`):

```
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
