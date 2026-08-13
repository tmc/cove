---
title: EnsureDir concurrent VM-directory creation (2026-08-13)
description: Hardening vmconfig.EnsureDir against races between concurrent cove invocations on the same VM name.
icon: flask
---
# `vmconfig.EnsureDir` concurrency hardening (2026-08-13)

## Failure mode

A production incident: `~/.vz/vms/grok-lab` (a plain, non-`.covevm`-suffixed
legacy VM directory — see `PathCandidates`/`ExistingPath` in
`internal/vmconfig/paths.go`) was removed with `rm -rf`. A concurrent or
detached `cove` process raced to recreate it. A subsequent `cove` invocation
hit `os.MkdirAll` returning `mkdir ...: file exists` inside `EnsureDir`,
which was surfaced verbatim as a fatal `"create VM dir: %w"` error — even
though the directory legitimately existed at that point and the command
could otherwise have proceeded normally.

## Root cause

`EnsureDir` (`internal/vmconfig/paths.go`) resolves the target VM directory
and calls `os.MkdirAll(resolvedDir, 0755)`, historically treating any
non-nil error as fatal. Go's own `os.MkdirAll` already re-`Lstat`s the path
and swallows the `Mkdir` error if the path is a directory by the time it
checks — but that only covers the race *inside* the single `MkdirAll` call.
It does not cover:

- Transient states where the leaf path briefly exists as something other
  than a directory (e.g. a partially-written entry from a concurrent
  `EnsureDir`/inject/provision sequence) and resolves to a real directory
  moments later.
- Any future change to the mkdir path that reintroduces a hard EEXIST
  failure without re-checking directory state.

Two `cove` processes racing on the *same, not-yet-existing* VM name is a
realistic scenario: `cove up`, `cove run`, and background/detached
invocations (e.g. from vzscript or agent tooling) can all resolve to the
same `ResolveDir` target concurrently.

## Fix

`EnsureDir` now treats an `os.MkdirAll` error as non-fatal if a follow-up
`os.Stat` shows the resolved path is a real directory — i.e. "someone else
already won this race, and that's fine":

```go
if err := os.MkdirAll(resolvedDir, 0755); err != nil {
    // A concurrent cove invocation (e.g. two terminals racing on the
    // same VM name) can win the mkdir between our dangling-link check
    // and this call. If the path is a real directory now, treat it as
    // success instead of failing the whole command.
    info, statErr := os.Stat(resolvedDir)
    if statErr != nil || !info.IsDir() {
        return "", fmt.Errorf("create VM dir: %w", err)
    }
}
```

This mirrors the tolerance `os.MkdirAll` already applies internally, just
one layer up, so `EnsureDir` stays correct even if the underlying error
doesn't originate from the exact leaf-directory race that stdlib already
handles.

### Audit of the rest of `EnsureDir`'s call chain

`EnsureDir` also calls `markFinderPackage` and `EnsureAlias` /
`EnsureCompatibilityAlias` (`internal/vmconfig/migrate.go`) immediately
after the `MkdirAll`. These were audited for the same class of
check-then-act race:

- `markFinderPackage` (`internal/vmconfig/finderinfo_darwin.go`) reads and
  rewrites a `com.apple.FinderInfo` xattr. It's idempotent — concurrent
  callers each set the same package bit — so a race here is at worst a
  redundant write, never an error.
- `EnsureAlias` and `EnsureCompatibilityAlias` (`internal/vmconfig/migrate.go`)
  already tolerate a concurrent winner: both call `os.Symlink` and
  explicitly ignore `os.IsExist(err)`, and both short-circuit early if the
  alias already resolves to the intended target. No change needed.

### Why not a lock file

`internal/coved/image_gc.go`'s stale-lock pattern (PID/timestamp lock file,
break locks from dead holders) was considered as a more robust alternative,
per that package's precedent for protecting multi-step image materialization
against concurrent gc. It was not used here: `EnsureDir`'s only non-idempotent
step is directory creation itself, which Go's `os.MkdirAll` plus the
Stat-fallback above already makes safe and race-free without any
cross-process coordination. Introducing a lock file would add failure modes
(stale lock cleanup, lock file lifecycle) to guard a step that is already
provably safe. Simple tolerant-retry is preferred while it stays correct;
revisit if `EnsureDir` grows additional non-idempotent steps.

## Test

`internal/vmconfig/paths_concurrency_test.go` (`TestEnsureDirConcurrentSameName`)
runs 16 goroutines calling `EnsureDir("racer", "")` concurrently against a
sandboxed `COVE_STATE_DIR`/`HOME` and asserts every call succeeds and
resolves to the same path. Run with `-race`:

```
go test ./internal/vmconfig/... -race -run TestEnsureDirConcurrentSameName -v
```

Note: because Go's `os.MkdirAll` already self-heals the simple
directory-vs-directory leaf race (it re-`Lstat`s after a failed `Mkdir` and
returns success if the path is now a directory), this test does not by
itself distinguish the fixed `EnsureDir` from a version without the extra
`Stat` fallback — both pass under this specific race shape today. The test
is kept as a regression guard for `EnsureDir`'s concurrent-call contract as
a whole (including the alias/package-bit steps audited above), and the
`Stat` fallback is kept as defense-in-depth for error shapes that aren't
plain "the directory already exists" (e.g. permission races, or future
changes to what `MkdirAll` is called on).
