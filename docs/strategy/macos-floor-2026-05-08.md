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
| `README.md:279` | "macOS 12.0+ (Monterey or later)" | **stale** |

## Decision

**Consistent.** Slice 4 Option A stands.

Every authoritative surface (CI matrix, design 041, v0.6 release
notes) names macOS 14 as the floor. The lone divergence is
`README.md:279`, which still reads `macOS 12.0+`. That line is
documented as a v0.6 tag-cut prerequisite in
`RELEASE-NOTES-v0.6.0.md` §"macOS host floor" — the release notes
explicitly acknowledge that nothing in `go.mod`, the entitlements
file, or the install instructions previously stated a minimum, and
v0.6 fixes that.

The README update is a release-cut task, not a design conflict.
v0.6 has not yet tagged (see MEMORY: "tag NOT yet cut"). When the
tag cuts, README §Requirements must update to `macOS 14.0+
(Sonoma or later)` in the same commit that flips the user-visible
floor. No design 041 spec change is required.

## Follow-up

- [ ] At v0.6 tag cut: update `README.md:279` to `macOS 14.0+
  (Sonoma or later)`. Track on the v0.6 release checklist, not as
  a separate slice.
