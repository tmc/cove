---
status: Draft
date: 2026-05-05
---

# Shared Folders: Hot-Mount EPERM and Guest Path UX

QA saw `mount_virtiofs: failed to mount /Volumes/My Shared Files: Operation not permitted`
on `mlxgo-fresh-headed2-20260505`, followed by root-agent reads of
`/Volumes/My Shared Files` failing with the same class of error. QA also expected
the host folder `/Users/tmc/ml-explore` to appear in the guest as `~/ml-explore`;
cove only reported the system mount path.

## Diagnosis

The fixed macOS VirtioFS mount root is `/Volumes/My Shared Files`. That path is
TCC-protected from the launchd daemon agent even when the daemon runs as root.
The 2026-04-25 routing work moved protected paths to the user agent, but the
repo history is explicit that routing alone did not close the FDA gap for
directory enumeration on non-system `/Volumes` mounts. `cove doctor` remains
the product-facing FDA diagnostic when the user agent is connected but the path
still times out or returns `Operation not permitted`.

The route helper already classifies `/Volumes/My Shared Files` as a user path:
all `/Volumes/<name>` paths route to the user agent except `Macintosh HD` and
`Macintosh HD - Data`. Exact regression coverage now pins:

```
/Volumes/My Shared Files
/Volumes/My Shared Files/<tag>/...
```

`cove ctl agent-exec -- ls "/Volumes/My Shared Files/ml-explore"` should use
the auto route and reach the user agent. Forced daemon commands such as
`cove ctl agent-exec --daemon -- ls "/Volumes/My Shared Files/ml-explore"` are
expected to fail on macOS guests because the daemon lacks FDA. If the auto route
reaches the user agent but still cannot enumerate the mount, run `cove doctor`
for the existing FDA grant check.

On the fresh QA VM, auto-login also failed. In that state the LaunchAgent never
starts, so the user agent is unavailable and shared-folder access remains
blocked until the first GUI login completes. The shared-folder symptom is
therefore partly gated by task #304 for fresh auto-login reliability.

## Verification Commands

These are maintainer diagnostics for the R41 bug. A normal user should not need
them; `cove shared-folder add` should either finish the mount and link creation
or tell the user to finish logging into the VM and retry.

Run these only when debugging the route after the VM reaches the desktop:

```sh
cove -vm mlxgo-fresh-headed2-20260505 ctl agent-status
cove -vm mlxgo-fresh-headed2-20260505 ctl agent-user-exec -- launchctl list
cove -vm mlxgo-fresh-headed2-20260505 ctl agent-exec -- ls "/Volumes/My Shared Files/ml-explore"
cove -vm mlxgo-fresh-headed2-20260505 ctl agent-exec --daemon -- ls "/Volumes/My Shared Files/ml-explore"
```

Expected result: automatic `agent-exec` works for the VirtioFS path, and forced
daemon access fails or reports a TCC-style permission error. If the user agent is
unavailable, the product-facing fix is to finish GUI login and rerun
`cove shared-folder add`; users should not need to inspect launchd or process
state.
