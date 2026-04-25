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

## Empirical verification (CLOSES ROADMAP #21)

Status: **deferred to a live VM run.**

The worktree environment cannot exercise a running guest from a host pane.
The empirical close criterion — "does VirtioFS-mount of a `~/Documents` path
succeed via the user agent without a manual TCC grant?" — needs a VM with the
LaunchAgent shipping and a logged-in user. The verification protocol:

1. `cove up -user tmc -password tmc` — fresh VM, user logged in.
2. `cove volumes add /Users/tmc/Documents docs` — tag a host VirtioFS share.
3. Wait for auto-mount.
4. `cove ctl agent-exec --daemon ls /Volumes/docs` — should fail with
   "Operation not permitted" (proves the TCC bite still exists).
5. `cove ctl agent-exec ls /Volumes/docs` — should succeed (user agent default,
   inherits TCC).
6. `cove ctl agent-write /Volumes/docs/test.txt 'hello'` — should succeed
   (path-aware routing picks user agent).
7. Confirm `test.txt` appears on the host.

Expected outcome: steps 5–7 succeed without any manual TCC grant in System
Settings, closing #21. If they fail, the LaunchAgent has a TCC gap of its own
(e.g., the `vz-agent-user` binary needs Full Disk Access too) and the brief
should be amended with the unblocking step.

The macOSWorld harness D1ECD40A is unblocking is the natural acceptance vehicle
— its M1 user-setup checklist removes the manual Accessibility grant once #21
is empirically green.

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
