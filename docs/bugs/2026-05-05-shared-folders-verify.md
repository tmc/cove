---
status: Draft
date: 2026-05-05
---

# Shared Folders Verification

This file records the R41 shared-folder verification gates.

## Unit Gates

Run from the repo root:

```sh
go test ./internal/agent
go test . -run 'TestSharedFolder(AddLiveApplies|StatusLinuxUses|Default)'
```

Expected result: routing treats `/Volumes/My Shared Files/...` as a user path,
macOS add creates the `~/<tag>` symlink through the user agent, and Linux still
uses `/mnt/<tag>` without the macOS mount root.

## Live Gate

The full live gate needs a macOS guest that has reached the desktop and has the
user agent running.

```sh
mkdir -p /Users/tmc/test-folder
echo ok >/Users/tmc/test-folder/somefile
cove -vm mlxgo-fresh-headed2-20260505 shared-folder add /Users/tmc/test-folder test-folder rw
cove -vm mlxgo-fresh-headed2-20260505 ctl agent-exec -- ls "/Volumes/My Shared Files/test-folder"
cove -vm mlxgo-fresh-headed2-20260505 ctl agent-exec -- readlink /Users/<guest-user>/test-folder
cove -vm mlxgo-fresh-headed2-20260505 ctl agent-exec -- cat "/Volumes/My Shared Files/test-folder/somefile"
```

Expected result:

```
/Volumes/My Shared Files/test-folder is mounted
~/test-folder -> /Volumes/My Shared Files/test-folder
cat prints ok
```

If the VM is still stopped at the login screen and auto-login has not fired, the
user agent is not available and the live gate is blocked by the login fix. In
that state cove should print a plain retry message instead of requiring users to
debug launchd, ports, or daemon permissions.

For automation, probe the real VirtioFS path under `/Volumes/My Shared Files`.
The home path is a symlink for the logged-in guest user; a forced daemon command
that follows `/Users/<guest-user>/<tag>` can still hit macOS FDA/TCC policy.
