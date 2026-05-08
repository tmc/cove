# cove v0.5.0 release notes

v0.5.0 is the stabilization release. The headline change is internal: the
`ControlServer` package-main god object is gone. All five sub-component bridges
(`Capture`, `Lifecycle`, `Agent`, `Input`, `Network`) now live in
`internal/controlserver/` behind narrow Host interfaces, and `ControlServer` in
package main is a thin facade. v0.5 also folds in design 027 disk I/O Slice 4,
design 028 block-device passthrough, design 030 GHA executor Slice 2, the
network policy v2 surface, the unified agent-sandbox provider, and a wider
`cove image` and `cove runs` CLI.

Behavioral diff against v0.4: none beyond the targeted fixes called out below.
Every commit cited resolves on `origin/main` at `8bd7a65`.

## Headline

cove now has the package boundary the project has wanted since v0.2. The
`internal/controlserver/` move closes design 039 §7 in full: ~5000 LOC moved
across the package boundary with zero behavioral diff and Host-interface
isolation in every direction. Sub-component bridges shipped at `8dcf3e9`
(scaffold + Capture), `09fc1d1` (Lifecycle), `cc9d12f` (Agent), `d6ee2e0`
(Input), and `8bd7a65` (Network).

The release also closes three vmrun open questions, lands TCC Apple Events
pre-auth Phase 2, and fixes two long-carried provisioning test failures.

## Headline ships

- **Design 039 §7 facade extraction.** `internal/controlserver/` introduced at
  `8dcf3e9` with `Capture` moved cleanly. `Lifecycle` followed at `09fc1d1`.
  `Agent` (`cc9d12f`), `Input` (`595727a` interface, `d6ee2e0` move), and
  `Network` (`62068ce` interface, `8bd7a65` move) shipped over the R49 arc.
  Each bridge has its own narrow `*Host` interface; package main keeps no
  back-references into the bridge bodies.
- **vmrun open questions closed.** `RunConfig.ISO` resolved through `Plan()` at
  `a61cdd1`; USB UUID slot added to `DevicePlan` at `2ef6126`; first `Plan`
  consumer (audio attach) routed at `8f2cb6a`.
- **TCC Apple Events pre-auth Phase 2.** `cove doctor tcc-preauth` subcommand
  with host probes shipped at `83634ef`; state file at `d2c5d6d`; `cove
  doctor` integration at `7682787`.
- **Two long-carried test failures fixed.**
  `TestApplyProvisioningDoesNotWarnWhenNativeAuthAvailable` and
  `TestRequireRootForMacOSUpProvisioningAllowsNativeAuth` resolved at
  `1f8435c` by clearing `CLAUDECODE`/`IS_SANDBOX` env vars in test setup.

## Cumulative since v0.4

The v0.4.0 anchor is `3ef3c42`. The following surfaces shipped between that
anchor and `8bd7a65`:

### Disk I/O and block devices

- Disk I/O tuning (design 027 Slice 4): cache mode + sync overrides shipped at
  `fc7ff1e` and `a459076`.
- Linux NVMe Slice 2 shipped at `8500ecb`, `968cbde`, `1b7c947`.
- Block device passthrough (design 028) shipped at `b522ab3`, `a78e891`,
  `74d9527`.

### CI executors

- GHA executor Slice 2 cross-run cache reuse (design 030) landed at `9e6253a`,
  `f06d554`, `c0a1433`.

### Network policy

- Network policy v2 surface continues from v0.4; v0.5 adds explicit policy
  enforcement on every fork-from path. See design 029 for current state.

### Agent sandbox v2

- Unified agent-sandbox provider registry shipped at `3c4a082`.
- Provider doctor at `fe15fdf`; cookbook at `e75eeba`.
- OpenAI GA computer tool wired at `b3d71ab`; computer-use model default at
  `14ab331`.
- Anthropic computer-use loop continues from v0.4; v0.5 adds the
  drop-in Go example at `b2d60cc` and provider polish at `5477a4f`.
- Replay/event recording at `1ec88a1`; benchmark gate at `81588c7`; provider
  benchmark command at `301972d`.

### `cove image` surface

- `cove image inspect/diff/verify` shipped through R36-R40 (cumulative; design
  024 Slice 2).
- Image build artifact preservation at `54b8c17`; CDN replay events at
  `1ec88a1`.

### `cove runs` and observability

- `cove runs list/show/export` shipped through the v0.4 cumulative arc.
- `coved` daemon UI mode at `94eab06`; embedded observability UI at `90b6a66`;
  internal event bus at `fa2a1bf`; webhook subscriber at `33bcf38`; Prometheus
  metrics at `94d08db`; daemon metrics command at `8f667f2`; fleet aggregation
  at `132a3af`.
- Bounded server shutdown at `cfb174b`.

### Cirrus migration

- Cirrus migration walkthrough at `0533c1f`; checklist at `fee2aa4`; doctor at
  `5757fb6`; index at `b484686`; quickstart funnel at `5ca850f`; landing draft
  at `e458848`/`fbcfd6e`; help integration at `8ed0fe3`; issue template at
  `e3189f9`.
- Competitive benchmark methodology at `0c5f4c0`, May results at `c4fe822`,
  reports at `0da3b2b`/`8b3ae3d`.

### Provisioning hardening

- Native-auth path: avoid sudo suggestion at `df5ed17`; native elevation off
  UI thread at `93d459e`; helper recovery routing at `99b05ca`; safe force
  detach at `5668685`; helper detach verification at `8a19485`.
- Autologin: cache credentials on apply at `19ac055`; avoid boot-time disk
  mount at `7977a14`; watchdog recovery at `002bd1a`/`4c1598a`.
- Shared folders: tag-into-home at `56c531d`; root symlink repair at
  `1500f54`; expand home link paths at `b63712f`; live probe doc at `5b46c15`;
  guest link status test at `95a1f41`; user routing clarification at
  `00d424b`.
- Idempotent apply at `8610367`; repeated apply UX test at `4ef3949`.
- Multi-file ownership verification at `1965ee7`/`e7b9e3e`; doctor agent +
  ownership repair at `2a3a73e`.
- Authorization watchdog at `9e75b5e`.
- Already-stopped stop race ignored in installer at `7007beb`.

### vzscript

- Xcode CLI recipe at `38f0ac2`; CLT opt-in required for homebrew at
  `1181c59`; cirrus migration doctor at `5757fb6`; terminal mode default
  streaming at `f4961d5`; Terminal automation doctor probe at `4132741`.

### Doctor and diagnostics

- Headless dock badge defer at `a68f44d`; promotion test at `f1380b0`.
- Apple Events Terminal probe at `4132741`.

### Release tooling

- Tag cut runbook at `ae57455`; tag smoke test at `ef2d329`; v0.3 tag
  readiness at `0f8c4aa`; v0.2.1 readiness at `02e14a7`; staged artifacts at
  `be98769`/`3dc6fe8`.

## Known regressions

- None. The two formerly-flaky provisioning tests are green at `1f8435c`.

## Test gates

- `go test ./...` is green at `8bd7a65`. The two long-carried provisioning
  failures cleared at `1f8435c`.
- `go build ./...` is green at `8bd7a65`.

## Tagging

This file is the release notes draft. The git tag `v0.5.0` is **not yet cut**;
tag-cut is user-gated. See `docs/release/v0.5-readiness.md` for the cut
sequence.
