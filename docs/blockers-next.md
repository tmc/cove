# Deferred blocker audit

Two items were deferred from the cove 0.1 ship gate on 2026-04-24 because
empirical smoke testing turned up reproducible bugs. Full smoke-test context
lives in `docs/ship-audit-2026-04-24.md`.

Audit status on 2026-05-10 against `origin/main` `373d50c`:

- #1 is no longer the original no-disk blocker. The resolver drift and
  ghost-wipe failure modes are covered by `5d7eacb` (`up.go`,
  `installer.go`, `up_path_resolution_test.go`) and `706cf0d`
  (`disposable_test.go`, `installer_watch.go`). The remaining gate is a
  real clean-host fresh-install smoke, because `RELEASE-NOTES-v0.1.0.md`
  still records that it had not been completed after the fix.
- #6 is fixed in code for upstream lume tar-split pull. `ba6024c`
  (`internal/ociimage/lume.go`, `lume_pull.go`) added tar-split manifest
  detection and a separate importer, `c48a5c8` projects `config.json` into
  cove vmconfig while preserving `lume-config.json`, and `d5bf2fc` covers
  pull dispatch with httptest registry fixtures. `fc1cd2e` keeps the old
  `docker://` DX wart closed by accepting the transport prefix in
  `internal/ociimage.ParseReference`.

## #1 — `cove up` fresh install produces no disk

**Status.** Original code-level blocker resolved; clean-host smoke still open.

**Original symptom.** `cove up -user <name> -vm <name> -ipsw <ipsw> -headless`
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

**Current code evidence.**

- `5d7eacb fix(up): instrument resolver flow + path-resolution regression
  test` added `up_path_resolution_test.go`, which asserts `parseUpFlags`,
  `applyUpConfig`, `currentVMSelection`, and `target.diskPath()` agree for
  fresh named VMs, fresh default VMs, and legacy `~/.vz/<name>` VMs.
- `installer.go` now logs `target.Directory`, `target.Name`, global
  `vmDir`/`vmName`, post-stop `diskFile`, parent entries, and post-install
  `vmDir`/`disk.img` state under `VZ_DEBUG_INSTALL=1`.
- `706cf0d fix(test): stop wiping the real ~/.vz/vms during disposable
  tests` removed the test-side `os.RemoveAll(vmconfig.BaseDir())` hazard
  that release notes identify as the actual root cause of the no-disk smoke.
- `installer_watch.go` adds a `VZ_DEBUG_INSTALL` fsnotify watcher for
  remove/rename/create events on `vmDir` and its parent during install.

**Next slice.**

- [ ] Run one clean-host fresh install with `VZ_DEBUG_INSTALL=1`:
  `./cove up -user smoketest -password smokepass123 -vm smoketest-vm
  -ipsw ~/.vz/cache/RestoreImage.ipsw -headless -disk-size 48
  -no-shutdown`.
- [ ] Record the full install path trace and post-condition in this doc or a
  dated smoke artifact.
- [ ] If it reaches the provisioned desktop and leaves
  `~/.vz/vms/smoketest-vm/disk.img`, flip #1 to PASS. If it fails, use the
  new resolver trace and watcher events as the next root-cause artifact.

## #6 — `cove pull` does not accept upstream lume images

**Status.** Fixed for upstream lume tar-split imports on current `origin/main`.

**Original symptom.** `cove pull` of any `ghcr.io/trycua/*` image failed at
manifest parse before any blob fetch:

```
$ ./cove pull --dry-run --as lume-smoke \
    ghcr.io/trycua/ubuntu-noble-vanilla:latest
error: parse registry manifest: parse manifest:
  missing annotation org.tmc.cove.uncompressed-disk-size or
  org.trycua.lume.uncompressed-disk-size
```

**Current code evidence.**

- `ba6024c ociimage: detect lume tar-split manifests and route to a separate
  importer` added `internal/ociimage/lume.go`. It detects layers with
  `application/vnd.oci.image.layer.v1.tar;part.number=...` or
  `org.opencontainers.image.title=disk.img.part.*`, extracts `nvram.bin` and
  `config.json`, and dispatches `ParseManifest` to `FormatLume`.
- `lume_pull.go` streams the sorted disk parts into `disk.img.partial`, renames
  atomically to `disk.img`, preserves sidecars, writes provenance, and maps
  lume CPU/memory config into cove vmconfig.
- `d5bf2fc pull: cover FormatLume vs FormatCove dispatch with httptest
  registry` added `TestPullDispatch_LumeManifest`, which uses the original
  blocker schema: only `org.opencontainers.image.created` at manifest level,
  tar-split disk layers, and `image.title` sidecars.
- `integration_oci_test.go` also drives a full pull dispatch path for related
  registry formats; `lume_pull_test.go` covers config projection.
- `fc1cd2e` accepts Lume-style `docker://ghcr.io/...` refs in
  `internal/ociimage.ParseReference`.

**Remaining validation.**

- [ ] Run one network smoke against
  `docker://ghcr.io/trycua/ubuntu-noble-vanilla:latest` and, if available to
  the operator, `docker://ghcr.io/trycua/macos-sequoia-vanilla:latest`.
  The current code has fixture-backed parser/import coverage, not a fresh
  live GHCR pull recorded in this doc.
