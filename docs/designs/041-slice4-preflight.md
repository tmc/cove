# Design 041 Slice 4 Preflight

Captured after `git fetch origin` on 2026-05-10. Branch
`origin/conductor/sckit-slice4` did not exist at this check.

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
