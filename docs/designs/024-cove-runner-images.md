# cove runner images: publish & fork-from

**Status**: Slice 1 shipped (`8a106dc`, 2026-05-02; 1027 LOC, 8 tests
green). Slice 2 (push/pull, private registries only) targets v0.4. Slice
3 (drop public-registry refusal + cosign) is **deferred indefinitely**
per user 2026-05-02 — cove repo stays private; revisit only when the
user explicitly requests a public flip.
**Source**: notebook scope-B verdict; the user prompt "i think cirrus
publishes images to use as github runners, can we do the same?". Scope A
(immediate vzscript-based runner provisioning) shipped at
[`7c315fc`](../../vzscripts/github-runner.vzscript) as
`T-GHA-RUNNER #90`. This doc covers Scope B (publishable images).
**Roadmap**: v0.2.1 (Slice 1) → v0.4 (Slices 2–3).
**Branch**: planning.

## Goal

cove publishes VM disk images (macOS + Linux) as OCI artifacts that
users pull and fork-from to spawn ephemeral self-hosted CI runners. The
wedge is `cove run -fork-from` semantics ([013](013-vm-fork.md)) plus
the soft-reset isolation result ([015](015-soft-reset-empirical.md)) —
not "we ship prebaked images too". Cirrus tart's image catalogue is
the prior art; cove's contribution is the fork primitive underneath
and the empirical isolation story on top.

## Non-goals

- Reimplementing tart's CLI surface 1:1.
- Multi-tenant registry hosting on cove's own infra.
- Anything that requires the cove repo to be public *before* the user
  flips it.
- Replacing T-GHA-RUNNER. The
  [`vzscripts/github-runner.vzscript`](../../vzscripts/github-runner.vzscript)
  recipe stays as the manual-provisioning path. 024 makes it optional.

## User flow (target)

```text
cove image build -from <vm-name> -tag cove-runner-macos:14.5
cove image push   cove-runner-macos:14.5 [registry]   # gated
cove image pull   cove-runner-macos:14.5
cove run -fork-from cove-runner-macos:14.5 -ephemeral -vzscripts github-runner
```

- `cove image build` snapshots a stopped local VM bundle into the local
  image store as an OCI artifact.
- `cove image push` uploads to a registry. Refused against public
  registries while the cove repo is private (see Security).
- `cove image pull` materializes a local image-store entry from a
  registry.
- `cove run -fork-from <image-ref>` resolves the ref to a local image,
  realizes a scratch VM bundle, and reuses the existing fork-from
  codepath. `-ephemeral` (new flag) discards the child after first stop.

## Differentiator vs Cirrus tart

- Fork-from is APFS clonefile (COW) per [013](013-vm-fork.md) Phase 3
  ([`99b3732`](../../)). The
  [`bench/fork-time/`](../../bench/fork-time/README.md) harness records
  ~140 ms fork-only on a 60 GB parent on M4 hardware. tart pulls a full
  image per job; cove forks an already-pulled image in-place.
- The soft-reset matrix in [015](015-soft-reset-empirical.md) recorded
  0 pass / 3 fail / 3 limit on warm-guest reset isolation. That result
  is empirical, not marketing — fork/restore is *required* for
  isolation against token leakage and secrets cross-contamination.
- The vz-agent gRPC over vsock ([006](006-cove-linux-v02.md),
  [023](023-cove-shell-exec-ux.md)) means CI jobs exec without
  re-entering SSH. tart shells in.
- The control-socket multiplex ([023](023-cove-shell-exec-ux.md) Option
  B) supports multi-client coordination without re-auth. The runner
  shim and an interactive operator can attach to the same job.

## Architecture

### Image format

- OCI artifact, mediaType `application/vnd.cove.vm.disk.v1+raw`
  (proposed). cove claims its own mediaType rather than reusing
  tart's; interop is a non-goal for v0.2.1.
- Layers contain the VM bundle subset that fork-from needs:
  `machine.id`, hardware-model, aux storage, MAC, disk image. The
  vmstate/`suspend.vmstate` file is *excluded* — vmstate binds to
  `{machine.id, aux, MAC, disk}` per
  [013](013-vm-fork.md) Phase 4 / Phase 5 finding (project memory
  `project_a1_snapshot_fidelity`), so shipping it across hosts is
  brittle. Cold-boot only in v0.2.1.
- Disk layer is sparse + zstd-compressed. Size budget instrumented in
  Slice 1 against a 60 GB parent.

### Build pipeline (`cove image build`)

1. Source: a stopped, registered cove VM at
   `vmconfig.BaseDir()/<name>/`.
2. APFS clonefile the disk into a staging dir (same primitive as
   `cove fork`, [013](013-vm-fork.md) Phase 3, `99b3732`).
3. Tar the bundle subset, zstd-compress.
4. Wrap in an OCI manifest with the cove mediaType.
5. Optionally cosign-sign locally if `cosign` is on PATH (Slice 1: not
   required; Slice 3: required for public push).
6. Output: `~/.vz/images/<repo>/<tag>` plus content-addressed blobs.

### Pull / push (`cove image push|pull`)

- Backed by `oras-go` or `quay/distribution-spec`. Dependency choice
  deferred to Slice 1 implementation; document the trade in the PR.
- Auth: reuse `~/.docker/config.json` so users get the same UX as
  `docker pull <ghcr.io/...>`.
- **Privacy gate**: `cove image push` refuses with a clear error in
  the v0.2.1 / v0.3 cycle if the target registry hostname is public
  (initial list: `ghcr.io`, `docker.io`, `quay.io`,
  `registry.gitlab.com`) and the cove repo
  ([`tmc/cove`](https://github.com/tmc/cove)) is still private. Override
  `COVE_ALLOW_PUBLIC_PUSH=1` for explicit operator opt-in. The hard
  refusal goes away in Slice 3 once the repo flips public.

### Fork from a pulled image

`cove run -fork-from <image-ref>` resolves `<image-ref>` against the
local image store, materializes a fresh `vmconfig.BaseDir()/<child>/`
bundle from the cached layers, then invokes the existing
[`-fork-from`](../../fork.go) codepath. The new `-ephemeral` flag marks
the child for destroy-on-stop and skips the registry/`vm tree` entry
that [013](013-vm-fork.md) Phase 4 added.

## Security

This section is load-bearing. See
[015](015-soft-reset-empirical.md) for the empirical basis.

- The 015 matrix recorded 0 pass / 3 fail / 3 limit on warm-guest
  isolation probes. Fails: System Keychain residue, GlobalPreferences
  leakage, orphaned LaunchDaemon residue. Conclusion: fork/restore is
  the only supported isolation primitive.
- Threat model for runner images: token leakage between consecutive
  CI jobs, residue in `/Library/Keychains/System.keychain`, shared
  `$TMPDIR` cross-contamination, orphaned LaunchDaemons left by a
  previous job.
- Mitigation: every CI job spawns a fresh fork via
  `cove run -fork-from -ephemeral`. The fork is destroyed at job end.
  Soft-reset is *never* the path for runners; the runner shim must
  refuse to reuse a parent VM directly.
- Image signing: cosign sign/verify is optional in Slice 1, required
  for public push in Slice 3. Slice 1 stores images locally only and
  signing is not a gate.
- Public-push gate: documented above. Until the cove repo flips
  public, no public-registry pushes — even with a valid auth token.
  This is a hard refusal in code, not a doc-only convention.

## Slices

- **Slice 1 (v0.2.1, ~400 LOC budget; shipped at 1027 LOC, no proto bump)**: `cove image build` +
  local image store + `cove run -fork-from <local-image-ref>` +
  `-ephemeral`. No push/pull. No public-registry interaction. Ships
  the core wedge under privacy gate (iii) in the notebook decision —
  cove repo can stay private through this slice. **SHIPPED `8a106dc` 2026-05-02**.
  Image store at `~/.vz/images/<name>/<tag>/` (chosen over an early
  `.cove/`-rooted proposal to match `vmconfig.BaseDir`'s
  `~/.vz/vms/`). Excludes
  `suspend.vmstate` per the identity-binding rule. Cold-boot only.
  ParentImage on child config.json gates `cove image rm` from deleting
  while live forks reference the image. 8 tests cover parse, build,
  list, materialize-with-fresh-identity, fork-from-image, delete-while-fork-live.
- **Slice 2 (v0.4, ~300 LOC)**: `cove image push|pull`. OCI artifact
  wire format finalized. `oras-go` (or distribution-spec) integration.
  Docker auth reuse. Public-registry refusal still active while cove
  is private.
- **Slice 3 (DEFERRED INDEFINITELY, ~150 LOC, public-flip dependent)**:
  drop the public-registry refusal once the user confirms the repo is
  public. Add cosign sign/verify as a default. Document the promotion
  path to `ghcr.io/tmc/cove-runner-image:<tag>`. Strict dependency on
  the user-driven public flip; this slice does not start before then.
  Per user 2026-05-02: stay private for now; this slice has no target
  release.

## Open questions

1. **Registry client**: `oras-go` or `quay/distribution-spec`?
   Deferred to Slice 1 implementation; PR records the choice.
2. **mediaType**: claim our own
   (`application/vnd.cove.vm.disk.v1+raw`) or piggyback on tart's?
   Strawman: claim our own. Interop is a non-goal.
3. **Layer model**: incremental delta-from-parent layers, or one
   monolithic blob per tag? Strawman: monolithic in Slice 1, deltas
   in Slice 2 only if size budget demands.
4. **Where does T-GHA-RUNNER live once images ship**? Strawman: keep
   the recipe as the bake-step inside `cove image build` workflows
   (so the published image already has the runner agent installed),
   and as the manual path for users not consuming images.

## References

- [013](013-vm-fork.md) Phase 3 (`99b3732`), Phase 4 (`eacbf5e`) —
  fork-from semantics, `vm tree`.
- [015](015-soft-reset-empirical.md) — soft-reset empirical
  (load-bearing for Security).
- [021](021-v04-ci-executors-tracks.md) — v0.4 CI executors. 024 is
  the image half of 021's surface; the GHA action consumes images
  built by 024.
- [023](023-cove-shell-exec-ux.md) — cove shell exec UX. 024 sits
  adjacent; runner-image jobs use the same control-socket and agent
  paths.
- [`vzscripts/github-runner.vzscript`](../../vzscripts/github-runner.vzscript)
  (`7c315fc`, T-GHA-RUNNER) — manual provisioning path that 024
  optionally bakes into images.
- [`bench/fork-time/README.md`](../../bench/fork-time/README.md) —
  fork-time baseline used in the differentiator section.
