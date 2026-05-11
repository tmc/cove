# Design 041 Slice 4 Preflight

## 2026-05-11 R107 host check

No-ship: this host cannot produce the Slice 2 p50/p95 evidence yet.

Commands run from an isolated worktree at `origin/main` (`b305c86`):

```sh
go build -o cove .
codesign -s - -f --entitlements internal/autosign/vz.entitlements ./cove
./cove doctor sckit-preauth -json
./cove list
COVE_TEST_SCKIT_GRANT=1 go test -v -tags sckit_live ./internal/sckit/ -run TestCaptureWindowLive
./cove doctor sckit-spike -n 30 -threshold 50ms -title-prefix cove
```

Observed state:

- Host: macOS 26.4.1 (`25E253`).
- `sckit-preauth`: SCKit available, Screen Recording not authorized.
- `cove list`: no running GUI VM; `default` is suspended and the other
  local VMs are suspended or stopped.
- Live SCKit test skipped because `COVE_TEST_SCKIT_WINDOW_ID` was not set.
- `sckit-spike` failed before sampling with TCC denial:
  `The user declined TCCs for application, window, display capture`.

Run later on a TCC-granted host with a visible cove GUI VM:

```sh
go build -o cove .
codesign -s - -f --entitlements internal/autosign/vz.entitlements ./cove
./cove doctor sckit-preauth
COVE_TEST_SCKIT_GRANT=1 COVE_TEST_SCKIT_WINDOW_ID=<id> go test -tags sckit_live ./internal/sckit/
./cove doctor sckit-spike -n 30 -threshold 50ms -title-prefix cove
```

Updated after `git fetch origin main conductor/sckit-slice4` on
2026-05-11. Branch `origin/conductor/sckit-slice4` exists at
`017e52d` (`screenshots: retire CGWindowList`), but it is not
ready to land on current `origin/main`.

## 2026-05-11 audit

Branch delta from `origin/main...origin/conductor/sckit-slice4`:

- One slice commit: `017e52d screenshots: retire CGWindowList`.
- Files touched: `README.md`, `control_socket.go`, `doc.go`,
  `docs/README.md`, `docs/architecture/overview.md`,
  `docs/architecture/purego.md`, `docs/designs/README.md`,
  `docs/reference/cli.md`, `internal/sckit/backend.go`,
  `internal/sckit/backend_test.go`, `internal/sckit/doc.go`,
  `private_api_display_test.go`, `screenshots.go`,
  `screenshots_sckit_test.go`.
- Net delta: 69 insertions, 253 deletions.

Merge status against current `origin/main`:

- `git merge --no-commit --no-ff origin/conductor/sckit-slice4`
  conflicts in `docs/designs/README.md` and `screenshots.go`.
- The `screenshots.go` conflict is in the SCKit fallback block that
  current main still keeps for Slice 3 dual-path behavior.
- The `docs/designs/README.md` conflict is a stale roadmap-status row.

Missing ship gate:

- Design 041 still blocks Slice 4 on real SCKit-vs-CGWindow latency
  evidence. `RELEASE-NOTES-v0.6.0.md` and
  `docs/release/v0.6-readiness.md` both still say Slice 2 p50/p95
  numbers are blocked/TBD.
- No in-repo benchmark/result file currently records the required
  p50/p95 comparison for the default flip.

Validate later with a TCC-granted bench host:

```sh
git fetch origin main conductor/sckit-slice4
git worktree add ../vz-macos-sckit-slice4-merge origin/main
cd ../vz-macos-sckit-slice4-merge
git merge --no-commit --no-ff origin/conductor/sckit-slice4
go build -o cove .
codesign -s - -f --entitlements internal/autosign/vz.entitlements ./cove
./cove doctor sckit-preauth
COVE_TEST_SCKIT_GRANT=1 go test -tags sckit_live ./internal/sckit/
./cove doctor sckit-spike -n 30 -threshold 50ms -title-prefix cove
go test ./...
staticcheck -checks=SA1019 ./...
```

Do not land Slice 4 until the benchmark output is recorded in-repo with
p50 and p95 values and the merge conflicts above are resolved. The safe
preparatory cleanup that can land now is this status clarification only.

## Design anchors

- Resolved decision 1: macOS 14+ is the SCKit floor.
- Slice 4 says v0.7 flips capture to SCKit and retires CGWindowList.
- Slice 4 keeps `COVE_CAPTURE_BACKEND=cgwindow` as a one-release warning.
- Migration order says Slice 4 is v0.7, after Slice 3 bake time and perf data.

## CGWindowList references

| Reference | Design 041 action |
|---|---|
| `screenshots.go:159` | Inline SCKit path; remove dual-path comment. |
| `screenshots.go:161` | Remove SCKit-failure fallback to CGWindowList. |
| `screenshots.go:208` | Delete `captureCGWindow`. |
| `screenshots.go:211` | Delete `CGWindowListCreateImage` call; clears SA1019. |
| `screenshots.go:213` | Delete CGWindowList option use. |
| `screenshots.go:220` | Delete nil-image verbose message. |
| `screenshots.go:222` | Delete nil-image error string. |
| `internal/sckit/backend.go:13` | Drop `BackendCGWindow` as active backend. |
| `internal/sckit/backend.go:16` | Remove fallback-to-CGWindowList doc. |
| `screenshots_sckit_test.go:78` | Rewrite fallback test; no bogus CGWindow ID. |
| `screenshots_sckit_test.go:81` | Drop assertion that fallback reached CGWindowList. |
| `doc.go:37` | Replace package-doc architecture line with SCKit wording. |
| `control_socket.go:366` | Extra hit: update timeout comment after CGWindow removal. |
| `private_api_display_test.go:399` | Extra hit: private display note still names CGWindowList. |
| `README.md:253` | Extra hit: repo map still says CGWindowList capture. |
| `CLAUDE.md:181` | Extra hit: old thread-safety claim conflicts with Slice 4. |
| `CLAUDE.md:182` | Extra hit: example call should be removed or made historical. |
| `CLAUDE.md:225` | Extra hit: screenshot section still names CGWindowList. |
| `CLAUDE.md:228` | Extra hit: thread-safety claim should be deleted. |
| `CLAUDE.md:230` | Extra hit: example call should be removed or made historical. |
| `CLAUDE.md:232` | Extra hit: option example should be removed with call. |
| `CLAUDE.md:1032` | Extra hit: repo map still says CGWindowList capture. |
| `docs/README.md:64` | Extra hit: architecture graph still names CGWindowList. |
| `docs/architecture/overview.md:64` | Extra hit: capture architecture still names CGWindowList. |
| `docs/architecture/overview.md:143` | Extra hit: thread-safety note conflicts with Slice 4. |
| `docs/architecture/purego.md:133` | Extra hit: example remains historical or must move. |
| `docs/release/v0.6-readiness.md:17` | Historical v0.6 note; leave unless updating release docs. |
| `docs/release/v0.6-readiness.md:163` | Historical v0.6 note; leave unless updating release docs. |
| `docs/designs/README.md:47` | Extra hit: roadmap blurb should reflect shipped slices. |
| `docs/designs/ROADMAP.md:179` | Historical roadmap; update only if current roadmap is revised. |
| `docs/designs/ROADMAP.md:183` | Historical Slice 1 row; no code blocker. |
| `docs/designs/ROADMAP.md:185` | Slice 3 row should stay historical unless roadmap refreshed. |
| `docs/designs/ROADMAP.md:186` | Slice 4 row should move from spec to landed after merge. |
| `docs/designs/ROADMAP.md:265` | Historical changelog row; no code blocker. |
| `RELEASE-NOTES-v0.6.0.md:7` | Historical release note; leave. |
| `RELEASE-NOTES-v0.6.0.md:9` | Historical release note; leave. |
| `RELEASE-NOTES-v0.6.0.md:50` | Historical release note; leave. |
| `RELEASE-NOTES-v0.6.0.md:58` | Historical release note; leave. |

## Backend selector and env gotchas

- `control_socket.go:100`, `:104`: design says delete `captureBackend`
  selector; call sites in `unattended.go`, `vzscript.go`, `input_host.go`,
  and `gui_control.go` must not lose input-backend behavior.
- `automation_backend.go` and `automation_backend_test.go` still own the
  shared capture/input enum; Slice 4 keeps values for one release.
- `internal/sckit/backend_test.go` and `screenshots_sckit_test.go` exercise
  `COVE_CAPTURE_BACKEND`, per-VM override, default `cgwindow`, and fallback.
  These are the dual-path tests Slice 4 collapses.
- `docs/reference/cli.md:1180` documents `COVE_CAPTURE_BACKEND`; update it
  to the v0.7 warning semantics or remove it when the env var is dropped.
