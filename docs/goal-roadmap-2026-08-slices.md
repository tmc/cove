# GOAL: Implement roadmap-apple-api-opportunities-2026-08 — first three slices

Objective (from /goal): implement docs/roadmap-apple-api-opportunities-2026-08.md
via an ultracode workflow. Scope = the roadmap's own §5 "Suggested first three
slices"; the remaining proposals stay as roadmap items.

Status: COMPLETE (code-level). Live-VM empirical checks remain (see gates).
Workflow run: wf_bf205f85-b25 (5 agents, ~509k subagent tokens, ~25 min).

## Checklist

- [x] Slice 1a: PGDisplay reachability + `display` status command —
      `cmd/cove/pgdisplay_status_darwin.go` (bounded ivar BFS from vm/vmView,
      respondsToSelector-gated reads of GuestPresentCount/HostPresentCount/
      name/serial/port/cursor/size/modes), `control_runtime_status.go`
      (`DisplayStatus` JSON, structured `available:false` on miss),
      `control_socket.go` registration, `cove ctl display` verb + help
      (`ctl.go`).
- [x] Slice 1b: disk caching-mode knob — `cmd/cove/disk_caching.go`,
      `-disk-caching auto|cached|uncached` (public
      `VZDiskImageCachingMode`), wired into macOS, Linux, AND Windows
      system-disk paths; unset flag = byte-identical old path.
- [x] Slice 2: windowless framebuffer screenshots —
      `cmd/cove/screenshots_framebuffer_darwin.go` behind
      `COVE_SCREENSHOT_BACKEND=framebuffer`; ivar-walk locate of
      VZFramebuffer (+ detached VZMacGraphicsDisplay constructor fallback),
      TakeScreenshot trigger, IOSurface probe/decode (BGRA→RGBA), one-time
      warn + automatic fallback to the untouched CGWindowList path
      (`screenshots.go` seam). x/vzkit/framebuffer inspected but not used
      (its TakeScreenshot wrapper carries no pixel payload).
- [x] Slice 3a: compressed suspend states — opt-in via
      `COVE_COMPRESSED_SUSPEND=1` or `-save-compress`; availability probe +
      warning in `runtime_lifecycle.go`; private-save error fallback to
      public save in `runtime_private.go`; snapshot path
      (`snapshots.go` saveVMStateSnapshot/saveMachineStateCompressed) with
      partial-file cleanup, timeout treated as fatal (no same-URL retry
      race, no unsafe resume), resume-on-error restored. Encryption
      deliberately not wired (no key-management story).
- [x] Slice 3b: `internal/sckit` stream backend — `stream.go` /
      `stream_darwin.go` (persistent SCStream, last-frame cache, Snapshot/
      Frames/Stop API), 7 TCC-free unit tests + gated live test. Wiring
      into `cmd/cove/screenshots.go`'s ring is a follow-up (prereq work for
      3.2/3.3 done at package level).
- [x] Verify: `go build ./...`, `go vet ./cmd/cove ./internal/sckit`,
      `go test ./internal/sckit` green; `cmd/cove` tests pass except
      pre-existing `TestSubcommandSkipsVMDir/help...` (committed in
      f68db2d9, untouched by this diff). Re-sign after build:
      `codesign -s - -f --entitlements cmd/cove/vz.entitlements ./cove`.
- [x] Adversarial review ran; all 4 confirmed defects fixed
      (suspend hang w/ -save-compress, snapshot timeout write race,
      paused-VM leak on error, windows disk-caching no-op).

## Live-VM gates (external, not code-blockable)

- PGDisplay/VZFramebuffer ivar-walk reachability on macOS 26
  (`cove ctl display` against a running GUI VM).
- Framebuffer screenshot: which completion block fires; imageConversionBlock
  ABI (bound void/void — if the framework consumes a return value this can
  crash; path is opt-in only); IOSurface accessor selector; headless
  detached-display behavior.
- Compressed save+restore round trip and cross-macOS restore compat
  (reason the gate stays default-off).
- SCStream live frame delivery under TCC (`-tags sckit_live` +
  COVE_TEST_SCKIT_GRANT=1 + COVE_TEST_SCKIT_WINDOW_ID).

## Evidence log

- 2026-08-15: baseline `go build ./...` broken by apple-overlay skew
  (`NSEventMask` now uint64); fixed `appkit_compat.go` to
  `appkit.NSEventMaskAny`. Green after fix.
- 2026-08-15: workflow wf_bf205f85-b25 implemented slices (journal:
  session subagents/workflows/wf_bf205f85-b25/journal.jsonl); reviewer
  defects fixed inline post-run; final build/vet/test green.

## Follow-ups (not in this goal)

- Wire internal/sckit Stream into the screenshot ring (roadmap 3.2 proper),
  then `cove record` (3.3).
- VZFramebufferObserver event-driven capture (3.12) — shares the new
  framebuffer plumbing.
- Pre-existing `-save-encrypt` has the same historical no-gate shape;
  now covered by the runtime_private.go fallback but still lacks a
  key-management story.
