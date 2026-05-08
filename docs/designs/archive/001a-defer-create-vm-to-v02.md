# 001a — Defer `create_vm` via HTTP API to v0.2

**Status**: accepted
**Date**: 2026-04-17
**Relates to**: [001-cove-serve-http-mcp.md](001-cove-serve-http-mcp.md) §Creation

## Decision

`POST /v1/vms` remains wired in v0.1 — it accepts the request, creates an LRO, and marks it `failed` with `error.code = "not_implemented"` and a descriptive message. The actual install logic is deferred to v0.2 as a standalone extraction task.

## Why extraction is a Pillar B task

`installMacOSLikeVZ` (installer.go, ~1900 LOC) cannot be called from an HTTP handler goroutine without a non-trivial refactor:

1. **NSApplication / main-thread requirement.** The GUI path calls `getSharedApp()`, manipulates `NSWindow`, and dispatches objc calls that require `runtime.LockOSThread()` on the main thread. An HTTP handler goroutine has no path to the main thread without re-architecting the run-loop.

2. **Package-global flag vars.** The install code reads ~15 package-level vars directly (`vmDir`, `ipswPath`, `cpuCount`, `memoryGB`, `diskSizeGB`, `guiMode`, `unattended`, `provisionUser`, `provisionPassword`, etc.). Wrapping these into a `CreateVMOptions` struct and threading them through requires touching every internal call site — ~31 references across the file, plus callers in `macos.go`, `linux_installer.go`, and `postinstall.go`.

3. **Post-install lifecycle divergence.** After installation, the code optionally boots the VM (`runMacOSVM()`), runs Setup Assistant automation, and chains into the agent provisioning flow. These lifecycle steps would need a completely different orchestration model when driven from an LRO background goroutine vs. a CLI invocation.

4. **Linux installer is separate.** `handleLinuxInstall()` / `linux_installer.go` has the same coupling pattern; supporting both via a unified HTTP API doubles the scope.

The headless path (`guiMode = false`) is closer to extractable (~200 LOC), but it still reads `ipswPath`, `vmDir`, `cpuCount`, `memoryGB`, `diskSizeGB` from globals and calls `resolveOrDownloadIPSW` which reads them transitively.

## v0.2 extraction plan

1. Introduce `type InstallOptions struct` mirroring the CLI flags needed for headless install.
2. Add `func installMacOSHeadless(ctx context.Context, opts InstallOptions, progress func(phase string, pct int)) error` — extracts only the headless path.
3. Wire `POST /v1/vms` LRO goroutine to call `installMacOSHeadless`.
4. Linux support follows the same pattern with `installLinuxHeadless`.
5. GUI-mode install from HTTP is explicitly out of scope (HTTP is a headless interface by nature).

## v0.1 behavior

`POST /v1/vms` returns:

```
HTTP/1.1 202 Accepted
Location: /v1/operations/op_xxxxxxxx
Content-Type: application/json

{
  "operation_id": "op_xxxxxxxx",
  "resource": "vms/",
  "status": "pending",
  "created_at": "..."
}
```

After ~100ms, `GET /v1/operations/op_xxxxxxxx` returns:

```json
{
  "status": "failed",
  "error": {
    "code": "not_implemented",
    "message": "create_vm via HTTP API is deferred to v0.2; use 'cove install' from the CLI"
  }
}
```

This proves the LRO plumbing works end-to-end and gives clients a clear, actionable error message.
