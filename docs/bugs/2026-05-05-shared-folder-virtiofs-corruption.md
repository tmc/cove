---
status: Open
date: 2026-05-05
---

# Shared Folders: macOS VirtioFS Source Read Corruption

QA reported a macOS guest read-integrity failure on
`mlxgo-fresh-headed2-20260505` after mounting the host checkout
`/Users/tmc/ml-explore` as `ml-explore`.

The host checkout is clean:

```sh
perl -ne 'if (index($_, chr(0)) >= 0) { print $. . "\n" }' \
  /Users/tmc/ml-explore/mlx-go/internal/setuphelper/setuphelper.go
nl -ba /Users/tmc/ml-explore/mlx-go/internal/setuphelper/setuphelper.go | sed -n '176,188p'
```

The NUL probe prints nothing on the host, and line 184 is ordinary Go source:

```go
			}
```

Inside the VM GUI Terminal, the same checkout path is readable through the
shared folder, but the content is corrupt:

```sh
perl -ne 'if (index($_, chr(0)) >= 0) { print $. . "\n" }' \
  internal/setuphelper/setuphelper.go
```

Output:

```text
184
```

`go test ./internal/mlxc` then fails with:

```text
internal/setuphelper/setuphelper.go:184:1: invalid NUL character
```

Screenshot evidence:

- `/tmp/mlx-go-gui-terminal-nul-probe-setuphelper.jpg`
- `/tmp/mlx-go-gui-terminal-go-test-internal-mlxc-1.jpg`

## Current cove State

`cove shared-folder status` reports the intended mount and home link:

```text
Configured shared folders: 1
  ml-explore    rw  /Users/tmc/ml-explore
    guest: /Volumes/My Shared Files/ml-explore
    link:  ~/ml-explore -> /Volumes/My Shared Files/ml-explore
Control socket: available
Guest agent: available
Guest mount: mounted at /Volumes/My Shared Files
```

The existing FDA probe currently passes:

```sh
cove -vm mlxgo-fresh-headed2-20260505 doctor --tcc-path \
  '/Volumes/My Shared Files/ml-explore'
```

But agent file opens through `/Volumes/My Shared Files/...` can still return
`Operation not permitted`, while GUI Terminal reads the same path and sees
corrupt bytes. That makes this distinct from the earlier "can't read the mount"
UX issue: the GUI session can read the mount, but file content is not trustworthy
for source builds.

## Working Theory

This is likely a macOS guest VirtioFS coherence/integrity problem, not an
`mlx-go` source edit. Linux shared-folder mounts default to `cache=none`; macOS
`mount_virtiofs` does not expose an equivalent cache mode in cove today.

Until the mount path is proven byte-for-byte safe, do not use macOS guest
VirtioFS shared folders as the active checkout for Go builds. Copy or clone the
checkout onto the guest disk first, then build from the guest-local path.

## Minimum Follow-up

Add a cove-side integrity probe for shared folders:

1. Choose one small host file under each configured share.
2. Hash it on the host.
3. Hash the same guest path from the GUI/user-session path.
4. Warn if bytes differ or if a NUL byte appears in a text file.

The probe should run from `cove doctor` or `cove shared-folder status --verify`
and should not rely on the root daemon path for macOS guests.

