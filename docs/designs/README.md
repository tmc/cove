# Design Docs

Architectural proposals for cove features, post-review. Each doc has been through multi-agent Council review and (for most) a second-opinion pass from an independent reviewer role. Status and review rounds are in each doc's frontmatter.

## Living roadmap

[ROADMAP](ROADMAP.md) is the active roadmap. It rolls up the notebook-backed
strategy, the post-v0.1 handoff, the soft-reset empirical result, and the latest
implementation review. Start there before choosing new work.

## Current set

1. [cove serve — HTTP & MCP](001-cove-serve-http-mcp.md) — v0.1 — HTTP and MCP subcommand exposing per-VM control socket. Master-token keychain auth. LRO persistence.
2. [cove disks & OCI](002-cove-disks-oci.md) — v0.1/v0.2 — push/pull against OCI registries, asymmetric lume compat with --lume-compat escape valve, content-addressed store in v0.2.
3. [cove build — OCI layer caching](003-cove-build-oci-caching.md) — v0.3 — docker-build-style vzscript layer caching. # secret: tmpfs with guest-swap hardening. Cross-machine digest stability is a benchmark-gated claim.
4. [Churn benchmark harness](004-churn-benchmark-harness.md) — pre-v0.3 — the 20-cell experiment that picks the default `compact_mode` and gates the cross-machine cache-from story.
5. [v0.4 secrets architecture](005-v04-secrets-architecture.md) — v0.4 — Council-consultation brief recommending URI delegation for external secret stores (1Password, Vault, SOPS, age).
6. [cove Linux support](006-cove-linux-v02.md) — v0.2 — Linux guest support: nested virt (M3/M4 gated), 4 distros, agent unary RPCs (ResizeExecTTY/SignalExec/SetTime), connect-go polyglot server, Docker-shaped HTTP URLs.
7. [soft-reset empirical result](015-soft-reset-empirical.md) — post-v0.1 — soft reset is not an isolation primitive; privacy-critical evals use VM fork/restore.
8. [NotebookLM roadmap refresh](016-notebooklm-roadmap-refresh-2026-04-30.md) — post-integration docs pass — production-docs corrections and next-roadmap ordering after the v0.1.2/v0.2/v0.3 branch integration.
9. [v0.3 execution roadmap](017-v03-execution-roadmap.md) — implementation slices for `cove build`, secrets, compaction, boot-to-agent benchmarks, and adapter hardening.
10. [v0.3 build executor scaffold](018-v03-build-executor-scaffold.md) — Slice 1 implementation contract for scratch lifecycle, locks, cleanup, and tests while keeping non-dry-run gated.
11. [v0.3 cache-hit materialization](019-v03-cache-hit-materialization.md) — Slice 2 implementation contract for applying cached build layers without VM boot.
12. [v0.3 cache-miss execution](020-v03-cache-miss-execution.md) — Slice 3 implementation contract for VM execution, layer persistence, and the point where non-dry-run builds become supported.
13. [v0.4 CI executors](021-v04-ci-executors-tracks.md) — v0.4 — GitHub Actions and GitLab executors as wrappers over `cove run -fork-from`, the control socket, and the guest agent. Slice 1 GHA action, Slice 2 GitLab shell-runner shim.
14. [v0.4 Anthropic adapter](022-v04-anthropic-adapter.md) — v0.4 — Anthropic computer-use adapter mirroring the OpenAI Agents SDK adapter shape. Slice 1 SDK survey (Anthropic has no `ComputerTool` analogue; adapter drives the Messages API agent loop directly). Slice 2 `cove-claude-sandbox` Python package.
15. [cove shell — Docker-shaped exec UX](023-cove-shell-exec-ux.md) — v0.2.1 / v0.3 — standalone `cove shell <vm>` subcommand brokering exec through the per-VM control socket because vsock requires VM-owner-process. Slice 1 control-socket extension (`agent-exec-attach/-resize/-signal`); Slice 2 `cove shell` client; Slice 3 v0.3 proto `ExecAttach` bidi RPC for true interactive stdin.

## Strategy inputs

- [beat lume roadmap](archive/011-beat-lume-roadmap.md) — 0.1 -> 0.4 — strategic roadmap input: win on local state, guest-agent control, Linux developer workflows, and `cove build`; use interop only at the boundary.
- [product roadmap 2026](archive/012-product-roadmap-2026.md) — notebook-backed strategy source for fork/restore, build, agent adapters, and registry sequencing.
- [roadmap update post-v0.1](archive/014-roadmap-update-post-v0.1.md) — post-v0.1 handoff; superseded where it conflicts with [015](015-soft-reset-empirical.md), [016](016-notebooklm-roadmap-refresh-2026-04-30.md), [017](017-v03-execution-roadmap.md), [018](018-v03-build-executor-scaffold.md), [019](019-v03-cache-hit-materialization.md), [020](020-v03-cache-miss-execution.md), and [ROADMAP](ROADMAP.md).

## How to amend

These docs are canonical until superseded by a successor with the same stem (e.g. `003-cove-build-oci-caching.md` → `003a-cove-build-parallel-steps.md`). Do not edit a locked design in place; write a new doc that references it.

## Review archive

Council review rounds and second-opinion findings are preserved as `changelog` entries within each doc's frontmatter.
