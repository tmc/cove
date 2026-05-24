# macOS host floor audit (2026-05-08)

Slice 4 of design 041 chose Option A: the v0.6+ binary always uses
SCKit, no runtime version check. Option A is sound only if cove no
longer ships for macOS 13. This audit confirms the floor.

Anchor: design 041 at 318d801, repo HEAD 94f839c.

## Surfaces

| Surface | Stated floor | Aligned? |
|---|---|---|
| `go.mod` | `go 1.25.5` (no OS min) | n/a |
| `.github/workflows/pr.yml` | `runs-on: macos-14` | yes |
| `.github/workflows/release.yml` | `runs-on: macos-14` | yes |
| `internal/sckit/sckit_darwin.go` | `//go:build darwin` (build-tag gate is sufficient per design 041 §6 — SCKit symbols link only on hosts that have them) | yes |
| `RELEASE-NOTES-v0.6.0.md` §"macOS host floor" | macOS 14.0 (Sonoma) | yes |
| `docs/designs/041-screencapturekit-migration.md` §1, §6, Slice 4 §1 | macOS 14.0 | yes |
| DMG / `.entitlements` / Info.plist | no `LSMinimumSystemVersion` set anywhere | n/a |
| `README.md:279` | "macOS 14.0+ (Sonoma or later)" | yes |

## Decision

**Consistent.** Slice 4 Option A stands.

Every authoritative surface (CI matrix, design 041, v0.6 release
notes, README, and install docs) now names macOS 14 as the floor.
The earlier README divergence has been corrected. No design 041 spec
change is required.

## Follow-up

- [x] At v0.6 tag cut: update README §Requirements to `macOS 14.0+
  (Sonoma or later)`.
