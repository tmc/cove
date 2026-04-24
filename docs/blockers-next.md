# Blockers deferred from 0.1 to `next`

Two items were deferred from the cove 0.1 ship gate on 2026-04-24 because
empirical smoke testing turned up reproducible bugs. This doc is the
handoff: reproduction, evidence, and root-cause hypotheses. Full
smoke-test context lives in `docs/ship-audit-2026-04-24.md`.

## #1 — `cove up` fresh install produces no disk

**Symptom.** `cove up -user <name> -vm <name> -ipsw <ipsw> -headless`
reports install 100%, then fails at provision stage because the VM
directory does not exist.

**Reproduction.**
```
./cove up -user smoketest -password smokepass123 -vm smoketest-vm \
  -ipsw ~/.vz/cache/RestoreImage.ipsw -headless -disk-size 48 -no-shutdown
```

**Observed output (abbreviated).**
```
=== Step 1/3: Installing macOS ===
…  0.0% … 100.0%
=== Installation Complete ===

Stopping VM...
VM stopped.
warning: disk not found after VM stop: /Users/tmc/.vz/vms/smoketest-vm/disk.img
  vmDir=/Users/tmc/.vz/vms/smoketest-vm
  cannot list vmDir: open …/smoketest-vm: no such file or directory

Provisioning VM disk...
warning: disk provisioning failed: disk image not found: …/smoketest-vm/disk.img

=== Step 2/3: Provisioning VM ===
error: stage provisioning: disk image not found: …/smoketest-vm/disk.img
```

**Post-condition.** `/Users/tmc/.vz/vms/smoketest-vm/` never exists on
disk. `find /Users/tmc/.vz -name 'smoketest*'` returns nothing.

**Hypotheses (pick one when working this).**

1. **Path-resolution mismatch between install target and post-install
   stop+inject.** Existing VMs on the machine live at `~/.vz/<name>/`
   (e.g. `~/.vz/hermes-mlx-go-60g-v10/disk.img`), but the post-install
   warning resolves `target.Directory` to `~/.vz/vms/<name>/`. Install
   may have written to one path, `stopVMAndInject` stats another.
2. **Install materialized disk in a temp dir that was cleaned up by the
   installer completion handler before provisioning ran.** Install
   reports 100% but leaves nothing on disk under the expected name.
3. **`applyUpConfig` / `currentVMSelection` drift.** Globals set by
   `up.go` for install may not agree with the `VMTarget` read by
   `stopVMAndInject` and `stageProvisioningFilesForVM`.

**Relevant code.**
- `up.go:196-217` — `applyUpConfig` sets globals `provisionStrategy="disk"`,
  `SkipSetupAssistant=true`, `AutoLogin=true`, writes `provisionUser`/
  `provisionPassword`.
- `up.go:238-264` — `runUpPipeline` orchestrates install → inject → run,
  skips install if `vmAlreadyInstalled(target.Directory)`.
- `installer.go:161-187` — `stopVMAndInject` stats `target.diskPath()`
  post-stop and prints the warning above.
- `installer.go:321-376` — `stopInstallerVM` is non-destructive; does not
  remove files.
- `vmconfig` / `VMTarget.Directory` resolution — likely where the divergence
  lives.

**TODO.**
- [ ] Add a pre-install log line that prints the full resolved
  `target.Directory` and the disk path the installer is about to write to.
- [ ] Add an equivalent log line in `stopVMAndInject` before the stat.
- [ ] Reproduce with `VZ_DEBUG_INSTALL=1` to dump the install path.
- [ ] If install and post-install resolve to different paths, unify at
  a single resolver in `vmconfig`.
- [ ] After the root cause is fixed, re-run the smoke from
  `docs/ship-audit-2026-04-24.md` and flip item #1 to PASS.

## #6 — `cove pull` does not accept upstream lume images

**Symptom.** `cove pull` of any `ghcr.io/trycua/*` image fails at
manifest-parse before any blob fetch:

```
$ ./cove pull --dry-run --as lume-smoke \
    ghcr.io/trycua/ubuntu-noble-vanilla:latest
error: parse registry manifest: parse manifest:
  missing annotation org.tmc.cove.uncompressed-disk-size or
  org.trycua.lume.uncompressed-disk-size
```

**Root cause.** Lume's public ghcr.io images use a schema that the
`coveToLume`/`lumeToCove` map in `internal/ociimage/annotations.go:22-50`
does not cover. Observed against
`ghcr.io/trycua/ubuntu-noble-vanilla:latest` and
`ghcr.io/trycua/macos-sequoia-vanilla:latest`:

- Manifest-level annotations: only `org.opencontainers.image.created`.
  No `org.trycua.lume.uncompressed-disk-size`, no `hw-model-digest`, no
  `aux-digest`, no lume- or cove-namespaced keys at all.
- Layer mediaType:
  `application/vnd.oci.image.layer.v1.tar;part.number=N;part.total=41` —
  a parameterised tar split, not LZ4-compressed chunks.
- Layer annotations: only `org.opencontainers.image.title` set to
  `disk.img.part.aa` … `disk.img.part.bo`, `nvram.bin`, `config.json`.
- No `chunk-index`/`chunk-total`/`uncompressed-size`/`role` anywhere.

Cove's pull path requires `CoveUncompressedDiskSize` at manifest level
(`annotations.go:75-78`) and per-layer `CoveUncompressedSize`,
`CoveChunkIndex`, `CoveChunkTotal`, `CoveRole` (`annotations.go:97-121`,
`pull.go:322-336`). None of these are present in lume's images.

**Schema differences that make "add a few more aliases" insufficient.**

1. **Disk layout.** Lume ships `disk.img` split into named 500 MB `tar`
   parts reassembled by filename; cove expects LZ4-compressed, sha256-
   verified chunks addressed by `chunk-index`/`chunk-total` annotations
   and written at computed offsets via `WriteCompressedChunkAt`.
   Different compression, different addressing, different verification.
2. **Identity metadata.** Lume ships `nvram.bin` and `config.json` as
   separate layers keyed by `image.title`; cove keys them by `role`
   annotation (`nvram`/`hw-model`/`machine-id`). Lume has no `hw-model`
   or `machine-id` blob — macOS hardware identity lives inside
   `config.json`, which cove does not parse.
3. **No size/digest hints.** Without `uncompressed-size` and
   `uncompressed-content-digest` on each layer, cove cannot preallocate
   `disk.img.partial` or verify final output.

**TODO.**
- [ ] Decide scope: do we want 1-way lume-import, or full interop?
- [ ] If 1-way import: add a second `pull` code path keyed on
      `mediaType` containing `;part.number=`/`;part.total=` and
      `image.title` matching `disk.img.part.*` / `nvram.bin` /
      `config.json`. Concat parts by sort-order of `part.aa`…`part.bo`,
      write to `disk.img` atomically.
- [ ] Parse `config.json` (lume format) for macOS hardware identity.
      Emit cove's `hw-model` / `machine-id` equivalents at pull time so
      the VM can boot with VZ's platform config.
- [ ] Smoke-test against `ghcr.io/trycua/ubuntu-noble-vanilla:latest`
      (Linux — simpler: no hw-model) and
      `ghcr.io/trycua/macos-sequoia-vanilla:latest` (macOS — needs hw
      identity extraction).
- [ ] After landing, flip item #6 to PASS and re-include it in 0.1
      scope or confirm deferral to 0.2.

**Minor DX wart seen during smoke:** `./cove pull docker://…` fails with
`reference must not include a URL scheme`. Lume docs commonly show
`docker://`-prefixed refs. Not a correctness issue — treat as a
ref-parser improvement while in this area.
