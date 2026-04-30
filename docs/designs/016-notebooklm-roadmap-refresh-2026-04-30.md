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
- `cove build` is dry-run cache planning only until the VM execution path lands.
- The `cove` trademark warning stays visible until counsel clears the name or a
  rename lands.

## Next roadmap items

1. Finish the `cove build` VM execution path.
2. Add `# secret:` tmpfs handling with guest swap disabled.
3. Integrate `cove compact` into the build pipeline before diffing and pushing.
4. Publish boot-to-agent fork benchmarks on named hardware.
5. Resolve product naming before public registry or signed agentkit image
   distribution.

## Docs work from this pass

- Add a dedicated license and virtualization limits reference page.
- Link README, INSTALL, docs installation, safety posture, and comparison docs
  to the reference page.
- Keep the build CLI and release checklist explicit that non-dry-run builds fail
  until VM execution ships.
- Keep the roadmap ordered around build execution, secrets, compaction,
  benchmark publication, and trademark gating.
