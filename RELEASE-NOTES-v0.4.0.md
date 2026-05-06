# cove v0.4.0 release notes

v0.4.0 is the CI-executor, adapter, daemon, and fleet closeout. Every item
below cites the `origin/main` commit that landed the claim.

## Headline

cove now has the pieces for an operator-owned alternative to hosted macOS CI:
private GitHub Action execution (`0985377`, `19804c7`), fork-backed run metrics
(`8318fa7`, `3f6c144`, `c390eb9`), fleet host routing (`695ae2e`, `9f993a5`),
direct cross-host image transfer (`f13dae5`, `1273fcb`), least-loaded placement
(`e347836`), and a daemon that reports lifecycle and image-GC state (`ef019c1`,
`c6f33df`).

The release also expands the computer-use story: the OpenAI Agents SDK
`SandboxRunConfig` helper landed at `4d61edd`, OpenAI integration docs at
`27f9e24`, and the Anthropic computer-use loop plus CLI integration at
`33e5b30` and `775537f`.

## Headline Ships

- **Fleet control plane.** `cove fleet add/ls/rm` and remote command routing
  landed at `695ae2e` and `9f993a5`; aggregate `vm list`, `image list`, and
  `ps` landed at `e59348d` and `6e91044`; direct image push/pull/sync landed
  at `f13dae5` and `1273fcb`; least-loaded placement landed at `e347836`.
- **Host daemon.** `coved` and `cove daemon` CLI scaffolding landed at
  `d786e53` and `a9a2a9b`; image-GC scheduling and status counters landed at
  `3258982` and `ef019c1`; lifecycle enforcement landed at `2b516da` and
  `c6f33df`.
- **Lifecycle policy and quotas.** Policy persistence, CLI, and enforcement
  landed at `d1df12b`, `2202f46`, and `80eea77`; run-budget locking and
  telemetry landed at `9749f29` and `cd899a1`. Per-VM quota persistence,
  APFS quota application, and `cove quota` landed at `94bf2d2`, `62a71aa`,
  and `2bad0e8`.
- **Guest OS coverage.** NixOS installer support, base recipe, and quickstart
  landed at `8324750`, `2427b2e`, and `f1e6812`. Linux Desktop auto-login
  hardening landed at `449cbfa`, `0cbc455`, and `4f71eb3`.
- **Image and operator UX.** Image prune/tag/history/search landed at
  `fb37866`, `75c1897`, `1e9806e`, and `7cbbf9c`; image inspect diff support
  landed at `9fc8303` and `b46deb0`; `cove cp` and `cove logs` landed at
  `dbe1520` and `fca848e`.
- **Network and forwarding.** Named network policy parsing and audit logs
  landed at `1ac32f9` and `6fd5bc5`; `cove network logs` landed at `6e6fa18`;
  reverse forwarding and UDP forwarding landed at `22e6c43` and `235b678`.
- **Soft-reset evidence.** Destructive soft-reset probes and the orchestrator
  landed at `8c7d54b`, `a51c544`, `31017ca`, `0ed3b70`, `ad9dc16`, and
  `7030657`. The release keeps the empirical conclusion from design 015:
  fork/restore remains the isolation primitive.
- **Cirrus migration docs.** The migration guide, landing page, walkthrough,
  README link, and blog draft landed at `bdd1912`, `2642d01`, `e413e0e`,
  `6017373`, and `4635254`.

## New CLI Surface

- `cove fleet add|ls|rm` and remote routing through `--fleet=<name>`:
  `695ae2e`, `9f993a5`.
- `cove fleet vm list`, `cove fleet image list`, `cove fleet ps`:
  `e59348d`, `6e91044`.
- `cove fleet image push|pull|sync` and `cove fleet run --policy=least-loaded`:
  `1273fcb`, `e347836`.
- `cove daemon status` and `coved`: `a9a2a9b`, `d786e53`, `ef019c1`,
  `c6f33df`.
- `cove policy` / `cove vm policy show`: `2202f46`, `80eea77`, `cd899a1`.
- `cove quota`: `2bad0e8`.
- `cove install -nixos`: `8324750`.
- `cove agent-sandbox run --provider anthropic`: `775537f`.
- `cove image prune`, `tag`, `history`, `search`, and inspect diffs:
  `fb37866`, `75c1897`, `1e9806e`, `7cbbf9c`, `b46deb0`.
- `cove cp`, `cove logs`, `cove forward -reverse`, UDP forwarding, and
  `cove network logs`: `dbe1520`, `fca848e`, `22e6c43`, `235b678`, `6e6fa18`.

## Behavior Changes

- macOS `cove up` now refuses non-root provisioning instead of continuing to a
  VM that may not contain the requested user account (`a1742f4`).
- Native authorization refuses noninteractive stdin before
  `AuthorizationExecuteWithPrivileges` can block on an unseen dialog
  (`14e2bdb`).
- Fresh VM startup now publishes startup state and has a start watchdog
  (`9dea52e`, `f78ce61`).
- The daemon status surface now includes lifecycle and image-GC counters
  (`ef019c1`, `c6f33df`).

## Bug Fixes

- Fresh-VM-login cluster: repro doc (`b9c06ee`), OCR login-screen detection
  (`e512329`), non-root `cove up` refusal (`a1742f4`), noninteractive
  authorization refusal (`14e2bdb`), and Data-partition diagnostic output
  (`ca4d824`).
- Provisioning reliability: authorization prompt pre-warm (`af1baa3`),
  kcpassword byte validation (`3450309`), and install overlay phase polish
  (`ef82fc0`).
- Reliability sweep: test-HOME audit (`34775a4`), concurrency fixes
  (`3456d67`), Apple virtualization log predicate refresh (`d3a6d18`), and
  run startup watchdog (`f78ce61`).

## Migration Notes

- Cirrus users should start with `docs/migrate-from-cirrus.md` (`bdd1912`),
  `docs/landing/cirrus-displacement.md` (`2642d01`), and
  `docs/landing/migration-walkthrough.md` (`e413e0e`). These are migration
  docs, not an automatic `.cirrus.yml` converter.
- Fleet features require SSH reachability to trusted Mac hosts; the base fleet
  docs landed at `366bfac`, and cross-host image transfer docs landed at
  `ec75f1b`.
- Public OCI distribution and public curated image channels remain out of
  scope for this private-repo release; the roadmap still keeps public registry
  work behind the product/name decision.

## Acknowledgments

This release closes the R36-R40 ship cluster across fleet, daemon, lifecycle,
adapter, Linux, reliability, and Cirrus-migration work. The release closeout
keeps commit hashes in the notes so each claim can be verified against
`origin/main`.

## Known Issues

- Do not cut `v0.4.0` until the user-gated tag approvals named in
  `docs/release/v0.4.0-readiness.md` are complete.
- Ubuntu Desktop first-boot reliability has multiple fixes in this release
  (`449cbfa`, `0cbc455`, `4f71eb3`), but the release notes keep it visible as
  an operator-facing risk rather than a universal guarantee.
- Cirrus migration docs are guidance and examples, not an automatic migration
  engine (`bdd1912`, `2642d01`, `e413e0e`, `4635254`).
- Public image registry publication remains deferred by roadmap policy.
