# cove v0.6.0 release notes

**Status: in development.** v0.6.0 is not tagged. This file is a scaffold;
slice rows update one at a time as design 041 ships. Do not treat as a
shipped release.

The headline change in v0.6 is operator-visible: cove's minimum macOS
host floor moves from implicit 12.0 to 14.0 (Sonoma), and the screen
capture path migrates from `CGWindowListCreateImage` to ScreenCaptureKit
in four staged slices. Both changes flow from design 041.

## Breaking changes

### macOS host floor: 14.0 (Sonoma)

cove v0.6 requires macOS 14.0 or newer on the host. Previous releases
ran on macOS 12.0+ implicitly; nothing in `go.mod`, the entitlements
file, or the install instructions stated a minimum, and the practical
floor was "whatever Apple Virtualization needs". v0.6 makes the floor
explicit at 14.0 so design 041 Slice 2 can target `SCScreenshotManager`
(macOS 14+) directly instead of carrying a parallel `SCStream`
start-snap-stop fallback for 12.x/13.x indefinitely. See
`docs/designs/041-screencapturekit-migration.md` Open Question 1 for
the rationale.

A 13.x host running v0.6 will see, on first capture:

```
cove ctl screenshot: ScreenCaptureKit unavailable on macOS 13.x;
cove v0.6+ requires macOS 14.0 or newer (design 041)
```

13.x users should stay on the v0.5.x line until they can upgrade.

## ScreenCaptureKit migration

Design 041 stages the SCKit migration in four slices. Each row updates
when the slice lands on `origin/main`. Status values: TBD = not yet
landed.

| Slice | Surface | Status | Commit |
|---|---|---|---|
| 1 | [`cove doctor sckit-preauth`](#how-to-test-the-probe) diagnostic — reports SCKit availability and Screen Recording authorization via [`internal/sckit/`](internal/sckit/). No production callers wired. | Shipped 2026-05-08 | [`8d55d7a`](https://github.com/tmc/cove/commit/8d55d7a) |
| 2 | Parallel `captureVMViewSCKit` behind `-capture-backend=sckit` (or `COVE_CAPTURE_BACKEND=sckit`). Default remains `cgwindow`. | TBD | — |
| 3 | Default flips to `sckit` on macOS 14+. First-GUI-screenshot may produce a Screen Recording TCC prompt; pre-flight via `cove doctor sckit-preauth`. | TBD | — |
| 4 | `CGWindowListCreateImage` removed from `screenshots.go`. `staticcheck` clears SA1019. | TBD | — |

Slice 3 and Slice 4 are intended to ship in the same release so the TCC
prompt appears in release notes only once. Slice 4 must not ship before
Slice 3 has had at least one release of bake time on the default.

The private framebuffer path (`capturePrivateGraphicsDisplay()` in
`screenshots_private_darwin.go`) is unaffected. It does not use
`CGWindowListCreateImage` and remains the headless escape hatch.

## TCC Screen Recording

ScreenCaptureKit always routes through the `kTCCServiceScreenCapture`
TCC service, even when capturing the calling app's own window. v0.6
adds a pre-flight diagnostic so operators can grant Screen Recording
before first capture rather than meeting the prompt mid-install.

```bash
cove doctor sckit-preauth
```

The command reports:

- whether SCKit is available on the host;
- whether the cove binary is authorized for Screen Recording;
- the next concrete step if either check fails.

Operators automating fresh-host setup should run
`cove doctor sckit-preauth` once per host before the first
`cove run -gui` or `cove ctl screenshot`. Revoking Screen Recording in
System Settings produces a clean error from `cove ctl screenshot` once
Slice 3 lands; cove does not silently fall back to the legacy path.

See [How to test the probe](#how-to-test-the-probe) below for exact
commands and expected output.

### How to test the probe

The probe is read-only: it never triggers a TCC prompt. Exit code 0
when SCKit is available and Screen Recording is authorized; exit 1
otherwise.

```
$ cove doctor sckit-preauth
=== cove ScreenCaptureKit pre-flight ===

  macOS version              : 14.5
  SCKit available            : yes
  screen recording authorized: yes

$ echo $?
0
```

Denied state (also exit 1):

```
$ cove doctor sckit-preauth
=== cove ScreenCaptureKit pre-flight ===

  macOS version              : 14.5
  SCKit available            : yes
  screen recording authorized: no

error: screen recording not authorized; grant in System Settings > Privacy & Security > Screen Recording
$ echo $?
1
```

Add `-json` for machine-readable output (same exit-code semantics):

```
$ cove doctor sckit-preauth -json
{
  "SCKitAvailable": true,
  "ScreenRecordingAuthorized": true,
  "MacOSVersion": "14.5"
}
```

## Pre-tag gates

- All four design 041 slices landed on `origin/main`.
- `staticcheck ./...` reports zero SA1019 hits in `screenshots.go`.
- `cove doctor sckit-preauth` reports authorized state on a fresh
  macOS 14 and macOS 15 host.
- Headless install path (no GUI window) still routes through
  `capturePrivateGraphicsDisplay()` cleanly.

## Test gates

- `go build ./...` green.
- `go test ./...` green, including the new `sckit` package tests.

## Tagging

This file is a scaffold drafted before any v0.6 slice ships. The git
tag `v0.6.0` is **not yet cut**; tag-cut is user-gated and will follow
the same readiness sequence used for v0.5.
