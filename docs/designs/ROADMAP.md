# cove ROADMAP

**Status**: living document. Updated as items ship or scope changes.
**Horizon**: v0.1.2 -> v1.0

This document is the single source of truth for cove's planned work. It does not
duplicate the design docs: each item links to the design doc that owns it. When
an item ships, mark its row `done` and leave the row in place.

## Strategy source

This roadmap is the post-integration, post-review rollup of the notebook-backed
strategy in [012](archive/012-product-roadmap-2026.md), the v0.1 handoff in
[014](archive/014-roadmap-update-post-v0.1.md), the soft-reset empirical result in
[015](015-soft-reset-empirical.md), and the post-integration NotebookLM refresh
in [016](016-notebooklm-roadmap-refresh-2026-04-30.md). The v0.3 execution
slices are tracked in [017](017-v03-execution-roadmap.md). The 012, 016, and
017 sources used NotebookLM notebook `79a32e96-8e1c-4e89-9385-20193e3a8209` as
a sparring partner. Date-sensitive market, legal, license, pricing, and
competitor claims from that notebook stay research inputs, not release claims,
until they are reverified against primary sources.

The current product bet is narrower than "another macOS VM CLI": cove should be
the local, MIT-licensed Apple-Silicon macOS agent substrate with fork/restore,
vsock control, OCI-backed images, and agent adapters. The next work should
protect that wedge instead of chasing disconnected features.

## Status legend

- **must**: required for the version to ship as a coherent release
- **should**: high value, target the version but do not block on it
- **maybe**: surfaced for awareness, may slip
- **done**: shipped, kept for provenance

## Review decisions

- The prior implementation review findings are closed: malformed manifest
  digests now return validation errors, and `cove build` local-base execution
  now runs VM steps, skips cache-hit steps, persists metadata, and leaves
  pushable image state.
- Soft reset failed as an isolation boundary. Use fork/restore for
  privacy-critical evals; do not publish "thousands per hour" style soft-reset
  claims. See [015](015-soft-reset-empirical.md).
- `cove` is not clean for public software/registry branding based on the
  preliminary USPTO search. Public registry, signed image distribution, and
  product-name claims need a legal/product decision first.

## RC scope: what ships and what's deferred

This boundary is canonical and must agree with the CLI reference, changelog,
release checklist, [016](016-notebooklm-roadmap-refresh-2026-04-30.md), and
[017](017-v03-execution-roadmap.md).

**Ships in this RC.** `cove build` non-dry-run execution against a local VM
directory base (cache hits validate metadata and skip guest execution; misses
fork a scratch VM, run vzscript steps through the agent, and persist verified
layer manifests); `# secret:` tmpfs handling with guest swap disabled; build
pipeline compaction (`fast`, `targeted`, `thorough`); cache TTL and full
metadata validation before apply; published fork-only and boot-to-agent fork
benchmarks on named M4 hardware; OpenAI Agents SDK adapter v1 with live-smoke
and package-check documentation.

**Explicitly deferred.** Registry-base `cove build` execution; registry cache
import/export (`--cache-from`, `--cache-to`); public curated `cove` image
registry and signed agentkit image channels; external secret stores
(1Password, Vault, SOPS, age); BuildKit-style parallel step execution; Packer
plugin shim; product-name resolution before any public registry or signed
channel.

## v0.1.2 - Reliability & Stale-State Cleanup

| Item | Priority | Depends on | Source | Why |
|---|---|---|---|---|
| `cove up` fresh-install path-resolution fix | done | none | [roadmap-post-v0.1](archive/roadmap-post-v0.1.md), `03fb38f fix(up): resolve up target before install` | Fixes the headline UX bug where install reported success but provisioning failed because the target VM directory was never materialized. Shipped on `main` as `03fb38f`. |
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

## v0.2.1 - Shell + Image Surface (post-v0.2 polish)

| Item | Priority | Depends on | Source | Why |
|---|---|---|---|---|
| `cove shell <vm>` server-side commands (Slice 1) | done | v0.2 in-process `cove run -linux -shell` | [023](023-cove-shell-exec-ux.md) | Ships agent-exec-attach/-resize/-signal control-socket commands. No proto bump. Landed 17211bd. |
| `cove shell <vm>` standalone client (Slice 2) | done | Slice 1 server commands | [023](023-cove-shell-exec-ux.md) | Standalone client subcommand. Landed 33fbe7e (~303 LOC, 7 tests). Stdin /dev/null until Slice 3 v0.3 proto bump. |
| Local image store + `cove image build/list/rm` (Slice 1) | done | fork-from + APFS clonefile | [024](024-cove-runner-images.md) | Pre-baked, forkable VM images at ~/.vz/images/. Landed 8a106dc, 1027 LOC, 8 tests green. |
| `cove run -fork-from <local-image-ref>` + `-ephemeral` | done | image store Slice 1 | [024](024-cove-runner-images.md) | Wires image store into existing fork-from codepath so users can spawn disposable VMs from a saved baseline. Shipped with image Slice 1 at 8a106dc. |
| github-runner vzscript | done | none | gap vs Cirrus tart workflow | Self-hosted GHA runner inside a long-lived cove VM; primary billing-block escape hatch. |
| gitlab-runner vzscript | done | none | parity with github-runner | Same-shape recipe for GitLab CI projects. |
| tailscale vzscript | done | homebrew vzscript | gap for users wanting stable remote access | Brings up Tailscale daemon on guest with `--ssh`; idempotent. |

## v0.3 - `cove build` + Caching + Agent Adapters

| Item | Priority | Depends on | Source | Why |
|---|---|---|---|---|
| `cove build` VM execution path | done | v0.2 store + dry-run planner | [003](003-cove-build-oci-caching.md) | Local-base builds create scratch VMs, restore cache-hit layers, execute misses, persist metadata, and leave pushable image state. Registry-base execution remains deferred. |
| Secrets via tmpfs (`# secret:` directive) with guest swap disabled | done | build execution | [003](003-cove-build-oci-caching.md) | Prevents secret leakage into pushed OCI block diffs. |
| `cove build` compaction integration | done | build execution | [002](002-cove-disks-oci.md), [003](003-cove-build-oci-caching.md) | Wires `fast`, `targeted`, and `thorough` build compaction into the pipeline before diffing and pushing images. |
| Fork-only benchmark publication | done | existing fork support | [012](archive/012-product-roadmap-2026.md), [015](015-soft-reset-empirical.md), [bench result](../../bench/fork-time/results-20260427.md) | Published 132-140 ms stopped-VM fork measurements on the M4 smoke host after soft reset failed as the isolation primitive. |
| Boot-to-agent fork benchmark publication | done | existing fork support + reachable agent base image | [012](archive/012-product-roadmap-2026.md), [015](015-soft-reset-empirical.md), [bench result](../../bench/fork-time/results-agent-20260430.md) | Published the product-relevant time from fork command to agent reachability on named M4 Max hardware. |
| ControlServer decomposition - phase 3 (`internal/control`) | should | v0.2 phases 1+2 | [008](008-codebase-cleanup-plan.md) | Completes the cleanup arc started in v0.2. |
| OpenAI Agents SDK adapter v1 | done | fork/restore + control socket | [012](archive/012-product-roadmap-2026.md), [OpenAI example](../examples/openai-agents.md) | Proves the agent-substrate pitch with a fork-first local adapter under `adapters/openai-agents-python`. |
| OpenAI adapter release hardening | done | adapter v1 + boot-to-agent benchmark | [012](archive/012-product-roadmap-2026.md), [017](017-v03-execution-roadmap.md) | Added live smoke instructions, package checks, and fork-first example polish before treating the adapter as a release surface. |
| Anthropic sandbox-runtime adapter | should | OpenAI adapter lessons | [012](archive/012-product-roadmap-2026.md) | Expands agent integrations after the first adapter proves the shape. |
| Curated agentkit base images | should | build execution + trademark decision | [012](archive/012-product-roadmap-2026.md) | Prepares the v1.0 registry story without publishing under a blocked name. |
| Packer plugin shim decision | maybe | none | gap vs tart Packer integration | Decide whether a shim accelerates adoption or distracts from the `cove build` moat. |

## v0.3 implementation slices

The next implementation branches should stay directly based on `origin/main`
and should not stack on each other. Each slice should be reviewable by itself;
see [017](017-v03-execution-roadmap.md) for files, gates, and docs updates.

1. Build executor scaffold and scratch VM lifecycle, with non-dry-run still
   gated. See [018](018-v03-build-executor-scaffold.md).
2. Cache-hit materialization, so cached layers can apply without guest boot. See
   [019](019-v03-cache-hit-materialization.md).
3. Cache-miss VM execution, block diff production, metadata persistence, and
   the point where non-dry-run `cove build` becomes supported. See
   [020](020-v03-cache-miss-execution.md).
4. `# secret:` tmpfs handling with guest no-swap verification.
5. Build-pipeline compaction integration with `targeted` as the current default.
6. Boot-to-agent benchmark publication plus OpenAI adapter release hardening.

## v0.3 execution order

1. Finish the first three `cove build` slices before adding more planner surface
   area.
2. Add secret handling and compaction only after the build path has real VM
   state to protect and shrink.
3. Publish fork benchmarks and revise product language to measured numbers.
4. Harden the first agent adapter against fork/restore, not soft reset.
5. Defer agentkit image publication until the name/legal decision and build
   execution are both resolved.

## Validation gates

- `cove build`: cache hits skip guest execution; misses execute in a VM;
  metadata survives restart; pushed state does not contain secret material.
- Fork isolation: benchmark reports fork-only time and boot-to-agent time on
  named host hardware, with slower-than-target runs published instead of hidden.
- Agent adapter: a fresh install can run an Agents-SDK-compatible agent against
  a cove VM in five lines, using fork/restore for sensitive examples.
- Registry: no public `cove` registry or signed agentkit image channel ships
  until trademark counsel clears the name or a rename lands.
- External claims: any public post that cites competitor dates, partner lists,
  pricing, or license positioning must reverify those facts near publication.

## Product Decisions

- `cove` is not clean for public software/registry branding based on the preliminary USPTO search. Do not take a public v1 registry out under this name without trademark counsel or a rename plan. See [docs/research/trademark-cove.md](../research/trademark-cove.md).

## Wedges to protect

- Named multi-snapshot fork lineage via APFS clonefile.
- Pure Go via purego.
- vsock+gRPC guest control, not SSH as the canonical path.
- Native AppKit GUI, not browser VNC.
- Hard isolation via VM fork/restore; soft reset is not an isolation primitive.

## Recent changes

- **2026-05-04**: designs [027](027-disk-io-tuning.md) and [028](028-block-device-passthrough.md) shipped. Disk I/O tuning landed at `fc7ff1e` and `a459076`; block device passthrough landed across `b522ab3`, `a78e891`, `74d9527`, and `cede792` with follow-up hardening.
- **2026-05-02**: design [025](025-cove-action-security.md) cove-action security architecture landed at `1db4830` (411 LOC). Threat model + token lifecycle + isolation invariants for the v0.4 cove-action GHA wrapper. Clears the security-gate prerequisite on design [021](021-v04-ci-executors-tracks.md) Slice 1 implementation. Per user 2026-05-02: Slice 1 still v0.4-targeted; cove repo stays private; design [024](024-cove-runner-images.md) Slice 3 deferred indefinitely.
- **2026-05-02**: v0.2.1 Slice 1 implementations shipped: design [023](023-cove-shell-exec-ux.md) server-side at 17211bd (289 LOC, 7 tests); design [024](024-cove-runner-images.md) image surface at 8a106dc (1027 LOC, 8 tests). Slice 2 of 023 (standalone `cove shell <vm>` client, ~150 LOC) is the only remaining v0.2.1 implementation.
- **2026-05-02**: Added v0.2.1 milestone covering `cove shell <vm>` Slice 1 (design [023](023-cove-shell-exec-ux.md)), local image store + `cove image build/list/rm` Slice 1 (design [024](024-cove-runner-images.md)), and three CI/networking vzscripts (github-runner, gitlab-runner, tailscale).
- **2026-05-02**: Trimmed Buildkite track from v0.4 design [021](021-v04-ci-executors-tracks.md); v0.4 CI work now covers GHA + GitLab only.
- **2026-04-30**: Reconciled docs with branch reality for the RC: `cove build` local-base execution, `# secret:` tmpfs, build-pipeline compaction, fork benchmarks, and OpenAI adapter hardening are all marked landed; deferred-items boundary made canonical and consistent across CLI reference, changelog, ROADMAP, 016, 017, and the release checklist.
- **2026-04-30**: Re-reviewed the roadmap against the notebook-backed 012 strategy; made `cove build` execution, fork benchmarks, adapter proof, and trademark gating explicit.
- **2026-04-30**: Added the Slice 3 cache-miss execution plan and started the metadata persistence implementation.
- **2026-04-30**: Added the Slice 2 cache-hit materialization plan, including validation-before-scratch and failure-atomicity rules.
- **2026-04-30**: Added the Slice 1 build-executor scaffold plan, including scratch lifecycle tests and the side-effect-free dry-run rule.
- **2026-04-30**: Added the v0.3 execution-slice roadmap and corrected OpenAI adapter v1 status to done; remaining adapter work is release hardening.
- **2026-04-30**: Synced the post-integration repo state into NotebookLM and added the 016 refresh plus license/SLA reference docs.
- **2026-04-30**: Promoted the published fork-only benchmark to done and kept boot-to-agent timing as the remaining F1 measurement gate.
- **2026-04-30**: Clarified that `cove compact` has shipped; v0.3 still needs build-pipeline compaction integration.
- **2026-04-29**: Rebased and integrated the v0.1.2, v0.2, and early v0.3 branch work onto main.
- **2026-04-29**: Landed `cove build` dry-run cache planning and kept execution gated until the VM build path is implemented.
- **2026-04-29**: Recorded preliminary USPTO trademark screen; `cove` needs legal/product decision before public registry use.
