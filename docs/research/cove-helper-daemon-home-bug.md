# `cove helper daemon` fails to start as root: EROFS on `/var/root/.vz/vms`

## Summary

`sudo cove helper daemon` exits before reaching `helperDaemon()` because
`main()` unconditionally calls `vmconfig.EnsureDir`, which performs an
`os.MkdirAll` rooted at `os.UserHomeDir()`. Under `sudo` (or launchd) the
process runs as root, `$HOME` is `/var/root`, and `/var/root` lives on the
SIP-sealed system volume. The mkdir returns `EROFS`, `EnsureDir` propagates
the error, and `main` exits with `error: create VM dir: ...`.

Effect: the privileged helper daemon launchd is supposed to run can never
start. This breaks `cove helper install` end-to-end because the daemon
never opens its socket.

## Root cause

`main.go` (pre-fix):

```go
// Resolve VM directory using registry (handles migration and VM selection)
var err error
vmDir, err = vmconfig.EnsureDir(vmName, vmDir)
if err != nil {
    fmt.Fprintf(os.Stderr, "error: %v\n", err)
    os.Exit(1)
}
```

`internal/vmconfig/paths.go`:

```go
func BaseDir() string {
    homeDir, _ := os.UserHomeDir()
    return filepath.Join(homeDir, ".vz", "vms")
}

func EnsureDir(vmName, currentDir string) (string, error) {
    ...
    resolvedDir := ResolveDir(vmName, currentDir)
    if err := os.MkdirAll(resolvedDir, 0755); err != nil {
        return "", fmt.Errorf("create VM dir: %w", err)
    }
    ...
}
```

`os.UserHomeDir()` returns `/var/root` for uid 0 on macOS. Writes anywhere
under `/var/root` fail with `EROFS` because the system volume is sealed;
`/var/root` is on `/`, not the writable Data volume.

## Fix

Allowlist the small set of subcommands that genuinely don't need a per-VM
directory and skip `EnsureDir` (and `applyVMConfig`) for them:

```go
if !subcommandSkipsVMDir(flag.Args()) {
    vmDir, err = vmconfig.EnsureDir(vmName, vmDir)
    if err != nil { ... }
    applyVMConfig(vmDir)
}
```

The allowlist (`cli_skip_vmdir.go`) starts with the unambiguous cases:

- `helper` — entire subtree (install/uninstall/status/daemon) is system-wide,
  not VM-scoped.
- `version` — pure print, no I/O.

A unit test (`cli_skip_vmdir_test.go`) pins the membership so adding to the
list is a deliberate code change.

## Why an allowlist, not "swallow EROFS"

The natural-looking alternative is heuristic:

```go
vmDir, err = vmconfig.EnsureDir(...)
if errors.Is(err, syscall.EROFS) {
    // ignore — assume the subcommand doesn't need it
}
```

We rejected this because:

1. **Explicit > heuristic.** "These commands don't need `~/.vz/vms`" is a
   property of the command, not of the filesystem. The allowlist names the
   commands; the EROFS check infers them from a side effect.
2. **EROFS is a real error for normal commands.** `cove run`, `cove install`,
   `cove provision`, etc. genuinely need a writable `~/.vz/vms`. If a user
   somehow lands in that state (read-only `$HOME`, broken filesystem,
   mis-set `HOME`), they should see a clear error rather than silently
   continuing into a code path that will fail later with a confusing
   downstream message.
3. **Other errno values matter too.** `EnsureDir` can fail with `EACCES`,
   `ENOSPC`, or migration errors. None of those should be swallowed for any
   command. Filtering on errno class is a slippery slope.
4. **Adding a new VM-dir-independent command is rare and deliberate.** The
   allowlist is small (currently 2 entries) and changes infrequently. The
   ergonomic cost is negligible; the failure mode it prevents (silent
   misconfiguration) is much worse.

The allowlist optimizes for the case the bug exposed (`helper daemon` as
root) without weakening error reporting for everyone else.

## Repro

Before the fix:

```
$ sudo ./cove helper daemon
error: create VM dir: mkdir /var/root/.vz: read-only file system
$ echo $?
1
```

After the fix `helperDaemon()` is reached and it then enforces its own
"must be run as root" check (which `sudo` satisfies), proceeding to listen
on `/var/run/cove-helper.sock`.

## Files changed

- `main.go` — gate `EnsureDir`/`applyVMConfig` behind `subcommandSkipsVMDir`.
- `cli_skip_vmdir.go` — new allowlist + helper.
- `cli_skip_vmdir_test.go` — pin allowlist membership.

## Follow-ups (not in this commit)

- Consider routing `serve`, `gc`, `vm list` through the same gate once we've
  audited each for actual VM-dir dependence. Today they go through the
  default path; if any of them are run as root in CI or via launchd in the
  future, expand the allowlist deliberately.
- Audit other `vmconfig.*` calls reachable from `helper` paths to confirm
  none of them re-introduce `os.UserHomeDir()` on a root-owned process.
