# cove v0.6.0 release notes

**Status: in development.** v0.6.0 is not tagged. This file is a scaffold;
slice rows update one at a time as design 041 ships. Do not treat as a
shipped release.

The headline change in v0.6 is operator-visible: cove's minimum macOS
host floor moves from implicit 12.0 to 14.0 (Sonoma), and the screen
capture path begins migrating from `CGWindowListCreateImage` to
ScreenCaptureKit in four staged slices. Slices 1-3 land in v0.6;
Slice 4 (default flip + CGWindowList retire) is deferred to v0.7
pending Slice 2 perf data. Both the floor bump and the SCKit work
flow from design 041. v0.6 also closes the last pure-engineering
Cirrus migration blocker by routing `ENCRYPTED[…]` secrets through
`cove shell --secret-env` and the `cove-action` `secrets:` input
without leaking them into run logs.

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
landed; spec = design landed but no implementation yet.

| Slice | Surface | Status | Commit |
|---|---|---|---|
| 1 | [`cove doctor sckit-preauth`](#how-to-test-the-probe) diagnostic — reports SCKit availability and Screen Recording authorization via [`internal/sckit/`](internal/sckit/). No production callers wired. | Shipped 2026-05-08 | [`8d55d7a`](https://github.com/tmc/cove/commit/8d55d7a) |
| 2 | Parallel SCKit spike (`internal/sckit.CaptureSpike`) and `cove doctor sckit-spike` A/B harness. Off the hot path; p50/p95 numbers blocked on Screen Recording TCC grant on the bench host. | Shipped 2026-05-08 (perf TBD) | [`d0877b8`](https://github.com/tmc/cove/commit/d0877b8) |
| 3 | Dual-path capture via `COVE_CAPTURE_BACKEND=sckit` (or per-VM `<vmDir>/capture-backend` file). Default remains `cgwindow`; SCKit failures fall through silently with one `slog.Warn` per cause. | Shipped 2026-05-08 | [`55257f2`](https://github.com/tmc/cove/commit/55257f2) |
| 4 | Default flip to `sckit` and removal of `CGWindowListCreateImage` (`staticcheck` clears SA1019). Slice 4 spec landed; impl deferred to v0.7 pending Slice 2 perf data — see design 041 §"Slice 4: default flip + CGWindowList retire" §6. | Spec landed 2026-05-08; impl deferred to v0.7 | [`318d801`](https://github.com/tmc/cove/commit/318d801) |

Slice 4 must not ship before Slice 3 has at least one release of bake
time on the default and Slice 2's p50/p95 latency numbers exist. Both
preconditions are why Slice 4 is held to v0.7.

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

Operators opting into SCKit (`COVE_CAPTURE_BACKEND=sckit` or per-VM
`<vmDir>/capture-backend`) should run `cove doctor sckit-preauth` once
per host before the first `cove run -gui` or `cove ctl screenshot` on
that backend. With the v0.6 default (`cgwindow`), no TCC prompt fires
and the probe is informational. Revoking Screen Recording in System
Settings while opted into `sckit` causes the dual-path code to log
one `slog.Warn` and fall back to `cgwindow` for the remainder of the
process.

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

## Cirrus secrets → guest env

Cirrus CI shuts down 2026-06-01. The migration audit
([cirrus-migration-readiness-2026-05-08.md](docs/strategy/cirrus-migration-readiness-2026-05-08.md))
identified one purely-engineering blocker: lifting Cirrus
`ENCRYPTED[…]` secrets into the guest environment without leaking them
into workflow logs. v0.6 closes that blocker.

| Slice | Surface | Status | Commit |
|---|---|---|---|
| 1 | `cove shell --env NAME=VALUE` and `--secret-env NAME=env://VAR\|file:///path` flags; metrics redactor scrubs resolved secret values from run logs. | Shipped 2026-05-08 | [`fe99629`](https://github.com/tmc/cove/commit/fe99629) |
| 2 | `cove-action` composite parses a multi-line `secrets:` input and forwards each entry as a `--secret-env` flag to `cove shell`. Same redaction guarantees as Slice 1. | Shipped 2026-05-08 | [`c9df361`](https://github.com/tmc/cove/commit/c9df361) |

Full design and host-side rationale: [docs/strategy/cirrus-secrets-fix-2026-05-08.md](docs/strategy/cirrus-secrets-fix-2026-05-08.md).

### How to use `--secret-env`

```bash
# env-backed (host env var must be set)
cove shell vm-name --secret-env API_TOKEN=env://CIRRUS_API_TOKEN -- ./run.sh

# file-backed (mode-0600 path on host)
cove shell vm-name --secret-env DEPLOY_KEY=file:///run/secrets/deploy.key -- ./deploy.sh
```

Plain `--env NAME=VALUE` forwards non-secret values without redaction.
Resolved secret values are redacted from `cove runs` logs by the
metrics masker; no in-band masking is performed on stdout/stderr while
the command is attached.

In a GitHub Actions workflow under `cove-action`:

```yaml
- uses: ./.github/actions/cove-action
  with:
    vzscripts: build,test
    secrets: |
      API_TOKEN=env://CIRRUS_API_TOKEN
      DEPLOY_KEY=file:///run/secrets/deploy.key
```

## Pre-tag gates

- Design 041 Slices 1-3 landed on `origin/main`. Slice 4 deferred to v0.7.
- `cove doctor sckit-preauth` reports authorized state on a fresh
  macOS 14 and macOS 15 host.
- Headless install path (no GUI window) still routes through
  `capturePrivateGraphicsDisplay()` cleanly.
- Cirrus secrets Slices 1-2 shipped; metrics redactor smoke-tested
  against multi-line and large-blob secret values.

## Test gates

- `go build ./...` green.
- `go test ./...` green, including the new `sckit` package tests.

## Tagging

This file is a scaffold drafted before any v0.6 slice ships. The git
tag `v0.6.0` is **not yet cut**; tag-cut is user-gated and will follow
the same readiness sequence used for v0.5.
