# TCC via the user agent (ROADMAP #21b)

## Background

The cove guest has two agents:

- `vz-agent` (LaunchDaemon, vsock port 1024) runs as root.
- `vz-agent-user` (LaunchAgent, vsock port 1025) runs in the logged-in user's GUI session.

Apple's TCC (Transparency, Consent, and Control) layer gates root access to a
small set of paths — `~/Library`, `~/Documents`, `~/Desktop`, `~/Downloads`,
`~/Movies`, `~/Music`, `~/Pictures`, and any non-system `/Volumes/<tag>` mount
including VirtioFS shares. On a SIP-enabled guest the daemon agent has no Full
Disk Access grant, so file operations on those paths fail with "Operation not
permitted" even as root.

Roadmap #21 originally proposed shipping a PPPC `.mobileconfig` profile to
grant FDA to the daemon. That path is closed: `profiles install` was deprecated
in macOS 11, `/var/db/ConfigurationProfiles/Setup/` is honored only by DEP, and
unsigned profiles cannot install silently without MDM enrollment. See
[/tmp/EDDC36F6-reply-tcc-profiles-stop.md](../../../) for the empirical STOP
report.

#21b takes a different route: every TCC-protected operation runs on the user
agent, which inherits the GUI session's TCC grants natively. No profile, no
MDM, no SIP toggle.

## Op → port mapping

| Op (control-socket type)              | Port | Reason                                                |
|---------------------------------------|------|-------------------------------------------------------|
| `agent-ping`, `agent-info`            | 1024 | Health and metadata; root-only signals.               |
| `agent-exec`                          | 1024 | Caller asked for root explicitly.                     |
| `agent-user-exec`, `-stream`          | 1025 | Caller asked for user session.                        |
| `agent-shutdown`, `agent-reboot`      | 1024 | System power.                                         |
| `agent-sshd`                          | 1024 | `launchctl` on a system LaunchDaemon.                 |
| `agent-mount-volumes`                 | 1024 | `mount_virtiofs` requires root; mount itself works.   |
| `agent-read`, `agent-write`           | path-aware | TCC-protected user paths → 1025; system → 1024. |
| `agent-cp`                            | 1024 today | Streaming refactor pending — see Gaps below.   |
| `agent-connect`, `agent-status`       | n/a  | Connection management.                                |

The `cove ctl agent-exec` CLI defaults to `agent-user-exec` (user); pass
`--daemon` to opt into root.

## What changed in agent_control.go

- New file `agent_routing.go`: defines `agentRoute` and the path-aware
  `agentRouteFor(op, path, linuxGuest)`. Single source of truth.
- `handleAgentRead` and `handleAgentWrite` now call `agentRouteFor` and
  shell out via the user agent (`UserExec` calling `/usr/bin/base64`) when
  the path lives in a TCC-protected directory.
- Daemon-served reads and writes are unchanged for system paths.
- `linuxMode` short-circuits the helper to the daemon — Linux guests have
  no UserAgent service.
- `slog`-style log lines via `log.Printf` (`"agent-route: read /Users/me/...
  -> user agent (TCC path)"`) make the routing visible during debugging.

## "User agent unreachable" error path

`handleAgentRead`/`handleAgentWrite` call `s.getUserAgent()`, which tries the
existing connection, then `connectUserAgentLocked`, which falls through to
`bootstrapUserAgentLocked` if the LaunchAgent is missing. Failure modes:

- No GUI user logged in (`/dev/console` owned by root) → `getUserAgent` returns
  "no logged-in GUI user on /dev/console". The control-socket response surfaces
  this verbatim; callers see `read: user agent: no logged-in GUI user on
  /dev/console` rather than a silent fallback to the daemon (which would also
  fail with TCC).
- LaunchAgent missing on a fresh VM → bootstrap installs the plist and
  retries; if that also fails, the original connect error and the bootstrap
  error are both surfaced.
- Linux guest → `connectUserAgentLocked` returns the connect error directly
  with no bootstrap attempt; the helper's `linuxMode` guard means we never
  reach this code path for read/write today, but it remains as a safety net.

## Empirical verification (post-v0.1.0 result)

Status: **routing-only is not sufficient; manual FDA grant required.**

The v0.1.0 smoke test (B32D, 2026-04-26) ran the verification protocol on
a real VM (`hermes-mlx-go-60g-v9`, five VirtioFS shares, both daemon and
user agent connected, console user logged in via auto-login). Steps 4–6
all timed out:

| Step | Probe                                                | Result          |
|------|------------------------------------------------------|-----------------|
| 4    | `agent-exec --daemon ls /Volumes/<share>`            | timeout (TCC bite, expected) |
| 5    | `agent-exec ls /Volumes/<share>` (user agent)        | **timeout (≥90s)** |
| 6    | `agent-write /Volumes/<share>/test.txt`              | timeout         |
| —    | `stat /Volumes/<share>` via user agent               | succeeds in 4.5ms |
| —    | `mount` (via user agent) shows the share is mounted  | succeeds        |

The split between `stat` (succeeds) and `readdir` (times out) confirms
the gap: macOS TCC blocks directory enumeration on non-system /Volumes
mounts but not metadata lookups. The user agent has the same gap as the
daemon for these paths.

### Why routing alone does not close the gap

TCC's Full Disk Access is granted **per binary path**, not per user
session. macOS keys grants in `/Library/Application Support/com.apple
.TCC/TCC.db` on the binary's signing identity (or path, for unsigned
binaries) plus the requesting service. A LaunchAgent running in the
Aqua session inherits user-id and the user's keychain, but FDA is a
separate consent that must be granted explicitly to `/usr/local/bin/
vz-agent` (the in-guest path the user agent runs from).

`tccutil` only supports `reset`, not `grant`. PPPC `.mobileconfig`
profiles can grant FDA but require MDM enrollment to install silently
on macOS 11+ (`profiles install` is deprecated). There is no fully
unattended path for FDA grant on a stock SIP-enabled guest.

### v0.1.1 manual workaround

Until a tighter path lands, users must grant FDA to the in-guest
`vz-agent-user` binary by hand on first install:

1. `cove up …` to install macOS and provision the guest agents.
2. Inside the guest GUI: open **System Settings → Privacy &
   Security → Full Disk Access**.
3. Click `+`, navigate (`Cmd+Shift+G`) to `/usr/local/bin/`,
   select `vz-agent`, and toggle the switch on.
4. `cove ctl agent-exec ls /Volumes/<share>` should now succeed.

`cove ctl agent-status` reports `daemon=connected, user=connected`
even before the FDA grant — TCC failures are silent timeouts on
readdir, not connection errors.

`cove doctor` now detects this while the VM is running. It uses the user
agent to find the first non-system `/Volumes` mount, runs a bounded
`ls` probe against that path, and reports the Full Disk Access grant
steps if the probe times out or fails. Use `cove doctor --tcc-path
/Volumes/<tag>` to force a specific VirtioFS mount.

### Tighter unattended paths (follow-ups, not landed)

- Bundle the in-guest agent inside a TCC-aware app (signing identity
  + FDA pre-grant via DEP) — heavy lift, requires Apple Developer
  account and MDM.
- Trigger the same FDA probe during `cove up` once a guest desktop and
  user agent are known to be available.
- Use VirtioFS `cache=none` plus a launchd `BootstrapBootstrap` flag
  so the mount is owned by the user session — investigation pending.

The macOSWorld harness D1ECD40A is unblocking is the natural acceptance
vehicle for the FDA-prompt path; its M1 user-setup checklist already
needs a manual Accessibility grant, so adding FDA is a small extension.

## Gaps deferred

- **`agent-cp`**: large-file streaming via the UserAgent service would require
  a new `UserCopyIn`/`UserCopyOut` RPC (currently only `UserExec` is exposed).
  Today `agent-cp` always uses the daemon; for TCC paths users should fall
  back to `agent-user-exec` with `cp` or `tar`. Adding streaming RPCs to the
  user agent is a self-contained follow-up.
- **`agent-mount-volumes` post-mount probe**: we still mount via the daemon.
  A user-agent probe (`UserExec ls /Volumes/<tag>`) after mount would surface
  TCC failures earlier; left out for scope.
- **`agent-write` heredoc size limit**: data is base64-encoded into a single
  argv element. macOS argv has a 1 MiB limit (`getconf ARG_MAX`); writes
  larger than ~700 KiB pre-encode will fail. Acceptable for config files;
  large blobs need the streaming follow-up.

## References

- `docs/designs/007-vzscript-host-files-NLM-REVIEW.md` — original argument for
  routing user-context ops through port 1025.
- `cmd/vz-agent/user_server.go` — UserExec implementation (runs in current
  user's process tree, inherits TCC).
- `agent_routing.go`, `agent_routing_test.go` — the helper and table-driven
  tests (19 op cases, 18 path cases).
- /tmp/EDDC36F6-reply-tcc-profiles-stop.md — STOP report for the original
  PPPC profile path.
