---
title: cove R50 release-readiness audit (v0.2.1 / v0.3 / v0.4 / v0.5)
status: Audit
date: 2026-05-07
---

# R50 release-readiness audit

This audit consolidates the tag-cut state for v0.2.1, v0.3, v0.4, and the v0.5
milestone. Every "ready" or "ancestor" claim is backed by
`git merge-base --is-ancestor <hash> origin/main` against `origin/main`
`8bd7a65`. No tags are cut by this document; tagging is user-gated by issues
`#224`, `#225`, and `#294`.

Prior audits remain authoritative for their tag-specific evidence:

- `docs/release/v0.2.1-readiness-final.md` (R41, 2026-05-05).
- `docs/release/v0.3-readiness-final.md` (R41, 2026-05-05).
- `docs/release/v0.4.0-readiness.md` (R41-R42, 2026-05-05).
- `docs/release/tag-cut-runbook.md`.
- `docs/release/release-checklist.md`.

This audit folds those findings forward and adds a v0.5 scope decision and a
24-day Cirrus-runway prioritization (Cirrus CI shutdown 2026-06-01).

## Summary verdict

| Tag    | Ancestor anchor | RELEASE-NOTES        | Mechanical gates | Operator gates                                   | Verdict                  |
|--------|-----------------|----------------------|------------------|--------------------------------------------------|--------------------------|
| v0.2.1 | `ca4d824`       | `RELEASE-NOTES-v0.2.1.md` (285 lines) | PASS at R41    | Version-order honesty (FAIL at current main); live VM smoke (BLOCKED) | Operator-decision gate; not mechanically blocked |
| v0.3.0 | `ca4d824`       | `RELEASE-NOTES-v0.3.0.md` (186 lines) | PASS at R41    | Live local-base build smoke (BLOCKED); developer-id (NOT RUN) | Mechanically ready; live-smoke gate open |
| v0.4.0 | `14cabac`       | `RELEASE-NOTES-v0.4.0.md` (126 lines) | PASS at R41    | `#224`, `#225` user approvals               | Mechanically ready; awaits user approval |
| v0.5   | `8bd7a65`       | not yet drafted (R51-RELEASE-PREP owns) | not gated yet | RELEASE-NOTES-v0.5 + ROADMAP closeout pending  | Code-complete on facade; release-prep in flight |

## Part 1: Tag-cut readiness matrix

### v0.2.1 (Linux MVP + design 023/024 Slice 1)

- Anchor: `ca4d824` (last fresh-VM-login fix). Verified ancestor of `origin/main`.
- RELEASE-NOTES: `RELEASE-NOTES-v0.2.1.md`, 285 lines, drafted at R41.
- ROADMAP rows shipped at this anchor: `cove shell <vm>` server-side at
  `17211bd`; client at `33fbe7e`; image store + `cove image build/list/rm` at
  `8a106dc`; `-fork-from <local-image-ref>` shipped with `8a106dc`;
  github-runner / gitlab-runner / tailscale vzscripts shipped.
- Known regressions: none mechanical. `make release-check` PASS at R41.
- Operator-gated blocker (per R41 `v0.2.1-readiness-final.md`):
  current `main` already contains `RELEASE-NOTES-v0.3.0.md`,
  `RELEASE-NOTES-v0.4.0.md`, and `docs/release/v0.4.0-readiness.md`. Cutting
  `v0.2.1` at current HEAD would label history "v0.2.1" while the worktree
  contains v0.3+v0.4+v0.5 surfaces. The operator must pick one of:
  1. tag the historical commit that actually corresponds to the v0.2.1 surface;
  2. accept current HEAD as a backfill tag with the version-order mismatch;
  3. skip v0.2.1 entirely and cut only later tags.
- Live VM smoke remains BLOCKED on operator disk-space approval.
- Verdict: **operator decision required** before any tag command runs.

### v0.3.0

- Anchor: `ca4d824` per R41. Verified ancestor.
- RELEASE-NOTES: `RELEASE-NOTES-v0.3.0.md`, 186 lines.
- ROADMAP rows shipped at this anchor: `cove build` VM execution path; `# secret:`
  tmpfs handling; build compaction (`fast`, `targeted`, `thorough`); fork-only
  benchmark publication; boot-to-agent benchmark publication; ControlServer
  decomposition phase 3 at `cede792`; OpenAI Agents SDK adapter v1 + release
  hardening; Anthropic sandbox-runtime adapter at `fafc32a`; curated agentkit
  base images at `92f2272`; disk I/O tuning + Linux NVMe Slice 2 (`fc7ff1e`,
  `a459076`, `8500ecb`, `968cbde`, `1b7c947`); block device passthrough
  (`b522ab3`, `a78e891`, `74d9527`); GHA executor Slice 2 cache reuse
  (`9e6253a`, `f06d554`, `c0a1433`).
- Mechanical gates: PASS per R41 `v0.3-readiness-final.md`.
- Operator-gated blocker: live local-base build smoke (cold-cache, cache-hit,
  failure-cleanup, keep-intermediate, SIGINT, secret+compaction) was BLOCKED
  on disposable-VM disk space. Operator either runs it now or accepts the
  missing smoke as recorded risk. Developer ID and notarytool credentials NOT
  RUN by audit; the local release script enforces both at tag time.
- Verdict: **mechanically ready; one live-smoke decision blocks the tag**.

### v0.4.0

- Anchor: `14cabac` (v0.4 closeout commit per ROADMAP). Verified ancestor.
- RELEASE-NOTES: `RELEASE-NOTES-v0.4.0.md`, 126 lines.
- ROADMAP rows shipped at this anchor: VM identity preservation for forked
  vmstate (`67c9abc`, `7e4ed99`, `4f8035c`); Anthropic computer-use adapter v2
  (`55a2463`, `33e5b30`, `775537f`); OpenAI Agents SDK `SandboxRunConfig`
  (`36552c2`, `4d61edd`, `27f9e24`); GHA private executor (`0985377`,
  `19804c7`, `8bd473e`, `82a0ac5`, `7fafe40`, `9e6253a`); NixOS guest
  (`1ddd3b9`, `8324750`, `2427b2e`, `f1e6812`); Linux Desktop autoprovisioning
  (`e93a3e0`, `449cbfa`, `0cbc455`, `4f71eb3`); Fleet Slices 1-3 (`622b571`,
  `695ae2e`, `9f993a5`, `366bfac`, `afba1a5`, `0b4776f`, `e59348d`,
  `6e91044`, `f13dae5`, `1273fcb`, `e347836`); coved daemon (`c94557d`,
  `d786e53`, `a9a2a9b`, `3258982`, `ef019c1`, `2b516da`, `c6f33df`); VM
  lifecycle policy v2 (`12c391d`, `d1df12b`, `2202f46`, `80eea77`, `9749f29`,
  `cd899a1`); per-VM resource quotas (`94bf2d2`, `62a71aa`, `2bad0e8`); image
  lifecycle UX (`fb37866`, `75c1897`, `1e9806e`, `7cbbf9c`, `6f0d396`,
  `b46deb0`); network policy + logs + forwarding (`1ac32f9`, `6fd5bc5`,
  `6e6fa18`, `22e6c43`, `235b678`); operator file/log/diff (`dbe1520`,
  `fca848e`, `9fc8303`, `b46deb0`, `535db8c`); reliability sweep (`34775a4`,
  `3456d67`, `d3a6d18`, `ef82fc0`, `9dea52e`, `f78ce61`); fresh-VM-login
  cluster (`b9c06ee`, `e512329`, `a1742f4`, `14e2bdb`, `ca4d824`); Cirrus
  migration docs (`bdd1912`, `2642d01`, `e413e0e`, `6017373`, `4635254`).
- Mechanical gates: PASS per R41 `v0.4.0-readiness.md`. `go build`,
  `codesign --entitlements internal/autosign/vz.entitlements`, `go test ./...`
  all green.
- Operator-gated blocker: `#224` and `#225` user approvals remain open.
- Visible release-notes risks kept open: Ubuntu Desktop first-boot reliability
  (`#237`); vmstate Phase 5 follow-on (`#232`); public OCI distribution remains
  private (privacy gate); Cirrus migration docs are guidance, not an automatic
  converter.
- Verdict: **mechanically ready; awaits user approval on `#224`/`#225`**.

### v0.5 milestone

- Anchor: `8bd7a65` ("controlserver: move Network bridge"). Verified ancestor
  (it is `origin/main`).
- RELEASE-NOTES: not yet drafted. `R51-RELEASE-PREP` (CA0E2BF4 / 6A04C089
  conductor lane) owns the draft per `/tmp/r51-v05-release-prep-goal.md`. This
  audit deliberately leaves notes-authoring to that lane.
- Headline: design 039 §7 (ControlServer facade extraction) is shipped.
  ControlServer is now a thin facade in package main; all five sub-component
  bridges live under `internal/controlserver/`:
    - Capture + Lifecycle bridges moved at `8dcf3e9` and `09fc1d1` (R48).
    - Agent bridge moved at `cc9d12f` (R49).
    - Input bridge moved at `d6ee2e0` (R49).
    - Network bridge moved at `8bd7a65` (R49); inbound type lifts
      (`HTTPListeners`, `VNCStatus`, `DebugStubStatus`) at `b26c7c1` /
      `849fb6d`; `NetworkHost` interface at `62068ce`; LifecycleContext
      duplicate dropped at `8806f7d`.
- Other v0.5 work that has shipped at `8bd7a65`:
    - vmrun §5 isoPath threading: `a61cdd1`.
    - TCC AE pre-auth Phase 2 (cove doctor TCC pre-auth path):
      `7682787`, `83634ef`, `d2c5d6d`, `916e918`.
    - Two long-carried provisioning test failures fixed at `1f8435c`
      (clear `CLAUDECODE` / `IS_SANDBOX` env in native-auth tests) and at
      `df5ed17` (stop suggesting sudo for native auth).
    - Dead-code sweep: `0a1d91d` (cove-action `cacheImageExists`),
      `34fdf4d` (softreset `hostPort`), `4349d8d` (`activeConnections`),
      `1adbfaa` (dead `httpListeners`), `b52d2d6` (dead agentBridge wrappers).
- Cumulative since v0.4 anchor `14cabac`: 225 commits.
  ```
  $ git log --oneline 14cabac..origin/main | wc -l
  225
  ```
- ROADMAP `docs/designs/ROADMAP.md` (line 156) currently labels the
  Agent/Input/Network facade move as `maybe`/"deferred to v0.6 unless v0.5 has
  time". That row is stale: all three bridges shipped before `8bd7a65`. The
  R51-RELEASE-PREP lane owns the ROADMAP edit; this audit only flags it.
- Mechanical gates: not yet executed at `8bd7a65` for v0.5 release purposes.
  R51-RELEASE-PREP must run `make release-check` and the entitlement re-sign
  before any v0.5 tag is considered.

## Part 2: v0.5 scope decision support

What landed post-v0.4 that the operator can choose to ship under v0.5:

1. **Design 039 §7 facade closure**: five sub-component bridges fully extracted
   to `internal/controlserver/`. Behavioral diff = none; package-boundary
   change only. Anchor `8bd7a65`.
2. **vmrun §5 isoPath**: ISO path threading through `RunConfig` is the first
   real consumer of the §5 plan. Anchor `a61cdd1`.
3. **TCC Apple Events pre-auth Phase 2**: `cove doctor tcc-preauth` shows host
   probe state and persists a state file. Anchors `7682787`, `83634ef`,
   `d2c5d6d`, `916e918`.
4. **Native-auth test stabilization**: two provisioning tests that had been
   carrying as "known pre-existing failures" through R47-R49 now pass under
   any harness. Anchor `1f8435c`. Memory file
   `feedback_provisioning_native_auth_tests_fixed.md` records the fix.
5. **Dead-code sweep**: five U1000 / dead-field removals at `0a1d91d`,
   `34fdf4d`, `4349d8d`, `1adbfaa`, `b52d2d6`.

What is in flight or queued in the R51 round (per conductor `0AA1EC69`):

- R51-FACADE-CLEANUP: post-facade dead-code triage in package main and
  `internal/controlserver/`, plus design 039 closeout line. Owned by lane
  CA0E2BF4. NOT touched by this audit.
- R51-RELEASE-PREP: drafts `RELEASE-NOTES-v0.5.0.md`, updates ROADMAP, adds
  v0.5 readiness doc. Owned by lane 6A04C089. NOT touched by this audit.
- R51-CIRRUS-LANDING: Cirrus migration landing-page polish for the 24-day
  runway. Owned by lane `01CC8872`. NOT touched by this audit.

Two realistic v0.5 scope shapes:

- **Minimal**: design 039 facade closure + R51-FACADE-CLEANUP polish + Cirrus
  migration polish. Ship by 2026-06-01 to capture displaced Cirrus runners.
- **Expansive**: above plus open ROADMAP rows that have shipped piecemeal
  (vmrun §5 isoPath, TCC AE Phase 2, native-auth test fix, dead-code sweep).
  No additional code work; only docs/closeout. Same calendar.

The minimal shape is the same set of commits as the expansive shape — they
differ only in how much of the existing surface the v0.5 release notes claim.
Recommend the expansive shape because the work has already shipped; not
claiming it would understate the release.

## Part 3: Cirrus 24-day runway prioritization

Cirrus CI shuts down 2026-06-01. As of 2026-05-07, **24 days remain**. Per
`docs/strategy/competitive-2026-05.md`, the post-T68 ranked-investment list is
already shipped (`cove action doctor` / `prepare-image`, `cove runs
list/show/export`, `cove image verify` provenance, `cove agent-sandbox run`
unified, network policy v2). The remaining wedge is migration ergonomics, not
new capability.

The three highest-leverage shipments before 2026-06-01:

1. **Cirrus migration landing-page hardening (R51-CIRRUS-LANDING).**
   Today's landing pages are at `bdd1912` (`docs/migrate-from-cirrus.md`),
   `2642d01` (`docs/landing/cirrus-displacement.md`), and `e413e0e`
   (`docs/landing/migration-walkthrough.md`). These need: a quick-eval path
   that runs without Apple Silicon Pro hardware; a `.cirrus.yml` -> `cove
   run` worked example with zero hand-edits; an explicit "what does NOT
   port" section; and a "migrate in one weekend" 5-step checklist. Owned by
   lane `01CC8872`. Highest leverage because the most-displaced users land
   on these pages.

2. **`v0.4.0` and `v0.5` tag cuts behind a public-distribution gate.**
   v0.4.0 is mechanically ready (`#224`/`#225`). v0.5 needs RELEASE-NOTES +
   ROADMAP closeout. Cutting both in the runway window gives Cirrus
   migrators a stable artifact to evaluate instead of a moving HEAD. The
   privacy gate (cove repo private) blocks public pushes to homebrew-tap
   and nixpkgs, but local artifacts and the Cirrus migration guide can still
   point at git tags.

3. **Live local-base build smoke (gates v0.3 closure).** v0.3's BLOCKED
   live-smoke gate is the only purely operator-runnable item between cove
   and a clean v0.3 tag history. Running it on a disposable VM unblocks the
   v0.2.1/v0.3 decision so the operator can choose whether to skip both,
   backfill both, or tag the historical anchor. This decision lands cleanly
   only with v0.3 fully verified.

Explicitly NOT recommended for the runway window:

- Public registry / signed image channel work. The privacy gate is
  load-bearing.
- New Tart-style networking depth. Network policy v2 already shipped; further
  parity work is post-v0.5.
- Cua-style background macOS computer-use polish. Out of scope for migration
  conversion.
- Public homebrew-tap / nixpkgs pushes. Privacy gate; will 404 for
  non-collaborators (memory `feedback_no_public_pushes_while_private.md`).

## Part 4: Release-cut command sequences

The R41 `tag-cut-runbook.md` covers v0.2.1 and v0.3.0. v0.4.0 and v0.5
sequences below extend that runbook, with the same preflight contract.

### Common preflight (all tags)

```bash
git fetch origin main --tags
git status --short --branch
git log --oneline origin/main..HEAD
go test ./...
go build -o cove .
codesign -s - -f --entitlements internal/autosign/vz.entitlements ./cove
codesign -d --entitlements - ./cove 2>&1 | grep -E 'com.apple.security.virtualization|com.apple.vm'
make release-check
```

The working tree must be clean. The local build must be re-signed after every
`go build`. Memory `project_v011_cut.md` records the v0.1.1 cut process and
the helper-daemon plist-ownership requirement.

### v0.2.1

See `docs/release/tag-cut-runbook.md` § v0.2.1. Operator decision required
first per Part 1 above. Homebrew tap publish is gated by privacy posture
(privacy gate is load-bearing).

### v0.3.0

See `docs/release/tag-cut-runbook.md` § v0.3.0. Live local-base smoke must
either run or be explicitly accepted as recorded risk per
`docs/release/v0.3-readiness-final.md`.

### v0.4.0

```bash
# preflight (above)
git tag -a v0.4.0 -m "v0.4.0 release"
git push origin v0.4.0
dist/build-v0.4.0.sh
dist/smoke-test.sh ./dist/v0.4.0/cove <fresh-vm-name>
gh release create v0.4.0 \
  --title "v0.4.0" \
  --notes-file RELEASE-NOTES-v0.4.0.md \
  dist/cove_0.4.0_darwin_arm64.tar.gz \
  dist/cove-0.4.0.dmg \
  dist/SHA256SUMS-v0.4.0.txt
```

`dist/build-v0.4.0.sh` and the SHA256SUMS file must exist or be added by
release-prep. Homebrew tap publish remains skipped while the cove repo is
private.

### v0.5

R51-RELEASE-PREP is responsible for:

1. drafting `RELEASE-NOTES-v0.5.0.md`;
2. updating `docs/designs/ROADMAP.md` to reflect that the design 039 facade
   row is shipped at `8bd7a65` (rather than deferred to v0.6);
3. writing or updating a v0.5 readiness doc.

Until those land, no v0.5 tag command should be drafted. The expected shape,
once the prep ships, is the same as v0.4.0:

```bash
# preflight (above)
git tag -a v0.5.0 -m "v0.5.0 release"
git push origin v0.5.0
dist/build-v0.5.0.sh
dist/smoke-test.sh ./dist/v0.5.0/cove <fresh-vm-name>
gh release create v0.5.0 \
  --title "v0.5.0" \
  --notes-file RELEASE-NOTES-v0.5.0.md \
  dist/cove_0.5.0_darwin_arm64.tar.gz \
  dist/cove-0.5.0.dmg \
  dist/SHA256SUMS-v0.5.0.txt
```

### Pre-flight verifications

Before any tag, verify:

- `git rev-parse <anchor>` produces the hash claimed in this audit.
- `git merge-base --is-ancestor <anchor> origin/main` exits 0.
- `RELEASE-NOTES-v<N>.md` exists and is final (not "draft").
- `make release-check` passes on a clean checkout at the tag's anchor.
- `codesign -d --entitlements - ./cove` lists
  `com.apple.security.virtualization`.
- For Cirrus-runway shipments, `docs/migrate-from-cirrus.md`,
  `docs/landing/cirrus-displacement.md`, and
  `docs/landing/migration-walkthrough.md` resolve to the tagged anchor.

### Rollback posture

Local tags can be removed with `git tag -d <name>` before push. After push,
prefer `git push --delete origin <tag>` only if no GitHub Release artifacts
have been uploaded; once a release exists, instead cut a `<tag>+1` (e.g.
`v0.4.1`) with corrected notes and mark the prior release as superseded.

The privacy gate (cove repo private) means public Homebrew tap, nixpkgs, and
public OCI registry pushes are forbidden by repo policy until the operator
flips the repo public. Memory file
`feedback_no_public_pushes_while_private.md` is load-bearing.

## Audit completion gate

| Requirement                                              | Evidence                                           |
|----------------------------------------------------------|----------------------------------------------------|
| Tag-cut readiness matrix for v0.2.1 / v0.3 / v0.4        | Part 1 above; cites R41 audits and ancestor anchors |
| v0.5 scope decision support                              | Part 2 above; cites 225-commit count, anchor `8bd7a65` |
| Cirrus 24-day runway picks                               | Part 3 above; grounded in `docs/strategy/competitive-2026-05.md` |
| Release-cut command sequences (extends `tag-cut-runbook.md`) | Part 4 above; preflight contract preserved      |
| Every "ready" claim cites a hash verified via `git show` | All 12 spot-checked anchors are `is-ancestor origin/main` (see R50 audit log) |
| Read-only audit + ONE doc commit                         | This file is the single new artifact              |
| Do not cut tags                                          | No tag commands run by this audit                 |
| Russ aesthetic; no emojis                                | Plain prose; no emojis                            |
