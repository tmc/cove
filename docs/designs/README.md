# Design Docs

Architectural proposals for cove features, post-review. Each doc has been through multi-agent Council review and (for most) a second-opinion pass from an independent reviewer role. Status and review rounds are in each doc's frontmatter.

## Living roadmap

[ROADMAP](ROADMAP.md) is the active roadmap. It rolls up the notebook-backed
strategy, the post-v0.1 handoff, the soft-reset empirical result, and the latest
implementation review. Start there before choosing new work.

## Current set

1. [cove serve — HTTP & MCP](archive/001-cove-serve-http-mcp.md) — v0.1 — HTTP and MCP subcommand exposing per-VM control socket. Master-token keychain auth. LRO persistence.
2. [cove disks & OCI](archive/002-cove-disks-oci.md) — v0.1/v0.2 — push/pull against OCI registries, asymmetric lume compat with --lume-compat escape valve, content-addressed store in v0.2.
3. [cove build — OCI layer caching](archive/003-cove-build-oci-caching.md) — v0.3 — docker-build-style vzscript layer caching. # secret: tmpfs with guest-swap hardening. Cross-machine digest stability is a benchmark-gated claim.
4. [Churn benchmark harness](004-churn-benchmark-harness.md) — pre-v0.3 — the 20-cell experiment that picks the default `compact_mode` and gates the cross-machine cache-from story.
5. [v0.4 secrets architecture](005-v04-secrets-architecture.md) — v0.4 — Council-consultation brief recommending URI delegation for external secret stores (1Password, Vault, SOPS, age).
6. [cove Linux support](006-cove-linux-v02.md) — v0.2 — Linux guest support: nested virt (M3/M4 gated), 4 distros, agent unary RPCs (ResizeExecTTY/SignalExec/SetTime), connect-go polyglot server, Docker-shaped HTTP URLs.
7. [vzscript host-file copying — v3](007-vzscript-host-files-v3.md) — v0.3 — supersedes v1/v2; native `WriteFile`/`CopyIn` user-agent RPCs, basename allowlist hardened against symlink evasion.
8. [VM forking](013-vm-fork.md) — post-v0.1 — `cove fork` produces a runnable child in O(seconds) via APFS clonefile; lineage tracking and ephemeral siblings on Apple Silicon.
9. [soft-reset empirical result](archive/015-soft-reset-empirical.md) — post-v0.1 — soft reset is not an isolation primitive; privacy-critical evals use VM fork/restore.
10. [NotebookLM roadmap refresh](archive/016-notebooklm-roadmap-refresh-2026-04-30.md) — post-integration docs pass — production-docs corrections and next-roadmap ordering after the v0.1.2/v0.2/v0.3 branch integration.
11. [v0.3 execution roadmap](archive/017-v03-execution-roadmap.md) — implementation slices for `cove build`, secrets, compaction, boot-to-agent benchmarks, and adapter hardening.
12. [v0.3 build executor scaffold](archive/018-v03-build-executor-scaffold.md) — Slice 1 implementation contract for scratch lifecycle, locks, cleanup, and tests while keeping non-dry-run gated.
13. [v0.3 cache-hit materialization](archive/019-v03-cache-hit-materialization.md) — Slice 2 implementation contract for applying cached build layers without VM boot.
14. [v0.3 cache-miss execution](archive/020-v03-cache-miss-execution.md) — Slice 3 implementation contract for VM execution, layer persistence, and the point where non-dry-run builds become supported.
15. [v0.4 CI executors](archive/021-v04-ci-executors-tracks.md) — v0.4 — GitHub Actions and GitLab executors as wrappers over `cove run -fork-from`, the control socket, and the guest agent. Slice 1 GHA action, Slice 2 GitLab shell-runner shim.
16. [v0.4 Anthropic adapter](archive/022-v04-anthropic-adapter.md) — shipped on 2026-05-05 (`55a2463`, `33e5b30`, `775537f`) — Anthropic computer-use adapter over the Messages API, wired into agent-sandbox; live API verification remains key-gated.
17. [cove shell — Docker-shaped exec UX](023-cove-shell-exec-ux.md) — shipped — standalone `cove shell <vm>` subcommand brokering exec through the per-VM control socket because vsock requires VM-owner-process. Slice 1 control-socket extension (`agent-exec-attach/-resize/-signal`); Slice 2 `cove shell` client; Slice 3 v0.3 proto `ExecAttach` bidi RPC for true interactive stdin.
18. [cove runner images — publish & fork-from](024-cove-runner-images.md) — shipped through private-registry Slice 2 — publish VM disk images as OCI artifacts and fork-from to spawn ephemeral CI runners. Slice 1 `cove image build` + local image store; Slice 2 push/pull behind a privacy gate; Slice 3 public-registry promotion remains deferred while the cove repo stays private.
19. [cove-action security architecture](025-cove-action-security.md) — shipped — private GitHub Actions executor threat model and boundary contract.
20. [Ephemeral self-hosted runners](026-ephemeral-self-hosted-runners.md) — shipped — disposable VM runner flow built around fork-from images and guest-agent command execution.
21. [Disk I/O tuning](027-disk-io-tuning.md) — shipped — durable install disk attachments, disk cache policy controls, and the follow-on benchmark plan.
22. [Block device passthrough](028-block-device-passthrough.md) — shipped — raw block-device attachment support for benchmark and appliance workflows.
23. [VirtioFS hot-add](029-virtiofs-hot-add.md) — shipped through placeholder-device live update — shared-folder live-apply design after confirming Apple Virtualization has no public new-device hot-add API.
24. [GHA executor Slice 2 cache reuse](030-gha-executor-slice-2.md) — shipped — local-only cross-run cache images for the private GitHub Actions executor; implemented by T77.
25. [VM lifecycle policy](031-vm-lifecycle.md) — shipped — idle timeout, maximum age, and run-budget policies with CLI and daemon enforcement.
26. [Per-VM resource quotas](032-vm-quotas.md) — shipped — durable CPU, memory, and disk quota records plus enforcement at cove-controlled boundaries.
27. [Cove daemon mode](033-cove-daemon.md) — shipped through lifecycle policy enforcement and scheduled image GC — long-lived coordinator for policy, metrics, and cleanup work.
28. [Fleet](034-fleet-slice-1.md) — shipped through Slices 1-2 — trusted-host registry, SSH routing, aggregate views, and image transfer.
29. [OpenAI SandboxRunConfig backend](035-openai-sandbox-run-config.md) — shipped — OpenAI Agents SDK sandbox backend over cove VMs.
30. [NixOS guest support](036-nixos-guest-support.md) — shipped — first-class NixOS install/run path on the Linux VM stack.
31. [Linux Desktop autoprovisioning](037-linux-autoprov.md) — shipped with known first-boot reliability follow-ups — Ubuntu Desktop user provisioning and login setup.
32. [Agent sandbox v2](038-agent-sandbox-v2.md) — shipped as provider abstraction — OpenAI/Anthropic/Gemini/Vertex behind a common `Run` shape with fork-isolation invariants; live provider-matrix evidence still gated on credentials.
33. [Package boundary extraction](039-package-boundary-extraction.md) — shipped at v0.5 (8bd7a65) — five ControlServer sub-bridges (Capture, Lifecycle, Agent, Input, Network) extracted to `internal/controlserver/`; `internal/vmrun` carved out alongside.
34. [Storage budget for `~/.vz/`](040-storage-budget.md) — shipped Phases 0-5 on 2026-05-08 (`78b2e7b`, `ce1a2c0`, `e6a9850`, `5660b13`, `7e9ea28`, `292b81d`, `394b812`, `42714c0`, `1f7ffa4`, `ecb75ed`, `cacce19`) — unified disk-budget enforcement covering images, runs, snapshot lineages, and orphaned VM scratch.
35. [ScreenCaptureKit migration](041-screencapturekit-migration.md) — Slices 1-3 shipped on 2026-05-08 (`8d55d7a`, `d0877b8`, `55257f2`); Slice 4 spec landed (`318d801`) — replaces the deprecated `CGWindowListCreateImage` path in `screenshots.go` with `SCScreenshotManager` / `SCStream`.

## Strategy inputs

- [beat lume roadmap](archive/011-beat-lume-roadmap.md) — 0.1 -> 0.4 — strategic roadmap input: win on local state, guest-agent control, Linux developer workflows, and `cove build`; use interop only at the boundary.
- [product roadmap 2026](archive/012-product-roadmap-2026.md) — notebook-backed strategy source for fork/restore, build, agent adapters, and registry sequencing.
- [roadmap update post-v0.1](archive/014-roadmap-update-post-v0.1.md) — post-v0.1 handoff; superseded where it conflicts with [015](archive/015-soft-reset-empirical.md), [016](archive/016-notebooklm-roadmap-refresh-2026-04-30.md), [017](archive/017-v03-execution-roadmap.md), [018](archive/018-v03-build-executor-scaffold.md), [019](archive/019-v03-cache-hit-materialization.md), [020](archive/020-v03-cache-miss-execution.md), and [ROADMAP](ROADMAP.md).

## How to amend

These docs are canonical until superseded by a successor with the same stem (e.g. `003-cove-build-oci-caching.md` → `003a-cove-build-parallel-steps.md`). Do not edit a locked design in place; write a new doc that references it.

## Review archive

Council review rounds and second-opinion findings are preserved as `changelog` entries within each doc's frontmatter.
