---
title: Release post-tag checklist
status: Draft
date: 2026-05-05
---

# Release post-tag checklist

Use after `v0.2.1` or `v0.3.0` is actually tagged and artifacts have been built.

## Manual follow-ups

- Confirm tag CI is green before publishing GitHub release artifacts.
- Upload the correct release notes file:
  - `RELEASE-NOTES-v0.2.1.md` for `v0.2.1`
  - `RELEASE-NOTES-v0.3.0.md` for `v0.3.0`
- Run `dist/smoke-test.sh` against a fresh VM before calling the release done.
- Keep the Homebrew tap update parked while cove is private.
- Notify the mlx-go QA thread when `v0.2.1` lands.
- Post the Cirrus migration link only after the release artifact is available.
- Record any skipped live-smoke gate in the GitHub release body.

## Privacy gate

Do not push to `tmc/homebrew-tap` and do not announce a public registry or
signed agentkit channel until the repo/public-name gate is explicitly cleared.
