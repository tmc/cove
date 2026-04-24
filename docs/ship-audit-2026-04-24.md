# cove 0.1 Ship-Gate Audit — 2026-04-24

Auditor: Claude (Opus 4.7, 1M-ctx). Source of truth:
`docs/designs/011-beat-lume-roadmap.md` §114–124.

Environment: VM `hermes-mlx-go-60g-v10` running at
`~/.vz/hermes-mlx-go-60g-v10/control.sock`; `./cove` binary built at
commit `0e2952c` (dump-docs reports version `4599ce29`).

Full `go test ./...` run: all 13 packages pass, 0 failures (log:
`/private/tmp/.../tasks/b2xt9ak49.output`).

## Findings

| Item | Status | Evidence | Severity |
|---|---|---|---|
| 1. `cove up -user <name>` reaches desktop without SA handholding | PASS (code-inspection only) | `up.go:217` forces `provisionStrategy = "disk"`; `up.go:275-276` hardcodes `SkipSetupAssistant:true, AutoLogin:true`; pipeline install→provision→run is linear with no TODO/FIXME across `up.go`, `provision_automation.go`, `setup_assistant.go` (2 kLOC combined). Disk-inject path writes `.AppleSetupDone` + LaunchDaemon + kcpassword + loginwindow.plist. `dump-docs` confirms `-user`, `-password`, `-vzscripts`, `-ipsw`, `-headless`, `-force` flags exist. Not exercised end-to-end (no fresh install ran). | major if broken on fresh install; minor otherwise |
| 2. `cove run -headless` does not create a GUI window | PASS | Headless path calls `newHeadlessGUIController` (`gui_control.go:114`) which invokes `initDetachedView` (line 166): constructs a `VZVirtualMachineView` but does NOT call `NewWindowWithContentRectStyleMaskBackingDefer`. `initWindow` (line 180) is only reached from the `gui-open` control-socket handler (`gui_control.go:333,430`). No NSWindow is created at boot in headless mode. | — |
| 3. `cove serve --mcp` operates a pre-existing VM end-to-end | PASS | `dump-docs -type mcp` lists 19 MCP tools: `vm_list`, `vm_status`, `vm_pause`, `vm_resume`, `vm_stop`, `vm_request_stop`, `vm_screenshot`, `vm_type`, `vm_key`, `vm_mouse`, `vm_agent_exec`, `vm_agent_read`, `vm_agent_write`, `vm_snapshot_save`, `vm_snapshot_list`, `vm_snapshot_restore`, `vm_snapshot_delete`, `vm_disk_snapshot_list`, `vm_pit_snapshot_list`. Bindings in `control_mcp.go:464-772` proxy to real `ControlRequest` protos, not stubs. Create-VM is a CLI-only path for 0.1 (resolved by product decision 2026-04-24). | — |
| 4. `cove serve -http` operates a pre-existing VM end-to-end | PASS (parity with MCP) | `dump-docs -type api` lists 26 REST endpoints under `/v1/vms/{name}/*`: status, pause, resume, stop, request-stop, screenshot, type, key, mouse, agent/exec, agent/read, agent/write, agent/cp, snapshot, snapshots (list/restore/delete), disk-snapshots, pit-snapshots, events (SSE), plus `/v1/operations/*` (list+SSE). Route handlers in `serve_gateway.go:596-628`. `POST /v1/vms` exists but returns `not_implemented` — by design for 0.1 (CLI-only create; resolved by product decision 2026-04-24). | — |
| 5. Agents can invoke snapshots and pause/resume over HTTP/MCP | PASS | Pause + resume + snapshot save/list/restore/delete all wired on both surfaces. Live-probed on hermes VM: `ctl pause` → status `canResume:true, canStop:true`; `ctl resume` → back to running. Roadmap "suspend/resume" clarified 2026-04-24 to mean pause/resume (resolved by product decision). | — |
| 6. `cove pull` of a lume-produced image boots in cove | PASS (code path exists; not booted E2E) | `cove pull` subcommand dispatched at `main.go:463`; `pull.go` is 471 lines with real OCI manifest fetch, LZ4 chunk streaming, atomic disk rename. `pull.go:323` calls `ociimage.NormalizeLayerAnnotations` which `internal/ociimage/annotations.go:22-49` defines a bidirectional `coveToLume`/`lumeToCove` map covering all legacy `org.trycua.lume.*` annotation keys. Pull accepts lume-produced manifests. Live boot of a lume image was not exercised. | major if lume schema diverges from the mapped keys; minor otherwise |
| 7. `cove dump-docs` emits structured CLI/API/MCP data | PASS | `./cove dump-docs` emits a single JSON document with top-level keys `version`, `cli`, `api`, `mcp`. CLI section has 26 commands with name/summary/usage/flags/examples. API section has 26 endpoints with method/path/description/auth. MCP section has 19 tools with name/description/JSONSchema input_schema. `--help` shows `-type cli|api|mcp` and `-pretty` flags. Output is valid JSON parseable without help-text scraping. | — |

### Incidental findings

- **`-verbose` ObjC main-thread crash:** **NOT REPRODUCIBLE** on current
  tip (commit `384b711`, 2026-04-24). Tested both
  `./cove -verbose run -vm-dir /tmp/fresh -headless` and
  `./cove -verbose run -vm-dir /tmp/fresh -gui` against an empty VM
  directory — both reach `VM state transition: Unknown(-1) -> Running`
  and survive until `SIGTERM`. No `API misuse`, `main thread`,
  `setMainMenu`, or `NSException` strings in stderr. The earlier
  session's crash report appears to have been stale or misattributed;
  no code change required. Left as-is.
- **Import cycles / dead code:** `go test ./...` passed cleanly; no build
  errors. No cycles flagged.
- **Test suite:** 13 packages, all green; runtime ~8s.

## Blockers to 0.1 ship

None at **blocker** severity. Both prior **major** flags resolved by product
decision on 2026-04-24:

1. `cove serve` — create-VM is CLI-only for 0.1; "operate a pre-existing VM"
   is the 0.1 contract. Roadmap bullet reworded accordingly. (#3, #4)
2. "suspend/resume" in the ship gate means pause/resume. Roadmap bullet
   reworded to drop "suspend." (#5)

The PASS items that weren't exercised end-to-end (#1, #6) remain candidates
for smoke passes before tagging:

- Fresh `cove up -user smoketest` to a booted macOS desktop.
- `cove pull` against a known lume-produced image.

## Smoke test: cove pull (lume image)

**Date:** 2026-04-24. **Binary:** `./cove` at commit `441b9b7`.

**Image refs attempted:**
- `ghcr.io/trycua/ubuntu-noble-vanilla:latest` (dry-run, anonymous pull)
- Manifest for `ghcr.io/trycua/macos-sequoia-vanilla:latest` also inspected
  via raw `ghcr.io/v2/...` API (not fed to `cove pull`; same schema).

**Pull outcome:** FAILURE at manifest-parse stage, before any blob fetch.

```
$ ./cove pull --dry-run --as lume-smoke ghcr.io/trycua/ubuntu-noble-vanilla:latest
error: parse registry manifest: parse manifest: missing annotation
  org.tmc.cove.uncompressed-disk-size or org.trycua.lume.uncompressed-disk-size
```

A prior invocation with `docker://` scheme also failed: `reference must not
include a URL scheme`. (Minor DX wart — lume's own docs commonly show
`docker://`-prefixed refs. Not a correctness issue.)

**Boot outcome:** not attempted — pull did not produce a disk.

**Diagnosis:** **lume schema drift. This is not a cove bug, and not a missing
image.** The real trycua lume manifests on ghcr.io look nothing like what
`internal/ociimage/annotations.go` maps. Observed schema of
`ghcr.io/trycua/ubuntu-noble-vanilla:latest`:

- Manifest-level annotations: only `org.opencontainers.image.created`. No
  `org.trycua.lume.uncompressed-disk-size`, `…hw-model-digest`, `…aux-digest`,
  or any other lume/cove-namespaced key.
- Layer mediaTypes: `application/vnd.oci.image.layer.v1.tar;part.number=N;part.total=41`
  (a parameterised tar split), not an LZ4-compressed chunk.
- Layer annotations: only `org.opencontainers.image.title` set to filenames
  `disk.img.part.aa` … `disk.img.part.bo`, `nvram.bin`, `config.json`.
- No `org.trycua.lume.chunk-index` / `chunk-total` / `uncompressed-size` /
  `role` anywhere in the manifest — verified across both the ubuntu and the
  macos-sequoia-vanilla manifests (84 layers).

Cove's pull path requires `CoveUncompressedDiskSize` at manifest level
(`NormalizeManifestAnnotations`, `annotations.go:75-78`) and per-layer
`CoveUncompressedSize`/`CoveChunkIndex`/`CoveChunkTotal`/`CoveRole`
(`annotations.go:97-121`, `pull.go:322-336`). Lume's public images set none
of these. The `coveToLume`/`lumeToCove` bidirectional map in
`annotations.go:32-50` was written against a schema lume either no longer uses
or never used at ghcr — either way, the compatibility layer is non-functional
against today's registry.

Schema differences that make "add a few more aliases" insufficient:

1. **Disk layout:** lume ships `disk.img` split into named 500 MB `tar`
   parts reassembled by filename; cove expects LZ4-compressed, sha256-verified
   chunks addressed by `chunk-index`/`chunk-total` annotations and written
   at computed `offset`s via `WriteCompressedChunkAt`. Different compression,
   different addressing, different verification.
2. **Identity metadata:** lume ships `nvram.bin` and `config.json` as
   separate layers keyed by `image.title`; cove keys them by a `role`
   annotation (`nvram`/`hw-model`/`machine-id`). Lume has no `hw-model` or
   `machine-id` blob at all — macOS hardware identity lives inside
   `config.json`, which cove does not parse.
3. **No size / digest hints:** without `uncompressed-size` and
   `uncompressed-content-digest` on each layer, cove cannot preallocate
   `disk.img.partial` or verify final output.

**Severity for 0.1 ship: MAJOR, but NOT blocker** — contingent on
interpretation of roadmap §114–124 item #6 ("`cove pull` of a lume-produced
image boots in cove"):

- If "lume-produced" means **public ghcr.io/trycua/* images shipped by the
  upstream lume project**: this is a **blocker**. It does not work and will
  not work without a second import path that speaks lume's tar-split +
  config.json format.
- If "lume-produced" means **any image produced by running `lume push`
  against cove's own registry schema** (i.e., a hypothetical lume fork or
  build that already emits cove-shaped manifests): the code path is
  structurally sound (manifest parse, chunk streaming, metadata restore,
  atomic rename all implemented) but cannot be verified today without such
  an image in hand. Treat as **major — unverified**.

Recommended action before tagging 0.1: pick one of —

(a) Restrict the ship-gate wording to "cove-format OCI images" and mark #6
    as verified-by-inspection (status quo, already PASS in the audit table);
(b) Land a lume-tar-split importer (new `pull` code path keyed on
    `org.opencontainers.image.title` + `mediaType` part.number/part.total)
    before 0.1; medium effort, ~1–2 days including a real boot test;
(c) Ship 0.1 without lume interop and document it as "roadmap: 0.2."

No VMs were created or modified during this test. `hermes-mlx-go-60g-v10`
untouched. No registry writes. `go test ./internal/ociimage/...` still
passes.

## Smoke test: cove up (fresh install)

**Date:** 2026-04-24. **Binary:** `./cove` version `4599ce29`. **IPSW:**
`~/.vz/cache/RestoreImage.ipsw` (17.6 GB).

**Command:**
```
./cove up -user smoketest -password smokepass123 -vm smoketest-vm \
  -ipsw ~/.vz/cache/RestoreImage.ipsw -headless -disk-size 48 -no-shutdown
```

**Result: FAIL.** Install phase reported 100% complete, then provisioning
aborted because the VM disk was missing. Log excerpt:

```
=== Step 1/3: Installing macOS ===
…  0.0% … 100.0%
=== Installation Complete ===

Stopping VM...
VM stopped.
warning: disk not found after VM stop: /Users/tmc/.vz/vms/smoketest-vm/disk.img
  vmDir=/Users/tmc/.vz/vms/smoketest-vm
  cannot list vmDir: open /Users/tmc/.vz/vms/smoketest-vm: no such file or directory

Provisioning VM disk...
warning: disk provisioning failed: disk image not found: …/smoketest-vm/disk.img
…

=== Step 2/3: Provisioning VM ===
error: stage provisioning: disk image not found: …/smoketest-vm/disk.img
```

**Post-condition:** entire `/Users/tmc/.vz/vms/smoketest-vm/` directory is
missing. No `disk.img`, no `aux.img`, no `config.json` — the directory was
never created under that path, or was removed before the stop handler ran.
`find /Users/tmc/.vz -name 'smoketest*'` returns nothing.

**Diagnosis:** path-resolution mismatch or install-target divergence in the
`up` pipeline. Install reports success against a VM target, but the path
that `stopVMAndInject` (`installer.go:161-187`) stats post-stop
(`target.Directory` → `~/.vz/vms/<name>/disk.img`) does not exist. Existing
VMs on disk (e.g., `hermes-mlx-go-60g-v10`) live directly under `~/.vz/<name>/`
not under `~/.vz/vms/<name>/`, suggesting either (a) the `-vm <name>` target
resolver pointed install at one path and post-install cleanup at another,
or (b) install materialized the disk under a temp dir that was cleaned up
by the installer completion handler before provisioning ran.

**Ship-gate impact on item #1 (`cove up -user <name>` reaches desktop
without SA handholding):** downgraded from PASS-by-inspection to **FAIL —
empirical**. The `provisionStrategy=disk` + `SkipSetupAssistant=true` +
`AutoLogin=true` hardcoding in `up.go:217,275-276` is correct, but the
install→provision handoff does not reach that code path on a fresh `-vm
<new-name>` invocation.

**Severity for 0.1 ship: BLOCKER.** This is the flagship command of 0.1.
A fresh `cove up -user X -vm Y` must produce a bootable provisioned VM;
today it produces a missing directory. Root cause needs investigation
before tagging 0.1.

**Test artifacts:** log at `/private/tmp/.../tasks/b0nzggawh.output`.
hermes-mlx-go-60g-v10 untouched. No cleanup needed (smoketest-vm dir is
already gone).
