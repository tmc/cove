**Status: archived 2026-04-28 — superseded by [docs/designs/ROADMAP.md](../ROADMAP.md). This was a session-scope bug tracker dated 2026-03-27 (different scope from the multi-version roadmap). Most items here are now shipped or obsolete. Preserved for forensic value.**

---

# cove Roadmap

Last updated: 2026-03-27

Strategic release roadmap: `docs/designs/011-beat-lume-roadmap.md`

## Bugs (Open)

### High Priority

- **agent-exec treats `--` as command** — `ctl agent-exec -- ls` passes `--` as the executable name. (#28)

### Medium Priority

- **Cloud-init autoinstall confirmation** — Ubuntu autoinstall prompts "Continue with autoinstall? (yes/no)" instead of being fully unattended. Need `autoinstall` kernel cmdline param. (#26)
- **VirtioFS stale directory listings** — Host-side file changes can take time to appear in guest VirtioFS mounts. Investigate mount options for consistency. (#27)

## In Progress

- **LaunchAgent mode for vz-agent** — Same binary with `-mode daemon|agent`, port 1025. User session context for FDA/TCC grants, GUI access, VirtioFS. Start with UserExec RPC. (#22)
- **mlx-go-generate in VM** — Get mlx-lm-generate working and benchmarked. Metal shader compilation errors with cooperative_tensor ops. FD45 debugging. (#15)

## Planned (High Priority)

- **Full Disk Access automation** — vz-agent LaunchDaemon blocked by TCC. Options: PPPC .mobileconfig profile, TCC.db with SIP off, or LaunchAgent (which inherits user FDA). (#21)
- **Agent health monitoring** — Periodic ping with auto-reconnect. Currently reconnects lazily on next RPC call. (#17)
- **Integration tests** — Run full NSError retain fix chain integration tests with live VM. (#11)

## Planned (Medium Priority)

- **Agent auto-upgrade on VM boot** — Check agent version on connect, upgrade if stale. (#19)
- **Private debug API investigation** — Check if Virtualization framework private debug selectors are usable (GDB stub, memory inspection, etc.). (#23)

## Planned (Lower Priority)

- **Control socket → gRPC migration** — Replace JSON control socket with Connect-RPC. Proto infra already exists. Deferred — JSON socket works. (#20)
- **Agent log streaming** — Stream guest agent logs to host in real-time via new RPC.
- **VM template system** — Snapshot a provisioned VM as a template for fast cloning.
- **vzscript test framework** — Unit tests for vzscripts using mock agent.
- **Multi-VM orchestration** — Run commands across multiple VMs simultaneously.
- **Structured logging** — Replace log.Println with slog throughout.

## Completed (This Session)

| Commit | Description |
|--------|-------------|
| `93eff04` | `ctl text` fix: release server mutex between key events (#24, verified live against hermes-mlx-go-60g-v10 on 2026-04-24 — `ctl text "test/text\|"` typed all 10 chars into TextEdit including `/` and `\|`) |
| `0e2952c` | `keyNameToCode`: add shifted-punctuation aliases so `boot-commands key question` etc. resolve (#25) |
| `4161789` | Inline vzkit/input helpers into local `input_events.go` |
| (pending) | CLI: top-level `rename`/`export`/`import`/`config` aliases for `vm`; "did you mean" suggester for unknown commands (#29) |
| `b92b52c` | Test suites for VM cloning, provisioning paths, SIP automation (25 tests) |
| `54fa67a` | VM selector: detail panel, count label, empty state, vzscript button |
| `bbb9df8` | Update purego, apple, macgo dependencies |
| `83ddb7d` | Agent concurrent RPCs with RWMutex |
| `ce7cd45` | Include VM name in control socket error messages |
| `f62d5dc` | Agent version fields in InfoResponse proto |
| `d9a9166` | Agent self-SHA256 in startup log |
| `92567b7` | Live agent-upgrade command |
| `f88fe18` | Makefile proto-go target fix |
| `6fc2e90` | Headless Xcode CLT install via softwareupdate |
| `a835660` | Shared-folders vzscript |
| `3422859` | vmDir→socketPath fix (no global mutation) |
| `c5e1a45` | Concurrent vzscript execution (-parallel flag) |
| `0c9e7c7` | iTerm2 recipes for in-VM terminal setup |
| `241c47c` | Agent inject: Linux guest OS support |
| `b5ceb4b` | Nested virtualization for Linux VMs |
| `7c8b496` | iTerm2 relay over vsock port 1912 |
| `f1141ac` | UserAgent service in agent.proto |
| `5b110b8` | Auto-inject vz-agent during Linux cloud-init |
| `3cefdcc` | WebSocket-to-vsock relay for iTerm2 API |

### Upstream (apple module)

| Commit | Description |
|--------|-------------|
| `13e1e90` | SafeErrorFrom with retain for async blocks |

## Architecture Decisions

- **Concurrent vzscripts**: Channel-based dependency waiting, auto-detect UI recipes at parse time, shared mutex for UI serialization.
- **LaunchAgent vs LaunchDaemon**: Same `vz-agent` binary, `-mode` flag, port 1024 (daemon) / 1025 (agent). LaunchAgent gets user session TCC/FDA.
- **Control socket → gRPC**: Deferred. JSON socket works, proto infra ready when needed.
- **VirtioFS consistency**: Apple's public API has no caching/sync mode for VirtioFS. Stale listings are a guest-side cache issue. Linux: `cache=none` mount option. macOS: limited options.
