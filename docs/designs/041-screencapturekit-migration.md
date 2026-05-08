# Design 041: ScreenCaptureKit Migration

Status: Accepted (2026-05-08; horizon v0.6)
Author: Travis Cline
Date: 2026-05-07

## Resolved decisions (2026-05-08)

1. **macOS floor**: 14.0+ for the SCKit migration. v0.6 bumps cove's
   minimum to macOS 14 Sonoma. Slice 2 uses `SCScreenshotManager`. Older
   hosts get a clear error at first capture with a bump suggestion.
2. **TCC prompt timing**: pre-flight via `cove doctor sckit-preauth`
   (Slice 1). Investigate `SCContentSharingPicker` on 14.4+ as a
   lower-friction permission UX during Slice 2.
3. **Performance under contention**: defer empirical decision to Slice
   2 A/B. If SCKit single-frame latency exceeds 50 ms median, switch to
   long-lived `SCStream` + ring buffer.
4. **`_framebufferView` interaction**: dual-path with fallback. Slice 2
   ships an explicit test that captures during a known install flow,
   compares SCKit output against `capturePrivateGraphicsDisplay()`,
   keeps the private path as fallback when SCKit can't see the
   framebuffer ivar.

## Problem

`screenshots.go:141` calls `coregraphics.CGWindowListCreateImage`,
the canonical Quartz Window Services screen-capture API. As of
macOS 14 (Sonoma) Apple ships a `staticcheck` SA1019:

```
screenshots.go:141:13: coregraphics.CGWindowListCreateImage is
    deprecated: Please use ScreenCaptureKit instead.
```

Apple periodically tightens deprecation enforcement. CGWindowList
APIs continue to function on macOS 14/15, but Apple has signalled the
direction (release notes, WWDC 2024 sessions, and the deprecation
attribute itself): future macOS releases may stop honoring the legacy
path, ship reduced fidelity, or refuse the call without a privacy
prompt that does not exist for this API today.

Cove uses screen capture in three load-bearing places:

- `captureVMView()` (`screenshots.go`) — full-window capture via
  `CGWindowListCreateImage`. Used when the GUI window is on-screen.
- `capturePrivateGraphicsDisplay()` (`screenshots_private_darwin.go`)
  — a separate code path that uses
  `cacheDisplayInRectToBitmapImageRep` against the private
  `_framebufferView` ivar inside `VZVirtualMachineView`. Used when
  the GUI is headless or the framebuffer-backend is selected.
- The `Capture` bridge (`internal/controlserver/capture.go`) — owns
  the diff cache and lazy OCR service across captures, but does not
  itself call CG APIs. The bridge's `RememberBounds`/`Diff` methods
  are SCKit-agnostic.

Only the first path (`captureVMView`) trips SA1019. The second path
already avoids `CGWindowListCreateImage` and is unaffected.

## Why this is non-trivial

Three properties make this a design exercise rather than a
mechanical edit.

### Threading contract changes

CLAUDE.md documents a concrete invariant:

> `CGWindowListCreateImage` is thread-safe — no main queue dispatch
> needed.

`captureVMView()` exploits this: the function runs on whatever
goroutine called it (typically a control-socket handler) and returns
synchronously. Callers (`takeScreenshotWithOptions`,
`captureDisplayImage`, `unattended.attemptLogin`,
`provision_automation.waitForVMScreenReady`,
`runtime_actions`, `boot_commands`, `control_socket_ocr`) all
assume synchronous return.

ScreenCaptureKit (`SCStream`, `SCStreamOutput` delegate) is
**asynchronous**: frames arrive as `CMSampleBuffer` callbacks from a
private serial queue owned by the kit. A synchronous "snap one frame
now" wrapper has to either:

1. start a stream, wait for the first frame, stop the stream, return — adds
   per-call setup/teardown latency (Apple's own samples show 100 ms +
   per cold start);
2. keep a long-lived stream running, and return the most-recent frame from a
   ring buffer — adds memory pressure and lifetime questions; or
3. use `SCScreenshotManager.captureImageWithFilter:configuration:completionHandler:`
   (macOS 14+) which is a one-shot callback API that more closely matches the
   existing synchronous shape.

`SCScreenshotManager` is the tightest fit. It is async-callback
shaped on the API surface but does not require running a stream. We
still need a goroutine-friendly shim: a `chan struct{ img, err }`
fed from the completion handler, plus a context-honoring receive.

### TCC permission surface

`CGWindowListCreateImage` does not currently prompt for permission
when the caller is the application that owns the window being
captured. SCKit always routes through the **Screen Recording** TCC
service, which:

- prompts the user on first use;
- shows a separate System Settings toggle;
- can be revoked, in which case `SCScreenshotManager` returns
  `SCStreamError(unauthorized)` (`-3801` family).

For cove that means: even though our screen-capture target is
**our own VM window**, Apple's TCC enforcement does not currently
distinguish self-capture from cross-app capture in SCKit. The first
SCKit call after migration will produce a TCC prompt unless the user
has already granted Screen Recording for the cove binary.

This composes badly with cove's existing TCC posture (memory:
`project_tcc_appleevents_slice.md`, `feedback_minimize_sudo.md`).
A single un-prompted-for prompt at first GUI screenshot is
operator-visible behavior change.

### macOS version floor

`SCStream`/`SCContentFilter`/`SCShareableContent` shipped in macOS
12.3. `SCScreenshotManager` shipped in macOS 14.0.
`SCContentSharingPicker` (which lets us delegate the permission UX
to a system-rendered picker rather than a hard TCC prompt) shipped
in macOS 14.4 and was extended in macOS 15.

Cove's current build target floor is open. `go.mod`, the entitlements
file, and the install instructions do not state a macOS minimum; the
implicit floor is "whatever Apple Virtualization needs", which is
macOS 12.0+ for guests and macOS 13.0+ for some features. This is an
**open question** for this design (see Open Questions).

If cove must continue to support macOS 12.x, we cannot rely on
`SCScreenshotManager` and must run a transient `SCStream`. If we can
require macOS 14+, the migration is materially simpler.

## Goals

- Replace `CGWindowListCreateImage` with the closest-shape SCKit
  surface, preserving the synchronous-return contract for callers.
- Honor TCC `kTCCServiceScreenCapture` cleanly: detect denial,
  surface a clear error, do not silently fall back.
- Keep `capturePrivateGraphicsDisplay()` as a fallback that does NOT
  require Screen Recording (private framebuffer path, no
  CGWindowList call).
- Stage the change so a single slice can be reverted if SCKit breaks
  in a future macOS update.

## Non-goals

- Rewriting `Capture` bridge or OCR/diff path. Those are
  format-agnostic and stay where they are.
- Migrating the private framebuffer path. It uses NSView's
  `cacheDisplayInRectToBitmapImageRep`, which is not flagged by
  SA1019.
- Recording video, multi-display capture, or any new capture
  features. Strictly an API replacement.
- Dropping CGWindowList immediately. The deprecation gives us time;
  we can run both paths under a flag for a release before removing
  the old one.

## SCKit shape we will adopt

The smallest viable surface (assuming we can target macOS 14+; see
Open Question 1):

```objc
SCContentFilter *filter = [[SCContentFilter alloc]
    initWithDesktopIndependentWindow:vmWindow];

SCStreamConfiguration *config = [SCStreamConfiguration new];
config.width  = vmWindow.frame.size.width  * scale;
config.height = vmWindow.frame.size.height * scale;
config.pixelFormat = kCVPixelFormatType_32BGRA;

[SCScreenshotManager
    captureImageWithFilter:filter
             configuration:config
         completionHandler:^(CGImageRef cgImage, NSError *error) {
    // Forward to Go via a registered callback or chan.
}];
```

In Go (sketch, lifted into purego style consistent with the rest of
`apple/x/vzkit`):

```go
func (s *ControlServer) captureVMViewSCKit(ctx context.Context) (image.Image, string) {
    state := s.captureState()
    if state.window.ID == 0 {
        return nil, "window not set"
    }
    ch := make(chan captureResult, 1)
    sckit.CaptureWindow(state.window.ID, sckit.Config{
        Scale:       1.0,
        PixelFormat: sckit.BGRA32,
    }, func(cg coregraphics.CGImageRef, err error) {
        ch <- captureResult{cg: cg, err: err}
    })
    select {
    case r := <-ch:
        if r.err != nil {
            return nil, r.err.Error()
        }
        defer coregraphics.CGImageRelease(r.cg)
        img, err := capture.GoImageFromCGImage(r.cg, 0)
        if err != nil {
            return nil, err.Error()
        }
        return img, ""
    case <-ctx.Done():
        return nil, ctx.Err().Error()
    }
}
```

The `sckit` package goes in `github.com/tmc/apple/x/vzkit/sckit`
alongside the existing `capture` package. The Go shim owns the
`SCStream`/`SCScreenshotManager` lifetimes and exports a minimal
`CaptureWindow(windowID, config, callback)` surface.

## Slice plan

### Slice 1 — Probe and TCC detection

Files: new `sckit/sckit_darwin.go` + tests; touch
`screenshots.go` only to add a `captureBackendSCKit` constant.

- Add a probe that reports `(SCKitAvailable bool, ScreenRecordingAuthorized bool)`.
- Surface state via `cove doctor` (extend the existing
  `tcc-preauth` subcommand or add a sibling).
- No production callers wired yet. Pure diagnostic.

Acceptance: `cove doctor sckit-preauth` prints availability +
authorization on macOS 12 / 13 / 14 / 15 hosts; shipping this on
its own is safe because it does not change any capture behavior.

### Slice 2 — Parallel SCKit implementation behind a flag

Files: `sckit/window.go` (new),
`sckit/window_test.go` (new), `screenshots.go` (add
`captureVMViewSCKit` parallel method), `automation_backend.go`
(add an opt-in backend value).

- Implement the synchronous-shaped wrapper (channel + completion
  handler).
- New CLI/env flag: `-capture-backend=cgwindow|sckit` or
  `COVE_CAPTURE_BACKEND=...`. Default: `cgwindow` (existing).
- Wire `captureVMView()` to dispatch on the flag.

Acceptance: with `-capture-backend=sckit`, `cove ctl screenshot`
returns a frame on macOS 14+ that is byte-comparable in size and
visually equivalent to the cgwindow path. CGWindow path remains the
default and unchanged.

### Slice 3 — Flip the default

Files: `screenshots.go`, `automation_backend.go`,
release notes.

- Change the default to `sckit` on macOS 14+; keep `cgwindow`
  default on macOS 12-13 (if we still support them).
- Document in `RELEASE-NOTES-v0.<N>.md` that first-GUI-screenshot
  may now produce a Screen Recording TCC prompt; add a `cove
  doctor sckit-preauth` mention.

Acceptance: a fresh-install user on a clean macOS 15 sees the TCC
prompt once; subsequent screenshots succeed without prompt; revoking
permission in System Settings produces a clean error response from
`cove ctl screenshot`.

### Slice 4 — Drop CGWindowListCreateImage

Files: `screenshots.go`, `screenshots_private_darwin.go` (untouched),
`automation_backend.go` (drop the `cgwindow` value), CLAUDE.md
(remove the thread-safety claim about CGWindowListCreateImage).

- Remove the `captureVMView()` body that calls
  CGWindowListCreateImage; keep the function but route everything
  through SCKit.
- Drop the `cgwindow` backend constant.

Acceptance: `staticcheck` is clean of SA1019 in `screenshots.go`. No
caller signature changes.

The slice plan deliberately ships Slice 1 alone first as a TCC
diagnostic shipment. Slice 2 lands the parallel implementation
non-default. Slice 3 + 4 should be the same release together (so the
release notes mention the TCC prompt only once), but Slice 4 should
not run before Slice 3 has at least one release of bake time on the
default.

## Fallback contract

If SCKit fails (host pre-12.3, TCC denied, transient framework
error) the call must:

1. Return a structured error string consistent with the existing
   error shape (callers do `string` switches on
   `"window not set"`, `"CGWindowListCreateImage returned nil"`,
   etc.; SCKit errors should be human-readable but not match those
   exact strings).
2. Not silently fall back to `capturePrivateGraphicsDisplay()`. The
   existing `captureDisplayImage()` already chooses between
   framebuffer and window paths via `captureBackend()`; adding a
   silent SCKit→cgwindow→framebuffer cascade hides which API is
   actually working.

The framebuffer path remains the explicit headless escape hatch and
is unaffected by this design.

## Open questions

### 1. macOS version floor

Cove's minimum macOS is implicitly 12.0 (Apple Virtualization), but
no doc spells it out. **Required user input** before Slice 3 ships:

- If 14.0+: use `SCScreenshotManager`. Slice 2 is small.
- If 13.0+: use `SCStream` start-snap-stop. Slice 2 is bigger and
  has more lifetime concerns.
- If 12.x: keep CGWindowList alongside SCKit indefinitely (a
  parallel implementation, not a migration).

Recommendation: bump the floor to macOS 14.0 for the v0.6 release
that ships Slice 3, in line with cove's "Apple Silicon, recent
macOS" positioning. But this is a user/product call, not a
designer's call.

### 2. TCC prompt timing

When does the user first see the Screen Recording prompt?

- If we trigger SCKit during `cove run -gui`, the prompt may
  appear before the VM window is fully drawn (jarring).
- If we trigger only on first `cove ctl screenshot`, we delay the
  prompt to a CLI-driven moment, but `unattended.go` and
  `provision_automation.go` use `captureDisplayImage()` during
  install — the prompt could fire mid-install.

Recommendation: ship Slice 1 (`cove doctor sckit-preauth`) first
and document a pre-flight step. Investigate `SCContentSharingPicker`
on macOS 14.4+ as a lower-friction permission UX.

### 3. Performance under contention

CGWindowListCreateImage returns a `CGImageRef` directly.
SCScreenshotManager produces a `CMSampleBuffer` (or `CGImageRef`
depending on which API surface we use). The `CMSampleBuffer` path
costs a CVPixelBuffer extraction step; the `CGImageRef` path
matches our current shape but may be slower under guest VM frame
contention.

Recommendation: measure in Slice 2's smoke (parallel implementation
allows direct A/B). If SCKit single-frame latency exceeds 50 ms
median on M4-class hardware, revisit by switching to a long-lived
`SCStream` with a recent-frame ring buffer.

### 4. `_framebufferView` ivar and SCKit interaction

`capturePrivateGraphicsDisplay()` reaches into the VZVirtualMachineView's
`_framebufferView` ivar. SCKit on macOS 15+ may or may not see
that view depending on AppKit's view-tree exposure rules. Worth
verifying that Slice 2's SCKit path against the **outer** VM
window still captures frames during install/setup, when the
private framebuffer is the source of truth.

Recommendation: explicit test in Slice 2 — capture during a known
install flow, compare against the private path output, document
divergence if any.

## Risks

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| Apple removes CGWindowList support in macOS 16 | medium | high if we have not migrated; low if Slice 3 has shipped | sequence Slice 3 within v0.6 |
| TCC prompt regresses operator UX | medium | medium | Slice 1 ships diagnostic first, release notes call it out |
| SCKit single-frame latency > CGWindowList | low-medium | medium | A/B in Slice 2 smoke; fall back to streaming SCStream + ring buffer if needed |
| `_framebufferView` invisible to SCKit | low | high during install | explicit Slice 2 test against install flow |
| Drop macOS 12.x without user OK | low | high | this design flags it as Open Question 1 — do not ship Slice 3 until resolved |

## Validation gates

Before Slice 4 ships:

- `staticcheck ./...` reports zero SA1019 hits in `screenshots.go`.
- `cove doctor sckit-preauth` (Slice 1) reports authorized state.
- `cove ctl screenshot` returns a non-empty image on macOS 14, 15.
- The TCC prompt appears at most once per cove install.
- Headless install path (no GUI window) still falls back cleanly to
  `capturePrivateGraphicsDisplay()`.
- Release notes for the version that contains Slice 3 explicitly
  mention the new Screen Recording TCC prompt.

## References

- `screenshots.go` (`captureVMView`)
- `screenshots_private_darwin.go` (`capturePrivateGraphicsDisplay`)
- `internal/controlserver/capture.go` (Capture bridge)
- `automation_backend.go` (backend constants)
- CLAUDE.md "Thread-safe screenshot capture" section
- `staticcheck` SA1019 hit at `screenshots.go:141:13`
- Memory: `project_tcc_appleevents_slice.md`,
  `feedback_minimize_sudo.md`
- Apple developer docs (consult before Slice 1):
  `SCStream`, `SCStreamConfiguration`, `SCContentFilter`,
  `SCShareableContent`, `SCScreenshotManager`,
  `SCContentSharingPicker`.

## Slice 3 — production wiring spec

Slice 1 shipped at `8d55d7a` (`cove doctor sckit-preauth`).
Slice 2 spike shipped at `d0877b8` (`internal/sckit.CaptureSpike`,
single-frame `SCScreenshotManager` against an `SCWindow` filter,
perf A/B blocked on Screen Recording grant in CI).

Slice 3 wires the spike into the live capture path behind a
feature flag, defaults off, with deterministic fallback to the
existing `CGWindowListCreateImage` path. No behavior change for
operators who do not opt in. Goal is to ship the production code
path on `main` so that internal dogfooders and the v0.6 release-
notes audience can flip a flag and exercise SCKit end to end.

### 1. Feature flag

Name: `COVE_CAPTURE_BACKEND`. Values: `cgwindow` (default),
`sckit`, `auto`.

- `cgwindow`: current behavior; `internal/sckit` is not touched.
- `sckit`: try SCKit first; on init or capture error, fall back
  to CGWindowList and emit one `slog.Warn` per process lifetime
  ("sckit-fallback").
- `auto`: equivalent to `cgwindow` in v0.6; reserved for Slice 4
  flip to `sckit-first`. Documented now so the matrix is stable.

Per-VM override: `~/.vz/vms/<name>/capture-backend` (one line,
same three values). Per-VM file wins over env var. Unreadable or
unknown value falls through to env var, then default. Read site:
new helper `captureBackend()` in `screenshots.go` (5–8 LOC),
called once per `captureVMView()` invocation. No global init —
the flag is honored on every capture so an operator can flip it
without restarting the VM.

### 2. Dual-path skeleton

The decision lives in `screenshots.go` next to the existing
`captureVMView()`. `internal/controlserver/capture.go` does not
change — it still consumes `image.Image`. The new code lives
behind a build tag (`darwin && !ios`) so non-darwin and stub
builds keep compiling.

```go
// screenshots.go (pseudocode, ~25 LOC over current captureVMView)
func (s *ControlServer) captureVMView() (image.Image, string) {
    switch captureBackend(s.vmName) {
    case "sckit":
        if img, err := s.captureSCKit(); err == nil {
            return img, ""
        } else {
            warnSCKitFallbackOnce(err) // slog.Warn, sync.Once
        }
    }
    return s.captureCGWindow() // renamed body of current captureVMView
}
```

`captureSCKit` is a thin adapter on `internal/sckit.CaptureSpike`
(rename to `CaptureWindow` in Slice 3 — Slice 2's "spike" naming
is no longer accurate). It takes the same `windowNum` the
CGWindowList path resolves and returns `(image.Image, error)`.

### 3. Fallback policy

SCKit init failure (no shareable content, TCC denied, window not
visible to the current process, or any non-nil error from
`CaptureWindow`) **must not** fail the capture call. The path is:

1. Log a `slog.Warn` with `cause=...` (TCC, window-missing,
   timeout, other). At most once per process via `sync.Once`
   keyed on the cause string, to avoid log floods during install.
2. Fall through to `captureCGWindow()`. Operator sees no
   user-visible degradation beyond the SA1019 deprecation that
   already exists.
3. Do **not** auto-disable the flag. The fallback is per-call so
   that a transient TCC reset (operator just granted Screen
   Recording) recovers on the next capture without a restart.

Headless install (no GUI window) keeps the existing branch in
`screenshots.go` that prefers `capturePrivateGraphicsDisplay()`
— SCKit is only consulted in the GUI window branch. Document
this in the Slice 3 commit message.

### 4. Migration order

| Phase | Default | What ships | Risk |
|---|---|---|---|
| Slice 1 (shipped `8d55d7a`) | n/a | `cove doctor sckit-preauth` diagnostic | none |
| Slice 2 (shipped `d0877b8`) | n/a | `internal/sckit.CaptureSpike`, A/B harness | none, off the hot path |
| Slice 3 (shipped `55257f2`) | `cgwindow` | dual-path, opt-in via env/per-VM, fallback policy | low — default unchanged |
| Slice 4 (v0.6 GA candidate) | `sckit` (`auto` resolves to sckit) | flip default; doctor command becomes recommended pre-flight | medium — TCC prompt now in default flow |
| Post-v0.6 cleanup | `sckit` only | drop CGWindowList branch, remove SA1019 hit, retire `COVE_CAPTURE_BACKEND` (or freeze at `sckit`) | low if Slice 4 has soaked one release |

Slice 4 ships only if Slice 3 dogfood reports zero unrecovered
fallbacks across at least one full release cycle on operator
hardware. Slice 4 spec is out of scope for this document.

### 5. Test gate

Tests split into three buckets:

| Bucket | Tests | Skip rule |
|---|---|---|
| Unit-pure | `captureBackend()` resolution (env + per-VM file precedence, unknown values), fallback log de-dup `sync.Once` semantics | always run |
| Fixture-only | `captureSCKit` adapter wiring against a fake `screencapturekit` interface (table-driven errors → fallback path) | always run; uses interface seam, no TCC |
| Live SCKit | end-to-end capture against a real cove window in `_test.go` files behind `//go:build darwin && sckit_live`, gated on `COVE_TEST_SCKIT_GRANT=1` | skip when env unset; document in `internal/sckit/doc.go` |

The live bucket replaces Slice 2's manual A/B in CI. CI does not
set `COVE_TEST_SCKIT_GRANT`; release engineer runs it once per
release on a TCC-granted host. Slice 3 commit must include at
least the unit-pure and fixture-only tests; live tests can land
in a follow-up commit on the same branch.

No new fixtures required for the unit-pure bucket. Fixture-only
bucket needs a single fake implementing the 3 SCKit calls
`CaptureSpike` makes (`GetShareableContentExcludingDesktopWindowsOnScreenWindowsOnly`,
`NewContentFilterWithDesktopIndependentWindow`,
`CaptureImageWithFilterConfiguration`). Define the seam as an
interface in `internal/sckit` so Slice 4 can swap to streaming
without rewriting tests.

### 6. macOS 14 floor

Confirmed and unchanged from §"Resolved decisions" item 1.
SCKit availability is gated at **build time** by `go:build
darwin && !ios` on `internal/sckit/sckit_darwin.go` plus
`internal/sckit/spike_darwin.go`. There is no runtime
`if #available(macOS 14.0)` check — the binary's macOS
deployment target is bumped to 14 in v0.6 (separate task,
tracked in RELEASE-NOTES-v0.6.0.md). Operators on macOS 12 or
13 cannot install v0.6 in the first place, so the SCKit code
cannot execute on an unsupported host.

### 7. LOC budget

| File | New LOC | Notes |
|---|---|---|
| `screenshots.go` | ~30 | `captureBackend()`, dual-path switch, `warnSCKitFallbackOnce`, rename `captureVMView` body to `captureCGWindow` |
| `internal/sckit/sckit_darwin.go` | ~20 | `CaptureWindow` (rename + minor cleanup of `CaptureSpike`), interface seam for tests |
| `internal/sckit/sckit_other.go` | ~10 | non-darwin stub matching new exported surface |
| `internal/sckit/sckit_test.go` | ~80 | fixture-only fake + table-driven fallback tests, `captureBackend()` precedence tests |
| `screenshots_test.go` (new or extended) | ~40 | dual-path resolution tests using fake seam |
| `internal/sckit/doc.go` | ~10 | document live test build tag + `COVE_TEST_SCKIT_GRANT` |
| `RELEASE-NOTES-v0.6.0.md` | ~10 | "opt-in via `COVE_CAPTURE_BACKEND=sckit`" stanza |

Total: ~200 LOC. Fits within the 250 LOC cap with ~50 LOC of
headroom. If live tests land in the same commit, expect another
~60 LOC; that pushes the total to ~260 and should split into a
second commit (`internal/sckit: live screenshot test`).

### 8. Open question

1. Should `auto` in v0.6 do anything at all, or is it pure
   reserved-future-value? Current spec says "treat as
   `cgwindow`". Alternative: `auto` resolves to `sckit` only
   when `cove doctor sckit-preauth` last ran successfully
   (cached marker file). Adds ~20 LOC + a doctor cache file.
   Defer to conductor.
