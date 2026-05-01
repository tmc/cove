# NotebookLM roadmap refresh - 2026-04-30

**Status**: accepted docs input.
**Source**: NotebookLM notebook `79a32e96-8e1c-4e89-9385-20193e3a8209`, synced
source `2284c48c-3979-4c8e-b3fc-4c5528565ce4`
(`cove-current-roadmap-release-state`).

## Context

After the v0.1.2, v0.2, and early v0.3 branch integration, the current repo
state was synced into NotebookLM and reviewed against older roadmap sources.
The current repository state wins when it conflicts with prior notebook notes.
Date-sensitive legal, license, pricing, competitor, and market claims remain
research inputs until reverified against primary sources.

## Corrections to keep in public docs

- Soft reset is not an isolation primitive. Do not use "thousands per hour" or
  similar soft-reset throughput language. Privacy-critical evals use VM
  fork/restore.
- Tart should not be described as abandoned or inactive. The current documented
  wedge is license arithmetic and cove's MIT posture, not a stale maintenance
  claim.
- APFS `clonefile` is not exclusive to cove. Cove's claim is named
  multi-snapshot fork/restore plus vsock control, VZScript, OCI push/pull, and
  agent-facing automation.
- `cove build` non-dry-run execution now runs locally for VM-directory bases:
  cache hits skip guest execution, misses run vzscript steps in a scratch VM,
  layers and metadata are persisted, and the final VM directory can be handed
  to `cove push`. Registry-base execution and registry cache import/export
  remain explicitly deferred — public docs must keep that boundary visible.
- The `cove` trademark warning stays visible until counsel clears the name or a
  rename lands.

## What ships in this RC

- `cove build` non-dry-run execution against a local VM-directory base.
- `# secret:` tmpfs mounting with guest swap disabled before secrets land.
- `cove compact` integrated into the build pipeline (`fast`, `targeted`,
  `thorough`) before diffing and pushing layers.
- Published fork-only and boot-to-agent fork benchmarks on named hardware.
- OpenAI Agents SDK adapter v1 plus release hardening under
  `adapters/openai-agents-python`.

## Explicitly deferred for this RC

- Registry-base `cove build` execution (non-local bases stay planning-only).
- Registry cache import/export (`--cache-from`, `--cache-to`).
- Public curated `cove` image registry and signed agentkit image channels.
- External secret stores (1Password, Vault, SOPS, age) — v0.3 secrets are host
  environment variables mounted through tmpfs only.
- BuildKit-style parallel step execution; v0.3 build execution is sequential.
- Packer plugin shim work.
- Product-name resolution before any public registry or signed channel.

## Docs work from this pass

- Add a dedicated license and virtualization limits reference page.
- Link README, INSTALL, docs installation, safety posture, and comparison docs
  to the reference page.
- Keep the build CLI and release checklist explicit about which build modes
  ship and which remain deferred (registry bases, registry cache, signed
  channels, external secret stores, BuildKit parallel, Packer, naming).
- Keep the roadmap ordered around build execution, secrets, compaction,
  benchmark publication, and trademark gating.
