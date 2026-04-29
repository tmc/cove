# cove ROADMAP

**Status**: living document. Updated as items ship or scope changes.
**Horizon**: v0.1.2 -> v1.0

This document is the single source of truth for cove's planned work. It does not
duplicate the design docs: each item links to the design doc that owns it. When
an item ships, mark its row `done` and leave the row in place.

## Status legend

- **must**: required for the version to ship as a coherent release
- **should**: high value, target the version but do not block on it
- **maybe**: surfaced for awareness, may slip
- **done**: shipped, kept for provenance

## v0.1.2 - Reliability & Stale-State Cleanup

| Item | Priority | Depends on | Source | Why |
|---|---|---|---|---|
| `cove up` fresh-install path-resolution fix | done | none | [roadmap-post-v0.1](../roadmap-post-v0.1.md), `fix/cove-up-fresh-install` | Fixes the headline UX bug where install reported success but provisioning failed because the target VM directory was never materialized. |
| CDC vs fixed-offset chunking trade-off study | done | none | [002](002-cove-disks-oci.md) | Settles whether content-defined chunking is worth the cost before committing the v0.2 store design. |
| `cove doctor` TCC/FDA probe | done | none | [TCC research](../research/tcc-via-user-agent.md) | Triggers/diagnoses Full Disk Access state before VirtioFS access silently fails. |
| Verify 008 codebase-cleanup status | done | none | [008](008-codebase-cleanup-plan.md) | Confirms which cleanup phases already landed and gates v0.2 work. |

## v0.2 - Linux Workstation + Foundation Cleanup

| Item | Priority | Depends on | Source | Why |
|---|---|---|---|---|
| Local content-addressed store at `~/.vz/store/` + GC | done | none | [002](002-cove-disks-oci.md) | Required foundation for resumable pulls and `cove build`. |
| Nested KVM on M3/M4 | done | none | [006](006-cove-linux-v02.md) | Enables minikube/k3d-class Linux developer workflows on supported Apple Silicon hosts. |
| Turnkey Linux distros (Ubuntu, Debian, Fedora, Alpine) | done | none | [006](006-cove-linux-v02.md) | Provides reliable unattended installers, including fast Alpine setup. |
| Linux shell unary RPCs (`ResizeExecTTY`, `SignalExec`, `SetTime`) | done | none | [006](006-cove-linux-v02.md) | Keeps terminal control proxy-safe and per-call authenticated. |
| VirtioFS UID/GID auto-mapping + Rosetta-by-default | done | none | [006](006-cove-linux-v02.md) | Removes manual chmod and Rosetta setup toil for Linux guests. |
| ControlServer decomposition - phases 1+2 | done | none | [008](008-codebase-cleanup-plan.md) | Moves agent and shared-folder configuration seams out of the package-main god object. |
| DHCP lease exhaustion warnings | done | none | gap vs tart DHCP lease handling | Warns high-throughput fork users before macOS VM networking degrades from long DHCP leases. |
| USPTO trademark search for "cove" | done | none | product/legal hygiene | Finds live/pending software-class COVE conflicts before public registry work. |

## v0.3 - `cove build` + Caching + Agent Adapters

| Item | Priority | Depends on | Source | Why |
|---|---|---|---|---|
| `cove build` with content-addressed cache keys | must | v0.2 store | [003](003-cove-build-oci-caching.md) | Dry-run cache-key planning, block-delta primitives, delta blob storage, and build-cache metadata have landed. VM execution remains intentionally gated behind `--dry-run`. |
| Secrets via tmpfs (`# secret:` directive) with guest swap disabled | must | none | prior roadmap | Prevents secret leakage into pushed OCI block diffs. |
| Agent-aware `cove compact` | must | none | [002](002-cove-disks-oci.md) | Zeroes free space before diffing and pushing images. |
| ControlServer decomposition - phase 3 (`internal/control`) | should | v0.2 phases 1+2 | [008](008-codebase-cleanup-plan.md) | Completes the cleanup arc started in v0.2. |
| Anthropic sandbox-runtime adapter | should | none | prior roadmap | Expands agent integrations beyond the OpenAI Agents SDK adapter. |
| Curated agentkit base images | should | v0.2 store + `cove build` | prior roadmap | Prepares the v1.0 public registry story. |
| Packer plugin shim decision | maybe | none | gap vs tart Packer integration | Decide whether a shim accelerates adoption or distracts from the `cove build` moat. |

## Product Decisions

- `cove` is not clean for public software/registry branding based on the preliminary USPTO search. Do not take a public v1 registry out under this name without trademark counsel or a rename plan. See [docs/research/trademark-cove.md](../research/trademark-cove.md).

## Wedges to protect

- Named multi-snapshot fork lineage via APFS clonefile.
- Pure Go via purego.
- vsock+gRPC guest control, not SSH as the canonical path.
- Native AppKit GUI, not browser VNC.
- Hard isolation via VM fork/restore; soft reset is not an isolation primitive.

## Recent changes

- **2026-04-29**: Rebased and integrated the v0.1.2, v0.2, and early v0.3 branch work onto main.
- **2026-04-29**: Landed `cove build` dry-run cache planning and kept execution gated until the VM build path is implemented.
- **2026-04-29**: Recorded preliminary USPTO trademark screen; `cove` needs legal/product decision before public registry use.
