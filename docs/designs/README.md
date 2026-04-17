# Design Docs

Architectural proposals for cove features, post-review. Each doc has been through multi-agent Council review and (for most) a second-opinion pass from an independent reviewer role. Status and review rounds are in each doc's frontmatter.

## Current set

1. [cove serve — HTTP & MCP](001-cove-serve-http-mcp.md) — v0.1 — HTTP and MCP subcommand exposing per-VM control socket. Master-token keychain auth. LRO persistence.
2. [cove disks & OCI](002-cove-disks-oci.md) — v0.1/v0.2 — push/pull against OCI registries, asymmetric lume compat with --lume-compat escape valve, content-addressed store in v0.2.
3. [cove build — OCI layer caching](003-cove-build-oci-caching.md) — v0.3 — docker-build-style vzscript layer caching. # secret: tmpfs with guest-swap hardening. Cross-machine digest stability is a benchmark-gated claim.
4. [Churn benchmark harness](004-churn-benchmark-harness.md) — pre-v0.3 — the 20-cell experiment that picks the default `compact_mode` and gates the cross-machine cache-from story.
5. [v0.4 secrets architecture](005-v04-secrets-architecture.md) — v0.4 — Council-consultation brief recommending URI delegation for external secret stores (1Password, Vault, SOPS, age).
6. [cove Linux support](006-cove-linux-v02.md) — v0.2 — Linux guest support: nested virt (M3/M4 gated), 4 distros, agent unary RPCs (ResizeExecTTY/SignalExec/SetTime), connect-go polyglot server, Docker-shaped HTTP URLs.

## How to amend

These docs are canonical until superseded by a successor with the same stem (e.g. `003-cove-build-oci-caching.md` → `003a-cove-build-parallel-steps.md`). Do not edit a locked design in place; write a new doc that references it.

## Review archive

Council review rounds and second-opinion findings are preserved as `changelog` entries within each doc's frontmatter.
