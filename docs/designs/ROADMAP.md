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
[015](archive/015-soft-reset-empirical.md), and the post-integration NotebookLM refresh
in [016](archive/016-notebooklm-roadmap-refresh-2026-04-30.md). The v0.3 execution
slices are tracked in [017](archive/017-v03-execution-roadmap.md). The 012, 016, and
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
  claims. See [015](archive/015-soft-reset-empirical.md).
- `cove` is not clean for public software/registry branding based on the
  preliminary USPTO search. Public registry, signed image distribution, and
  product-name claims need a legal/product decision first.

## RC scope: what ships and what's deferred

This boundary is canonical and must agree with the CLI reference, changelog,
release checklist, [016](archive/016-notebooklm-roadmap-refresh-2026-04-30.md), and
[017](archive/017-v03-execution-roadmap.md).

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
| CDC vs fixed-offset chunking trade-off study | done | none | [002](archive/002-cove-disks-oci.md) | Settles whether content-defined chunking is worth the cost before committing the v0.2 store design. |
| `cove doctor` TCC/FDA probe | done | none | [TCC research](../research/tcc-via-user-agent.md) | Triggers/diagnoses Full Disk Access state before VirtioFS access silently fails. |
| Verify 008 codebase-cleanup status | done | none | [008](archive/008-codebase-cleanup-plan.md) | Confirms which cleanup phases already landed and gates v0.2 work. |

## v0.2 - Linux Workstation + Foundation Cleanup

| Item | Priority | Depends on | Source | Why |
|---|---|---|---|---|
| Local content-addressed store at `~/.vz/store/` + GC | done | none | [002](archive/002-cove-disks-oci.md) | Required foundation for resumable pulls and `cove build`. |
| Nested KVM on M3/M4 | done | none | [006](006-cove-linux-v02.md) | Enables minikube/k3d-class Linux developer workflows on supported Apple Silicon hosts. |
| Turnkey Linux distros (Ubuntu, Debian, Fedora, Alpine) | done | none | [006](006-cove-linux-v02.md) | Provides reliable unattended installers, including fast Alpine setup. |
| Linux shell unary RPCs (`ResizeExecTTY`, `SignalExec`, `SetTime`) | done | none | [006](006-cove-linux-v02.md) | Keeps terminal control proxy-safe and per-call authenticated. |
| VirtioFS UID/GID auto-mapping + Rosetta-by-default | done | none | [006](006-cove-linux-v02.md) | Removes manual chmod and Rosetta setup toil for Linux guests. |
| ControlServer decomposition - phases 1+2 | done | none | [008](archive/008-codebase-cleanup-plan.md) | Moves agent and shared-folder configuration seams out of the package-main god object. |
| DHCP lease exhaustion warnings | done | none | gap vs tart DHCP lease handling | Warns high-throughput fork users before macOS VM networking degrades from long DHCP leases. |
| USPTO trademark search for "cove" | done | none | product/legal hygiene | Finds live/pending software-class COVE conflicts before public registry work. |

## v0.2.1 - Shell + Image Surface (post-v0.2 polish)

| Item | Priority | Depends on | Source | Why |
|---|---|---|---|---|
| `cove shell <vm>` server-side commands (Slice 1) | done | v0.2 in-process `cove run -linux -shell` | [023](023-cove-shell-exec-ux.md) | Ships agent-exec-attach/-resize/-signal control-socket commands. No proto bump. Landed 17211bd. |
| `cove shell <vm>` standalone client (Slice 2) | done | Slice 1 server commands | [023](023-cove-shell-exec-ux.md) | Standalone client subcommand. Landed `33fbe7e` (~303 LOC, 7 tests). Bidi stdin/stdout/stderr ships in Slice 3 (v0.3 proto bump) — see next row. |
| `cove shell <vm>` bidi `ExecAttach` (Slice 3) | done | Slice 2 + v0.3 proto bump | [023](023-cove-shell-exec-ux.md) | Additive bidi `ExecAttach` stream RPC plus fallback to the v0.2 unary/`ExecStream` path for older agents. Shipped 2026-05-04 at `4d9043a`, `8e550d4`, `5d5876f`. |
| Local image store + `cove image build/list/rm` (Slice 1) | done | fork-from + APFS clonefile | [024](024-cove-runner-images.md) | Pre-baked, forkable VM images at ~/.vz/images/. Landed 8a106dc, 1027 LOC, 8 tests green. |
| `cove run -fork-from <local-image-ref>` + `-ephemeral` | done | image store Slice 1 | [024](024-cove-runner-images.md) | Wires image store into existing fork-from codepath so users can spawn disposable VMs from a saved baseline. Shipped with image Slice 1 at 8a106dc. |
| github-runner vzscript | done | none | gap vs Cirrus tart workflow | Self-hosted GHA runner inside a long-lived cove VM; primary billing-block escape hatch. |
| gitlab-runner vzscript | done | none | parity with github-runner | Same-shape recipe for GitLab CI projects. |
| tailscale vzscript | done | homebrew vzscript | gap for users wanting stable remote access | Brings up Tailscale daemon on guest with `--ssh`; idempotent. |

## v0.3 - `cove build` + Caching + Agent Adapters

| Item | Priority | Depends on | Source | Why |
|---|---|---|---|---|
| `cove build` VM execution path | done | v0.2 store + dry-run planner | [003](archive/003-cove-build-oci-caching.md) | Local-base builds create scratch VMs, restore cache-hit layers, execute misses, persist metadata, and leave pushable image state. Registry-base execution remains deferred. |
| Secrets via tmpfs (`# secret:` directive) with guest swap disabled | done | build execution | [003](archive/003-cove-build-oci-caching.md) | Prevents secret leakage into pushed OCI block diffs. |
| `cove build` compaction integration | done | build execution | [002](archive/002-cove-disks-oci.md), [003](archive/003-cove-build-oci-caching.md) | Wires `fast`, `targeted`, and `thorough` build compaction into the pipeline before diffing and pushing images. |
| Fork-only benchmark publication | done | existing fork support | [012](archive/012-product-roadmap-2026.md), [015](archive/015-soft-reset-empirical.md), [bench result](../../bench/fork-time/results-20260427.md) | Published 132-140 ms stopped-VM fork measurements on the M4 smoke host after soft reset failed as the isolation primitive. |
| Boot-to-agent fork benchmark publication | done | existing fork support + reachable agent base image | [012](archive/012-product-roadmap-2026.md), [015](archive/015-soft-reset-empirical.md), [bench result](../../bench/fork-time/results-agent-20260430.md) | Published the product-relevant time from fork command to agent reachability on named M4 Max hardware. |
| ControlServer decomposition - phase 3 (`internal/control`) | done | v0.2 phases 1+2 | [008](archive/008-codebase-cleanup-plan.md) | Completes the cleanup arc started in v0.2. Shipped as `cede792`. |
| OpenAI Agents SDK adapter v1 | done | fork/restore + control socket | [012](archive/012-product-roadmap-2026.md), [OpenAI example](../examples/openai-agents.md) | Proves the agent-substrate pitch with a fork-first local adapter under `adapters/openai-agents-python`. |
| OpenAI adapter release hardening | done | adapter v1 + boot-to-agent benchmark | [012](archive/012-product-roadmap-2026.md), [017](archive/017-v03-execution-roadmap.md) | Added live smoke instructions, package checks, and fork-first example polish before treating the adapter as a release surface. |
| Anthropic sandbox-runtime adapter | done | OpenAI adapter lessons | [012](archive/012-product-roadmap-2026.md) | Expands agent integrations after the first adapter proves the shape. Shipped as `fafc32a`. |
| Curated agentkit base images | done | build execution + trademark decision | [012](archive/012-product-roadmap-2026.md) | Prepares the v1.0 registry story without publishing under a blocked name. Local curated bases shipped as `92f2272`; public registry publication remains deferred. |
| Disk I/O tuning Slices 1-4 | done | Linux workstation install path | [027](027-disk-io-tuning.md) | Slice 1 (DiskCachePolicy + `-disk-sync`) `fc7ff1e`, `a459076`; Slice 2 (Linux NVMe + `-nvme`) `8500ecb`, `968cbde`, benchmark deferred `1b7c947`, `f71851b`; Slice 3 (preallocated raw install disk + `-raw-disk`) `7ca06a9`, benchmark `093d63d`; Slice 4 (block device passthrough) `b522ab3`, `a78e891`, `74d9527`, `65b6964`, benchmark gated `50ba06e`. |
| Block device passthrough | done | cove-helper root protocol | [028](028-block-device-passthrough.md) | Raw `/dev/rdiskN` helper protocol, Linux run wiring, and smoke runbook shipped at `b522ab3`, `a78e891`, and `74d9527`. |
| GitHub Actions executor Slice 2 cache reuse | done | cove-action Slice 1 + local image store | [030](030-gha-executor-slice-2.md) | Adds local-only cross-run cache images for the private GHA executor, preserving fork isolation while speeding repeated CI runs. Implemented by T77 at `9e6253a`, `f06d554`, and `c0a1433`. |
| Packer plugin shim decision | maybe | none | gap vs tart Packer integration | Decide whether a shim accelerates adoption or distracts from the `cove build` moat. |

## v0.4 — CI Executors + Adapters + Daemon + Fleet

| Item | Priority | Depends on | Source | Why |
|---|---|---|---|---|
| VM identity preservation for forked vmstate bundles | done | fork/restore | [013](013-vm-fork.md) | Preserves identity files across vmstate forks so forked agents keep stable guest identity. Design 013 Phase 5 Slice 5a shipped at `67c9abc` (vmidentity bundle) and `7e4ed99`/`4bd435d` (fork preserve identity); status note at `4f8035c`. |
| Anthropic computer-use adapter v2 | done | agent-sandbox CLI | [022](archive/022-v04-anthropic-adapter.md) | Adds a Go-side Anthropic Messages computer-use loop and wires it into `cove agent-sandbox run`. Design v2 update at `55a2463`; implementation at `33e5b30` (`anthropicadapter` computer-use loop) and `775537f` (`agent-sandbox` provider wiring). |
| OpenAI Agents SDK `SandboxRunConfig` helper | done | OpenAI adapter v1 | [035](035-openai-sandbox-run-config.md) | Adds a local Python `cove-sandbox` helper for `Runner.run()` workflows. Design doc landed at `36552c2`; implementation at `4d61edd` (Python `sandbox-run-config` helper) and `27f9e24` (`docs/integrations/openai-agents.md` integration walkthrough). |
| GitHub Actions private executor surface | done | local image store + run metrics | [021](archive/021-v04-ci-executors-tracks.md), [030](030-gha-executor-slice-2.md) | Ships the private GHA wrapper, action metrics, preflight commands, and cache-image reuse. Representative commits: `0985377`, `19804c7`, `8bd473e`, `82a0ac5`, `7fafe40`, `9e6253a`. |
| NixOS guest distro | done | Linux installer | [036](036-nixos-guest-support.md) | Adds `cove install -nixos`, a NixOS installer path, base vzscript, and quickstart. Design doc landed at `1ddd3b9`; implementation chain `8324750` (installer), `2427b2e` (`nixos-base.vzscript`), `f1e6812` (quickstart docs), `07835a9` (cli.md install table). |
| Linux Desktop autoprovisioning | done | Linux desktop installer | [037](037-linux-autoprov.md) | Documents and hardens the Desktop first-boot auto-login path. Design doc landed at `e93a3e0`; implementation chain `f718d69` (GDM autologin), `c624d3f` (`-desktop-installer oem\|server` flag), `aebcf13` (OEM user provisioning), `c3f7ead` (cloud-init disable), `449cbfa` (autologin late-command tests), `0cbc455` (keyring prompt clear), `4f71eb3` (GNOME Initial Setup suppression). |
| Fleet Slice 1 remote routing | done | SSH access to trusted Macs | [034](034-fleet-slice-1.md) | Adds fleet host config, SSH tunnel helpers, routeable remote commands, and docs. Shipped at `622b571`, `695ae2e`, `9f993a5`, and `366bfac`. |
| Fleet Slice 2 aggregation | done | Fleet Slice 1 | [034](034-fleet-slice-1.md) | Adds parallel remote query helpers and aggregate VM/image/ps lists. Shipped at `afba1a5`, `0b4776f`, `e59348d`, and `6e91044`. |
| Fleet Slice 3 image transfer and placement | done | Fleet Slice 1 + local image store | [034](034-fleet-slice-1.md) | Adds `cove fleet image push/pull/sync` and least-loaded run placement. Shipped at `f13dae5`, `1273fcb`, and `e347836`. |
| Cove daemon Slices 1-3 + storage budget wiring | done | per-VM control sockets | [033](033-cove-daemon.md), [040](040-storage-budget.md) | Adds `coved`, `cove daemon status`, lifecycle enforcement, scheduled image GC, observability stack (event bus, JSONL, Prometheus, web UI, webhooks), and design 040 storage poll with prune coordinator. Shipped at `d786e53`, `2b516da`, `c6f33df`, `3258982`, `ef019c1`, `fa2a1bf`, `94d08db`, `33bcf38`, `90b6a66`, `394b812`, and `9b7c849`. |
| VM lifecycle policy v2 | done | none | [031](031-vm-lifecycle.md) | Adds policy persistence, CLI, enforcement loop, locked run-budget counter, and telemetry. Design doc landed at `12c391d`; implementation chain `d1df12b` (`internal/vmpolicy` persistence), `2202f46` (`cove policy` CLI), `80eea77` (runtime enforcement), `9749f29` (locked run-budget counter), `cd899a1` (`vm_policy_stop` telemetry). |
| Per-VM resource quotas | done | VM config persistence | [032](032-vm-quotas.md) | Adds quota persistence, APFS quota wrapper, and `cove quota` CLI. Design doc landed at `94bf2d2`; implementation at `62a71aa` (`internal/vmquota` + `diskutil apfs setQuota` wrapper) and `2bad0e8` (`cove quota` CLI + install-time `persistInstallQuota`). |
| Soft-reset destructive probe matrix + orchestrator | done | none | [015](archive/015-soft-reset-empirical.md) | Adds network, memory, process-table, filesystem-attribute probes and a run-all orchestrator. Shipped at `8c7d54b`, `a51c544`, `31017ca`, `0ed3b70`, `ad9dc16`, `7030657`, and `6773695`. |
| Image lifecycle UX | done | local image store | [024](024-cove-runner-images.md) | Adds prune/gc, tag, history, search, freshness verification, and inspect diffs. Shipped at `fb37866`, `75c1897`, `1e9806e`, `7cbbf9c`, `6f0d396`, and `b46deb0`. |
| Network policy, logs, and forwarding | done | control socket + guest agent | [001](archive/001-cove-serve-http-mcp.md), [034](034-fleet-slice-1.md) | Adds named policy audit logs, `cove network logs`, `cove forward` reverse direction, and UDP support. Shipped at `1ac32f9`, `6fd5bc5`, `6e6fa18`, `22e6c43`, and `235b678`. |
| Operator file/log/diff commands | done | guest agent + image store | [024](024-cove-runner-images.md) | Adds `cove cp`, `cove logs`, and manifest diff/inspect helpers. Shipped at `dbe1520`, `fca848e`, `9fc8303`, `b46deb0`, and `535db8c`. |
| Reliability sweep | done | run/provision/fleet surfaces | [031](031-vm-lifecycle.md), [033](033-cove-daemon.md) | Adds test-HOME isolation audit, concurrency fixes, Apple log predicate refresh, install overlay polish, startup state, and start watchdog. Shipped at `34775a4`, `3456d67`, `d3a6d18`, `ef82fc0`, `9dea52e`, and `f78ce61`. |
| Fresh VM login recovery cluster | done | macOS provisioning | [bugs/fresh-vm-login](../bugs/2026-05-05-fresh-vm-login-misclassify.md) | Makes non-root `cove up` fail loudly, refuses noninteractive native authorization, improves login-screen OCR, and dumps `diskutil list` on Data lookup failure. Shipped at `b9c06ee`, `e512329`, `a1742f4`, `14e2bdb`, and `ca4d824`. |
| Cirrus migration docs | done | fleet + images + action surface | [migration guide](../migrations/from-cirrus.md) | Adds Cirrus displacement landing page, migration walkthrough, README link, and blog draft. Shipped at `bdd1912`, `2642d01`, `e413e0e`, `6017373`, and `4635254`. |
| v0.4 readiness and integration matrix | done | shipped v0.4 surfaces | [release audit](../release/v0.4.0-readiness.md) | Adds v0.4 release notes draft, integration matrix, cross-reference pass, and readiness audit. Shipped at `96706d2`, `7852177`, `c29eae3`, and `14cabac`; this closeout updates the final docs. |

## v0.5 — Stabilization: package boundaries + observability fixes

**Status: Shipped v0.5.0 2026-05-08 at `463c5ce` (tag object `8e4c767`).**

This release contracts surface area and tightens internal boundaries. Slices
1-4 of design 039 ship, slice 5 (`internal/vmrun`) and all five slice 6
sub-slices (`agentBridge`, `screenCapture`, `inputBridge`, `lifecycleBridge`,
`networkBridge`) ship, **and the §7 facade move to `internal/controlserver` is
fully complete**: all five bridges (`Capture`, `Lifecycle`, `Agent`, `Input`,
`Network`) now live in `internal/controlserver/` behind narrow `*Host`
interfaces. `ControlServer` in package main is a thin facade.

| Item | Priority | Depends on | Source | Why |
|---|---|---|---|---|
| Design 039 package boundary extraction (slices 1-4) | done | none | [039](039-package-boundary-extraction.md) | Slice 1 introduces a command registry table replacing main.go switch sprawl (`346de9c`, net -53 LOC). Slice 2 adds an explicit `commandEnv` struct populated from globals at the dispatch boundary (`eeef127`). Slice 3 extracts `internal/controlclient` from `ctl.go`/`control_client.go` with a small Client API and no GUI/OCR pulled in (`3777885`, +200/-979 LOC net). Slice 4 extracts pure `ProvisionConfig`/`InjectOptions`/`ProvisionManifest` types into `internal/provision` with 200 LOC of tests at the new boundary (`49bd8f1`). |
| Startup state visibility (#292 Issue B) | done | run.lock | [bugs/fresh-vm-run-hang](../bugs/2026-05-05-fresh-vm-run-hang.md) | `detectVMState` now honors `run.lock` + live-pid liveness check; `runtime.json` records startup phase (configuring → building-config → creating-vm → awaiting-start → running); `cove list`/`cove status` surface `starting` state with phase + pid. Shipped at `3bc6d86`. |
| Bounded `internal/control.Server.Stop()` shutdown | done | control socket server | none (post-incident fix) | Adds `StopTimeout` (default 5s) and tracks active connections so a wedged health monitor cannot keep `cove run` alive after `runtime.json` writes `stopped`. Shipped at `cfb174b` via the agent-sandbox v2 batch. |
| Mlxqa fresh-VM provisioning P0 verified resolved | done | fresh-VM-login cluster | [bug doc](../bugs/2026-05-05-fresh-vm-login-misclassify.md) | Verified that the silent-success / no-user-account failure mode is caught loudly by the `b9c06ee...ca4d824` cluster plus `81680c4`/`1965ee7`/`e7b9e3e`. Forced noninteractive `cove up` exits with `auto-login provisioning needs the native macOS admin dialog`; recent fresh VM `mlxgo-fresh-headed2-20260505` has `mlxqa` account and `root:wheel` LaunchDaemon plists. No additional code change needed. |
| Design 039 slice 5 (`internal/vmrun` config) | done | slices 1-4 | [039](039-package-boundary-extraction.md) | `internal/vmrun` package shipped with `RunConfig`, `HostConfig`, `DevicePlan`, `Plan()` pure function and table-driven tests (`3d876b8`). `macos.go`/`linux.go`/`windows.go` entry-point bodies converted via `vmrun_adapter.go` snapshot constructor (`c5dcfec`/`ed05f50`/`23c25d1`). 38 entry-point global reads retired; `runVMHeadless`/`runVMWithGUI` and `isoPath`/`usbDevices` deferred to v0.6 (see slice 5 summary). |
| Design 039 slice 6 sub-slice 1 (`agentBridge`) | done | slice 5 | [039](039-package-boundary-extraction.md) | Agent connection plumbing + health monitor extracted into `agentBridge` value embedded in `ControlServer` (`fb376ba`/`270bd42`/`2d2f266`/`d805575`/`b52d2d6`). Two `ControlServer` mutexes retired (`agentMu`, `healthMu`); 5 invariant tests pin disconnect-edge / cadence / nil-cs at the bridge boundary. Sub-component stays in package main per facade-late rule. |
| Design 039 slice 6 sub-slice 2 (`screenCapture`) | done | slice 5 | [039](039-package-boundary-extraction.md) | Capture cache + lazy OCR service extracted into `screenCapture` value with own narrow lock (`d9d5055`/`067aff5`). One `ControlServer` mutex retired (`screenshotMu`); OCR no longer piggybacks on `mu`. Mouse Y mapping invariant (memory-protected `viewContentHeight`) preserved. |
| Design 039 slice 6 sub-slice 3 (`inputBridge`) | done | slice 5 | [039](039-package-boundary-extraction.md) | Mouse/keyboard/scroll/typeText routed through `inputBridge` value embedded in `ControlServer` (`fdb9907`/`5422e03`/`7b3c387`/`139da0d`). All four input families own their bodies on `inputBridge`; `ControlServer` methods are 1-line forwarders preserving every external signature. `viewContentHeight` mouse-mapping memo + `CGEvent->NSEvent->keyDown` keyboard invariants documented and preserved. |
| Design 039 slice 6 sub-slice 4 (`lifecycleBridge`) | done | slice 5 | [039](039-package-boundary-extraction.md) | Lifecycle ctx + policy state extracted into `lifecycleBridge` value (`6592695`/`ccba4bf`/`6947d3d`). Two `ControlServer` mutexes retired (`policyMu`, `lifecycleMu`); 6 invariant tests pin set-once start time, exec increment, one-shot stop edge, pre-start ctx, shutdown idempotence at the bridge boundary. Bounded-shutdown invariant from `cfb174b` preserved by construction (`StopTimeout` stays in `internal/control.Server`, `life.shutdown()` only cancels ctx). |
| Design 039 slice 6 sub-slice 5 (`networkBridge`) | done | slice 5 | [039](039-package-boundary-extraction.md) | iTerm2 proxy, port forwards, HTTP listeners, VNC/debug status extracted into `networkBridge` value (`0c74183`/`4a66c3a`). Five `ControlServer` catch-all-mu uses retired (iterm2Proxy, portForwards, httpListeners, vncStatus, debugStubStatus); `StartHTTP` lazy-init race fixed. T35 portForwards concurrency invariant preserved. Dead `httpListeners` field on `ControlServer` swept in follow-up `1adbfaa`. |
| Design 039 facade move — `Capture` + `Lifecycle` to `internal/controlserver` | done | slices 6.1-6.5 | [039](039-package-boundary-extraction.md) | First R48 facade slice shipped (`8dcf3e9`/`09fc1d1`): `internal/controlserver/` introduced with `Capture` and `Lifecycle` types moved cleanly (zero back-refs). `doc.go` pins Host-interface convention as load-bearing artifact for the remaining bridges. |
| Design 039 facade move — remaining bridges (`Agent`, `Input`, `Network`) | done | facade slice 1 | [039](039-package-boundary-extraction.md) | All three larger bridges shipped into `internal/controlserver/` over the R49 arc. `Agent` shipped at `cc9d12f` (~1500 LOC, `AgentHost` iface). `Input` shipped via `595727a` (interface) + `d6ee2e0` (move; ~700 LOC, `InputHost` iface; preserves `viewContentHeight` mouse mapping + CGEvent->NSEvent->keyDown invariants). `Network` shipped via `62068ce` (interface) + `8bd7a65` (move; ~1100 LOC, `NetworkHost` iface; pulls iTerm2Proxy + PortForwardManager). `ControlServer` in package main is now a thin facade. Behavioral diff: none. |
| Design 040 storage budget (Phases 0-5) | done | none | [040](040-storage-budget.md) | Storage census, budget config, prune coordinator, pin/unpin CLI, and daemon storage poll all shipped during R57. New surface: `cove pin/unpin/pins list`, `cove storage prune`, `cove storage budget get/set/clear`; `coved` polls usage and runs pin-aware prune at hard tripwire. SHA chain: `78b2e7b`, `ce1a2c0`, `e6a9850`, `5660b13`, `7e9ea28`, `292b81d`, `394b812`, `42714c0`, `e087a71`, `1c49740`, `cacce19`, `6cf3a93`. |

## v0.6 — macOS 14.0 floor + ScreenCaptureKit migration

**Status: in flight. macOS host floor bumps from 12.0 to 14.0; capture pipeline migrates from `CGWindowListCreateImage` to ScreenCaptureKit.**

| Item | Priority | Status | Size | Depends on | Source | Why |
|---|---|---|---|---|---|---|
| Design 041 Slice 1 — SCKit capture path behind feature flag | must | shipped (`8d55d7a`) | S | none | [041](041-screencapturekit-migration.md) §Slice 1 | Lands ScreenCaptureKit-backed capture as opt-in path, leaving `CGWindowListCreateImage` as default until Slice 2 perf A/B. Accepted at `50bf8ca`; probe + `cove doctor sckit-preauth` shipped at `8d55d7a`. TCC pre-flight (originally tracked as a separate Slice 4) was folded into the Slice 1 doctor surface. |
| Design 041 Slice 2 — dual-path spike + perf A/B | must | spike shipped (`d0877b8`); p50/p95 TBD | M | Slice 1 | [041](041-screencapturekit-migration.md) §Slice 2 | Side-by-side capture with measured latency; SCKit promoted to default only if no regression on representative workloads. R58 spike `cove doctor sckit-spike` at `d0877b8`; p50/p95 numbers blocked on Screen Recording TCC grant + GUI VM run. Depends: Slice 1. |
| Design 041 Slice 3 — dual-path opt-in via `COVE_CAPTURE_BACKEND` | must | shipped (`55257f2`) | M | Slice 2 | [041](041-screencapturekit-migration.md) §Slice 3 | Wires the SCKit spike into the live `controlserver/Capture` path with `cgwindow`/`sckit`/`auto` selector, fallback policy, and per-VM `<vmDir>/capture-backend` override. Default stays `cgwindow`; SCKit failures fall through with one `slog.Warn` per cause. Default flip is Slice 4, deferred to v0.7. Spec at `5794c94`; impl at `55257f2`. Depends: Slice 2. |
| Design 041 Slice 4 — flip default to `sckit`; drop `CGWindowListCreateImage` | must | spec landed (`318d801`); impl deferred to v0.7 | S | Slice 3 (one release of bake time) + Slice 2 perf clearance | [041](041-screencapturekit-migration.md) §Slice 4 | Flips the default to SCKit on macOS 14+ and deletes the CGWindowList path (clears SA1019; net negative LOC). Per design 041 §6 and RELEASE-NOTES-v0.6.0.md, impl is held to v0.7 pending Slice 2 perf data plus one release of Slice 3 bake time. Depends: Slice 3, Slice 2 perf clearance. |
| APFS clonefile-aware storage census | should | not started | M | design 040 phases 0-5 | [040](040-storage-budget.md) §Open questions | Storage census reports per-file allocated size; two images sharing 30 GB of cloned blocks show as 60 GB used. Switch to a one-pass reference-counted walk if user-visible. Spec needed: design 040 §Open questions calls out the algorithm but no separate design doc. Depends: design 040 phases 0-5 (shipped R57). |
| Guest → host artifact copy-out (`cove runs export --include-guest`) | should | not started | M | guest agent + runs bundle | [Cirrus migration readiness 2026-05-08](../strategy/cirrus-migration-readiness-2026-05-08.md) §Partial | First-class guest-side artifact pull into `~/.vz/runs/<run-id>/` so CI users don't hand-roll `cove ctl cp` at end of every script. Sized M per Cirrus migration audit; pure engineering, no design doc. Spec needed if scope grows beyond agent stream + tar. |
| Cirrus secrets (`ENCRYPTED[…]`) → guest env | should | Slices 1-2 shipped (`fe99629`, `ab7f159`) | M | cove-action `secrets:` plumbing | [cirrus-secrets-fix-2026-05-08](../strategy/cirrus-secrets-fix-2026-05-08.md) | Closes the last engineering blocker for the Cirrus migration story before 2026-06-01: host-side `secrets:` flag + redactor on top of the already-plumbed `ExecRequest.env`. Spec at `7f196e9`; Slice 1 (metrics redaction) shipped at `fe99629`; Slice 2 (cove-action `secrets:` input → `--secret-env`) shipped at `ab7f159`. Depends: cove-action surface (shipped v0.4 at `0985377`). |
| Test coverage gap fill — control + storage internals | should | in progress (R59-R71+) | M | none | R57-R71+ test-gap dispatches | Targets exercised through R71: `storagecensus` (`dbfacf8`, `f6e7c7c`, `1339f2f`, `61c2ca5`, `84ab32d`; PiB-overflow fix `c7865e1`), `storagepins` (`c5ec526`, `4150a02`), `coved` poll scheduler (`9b7c849`). Continue across `internal/control`, `internal/controlserver`, and `internal/vmrun` to lift coverage before v0.6 GA. Sized M (per-pane single-package dispatches). |
| Deadcode + staticcheck SA4006 sweep — round 5 | should | in progress (R56-R83+) | S | none | R54-R83+ sweeps | Continue the U1000/SA4006 cadence shipped through R54 (`1bdd1e8`), R56 (`260e80c`, `05d0ee3`, `8d53d70`, `59be48a`), R57 (`aa1a80e`), R59 (`e3f135a`), R83 (`2decbf9`), and the follow-on internal-pkg cleanup at `3d54d6d` and `f5c14f4`. One small sweep per release keeps the surface narrow ahead of the SCKit and storage-census churn. Sized S; no design doc. |
| Capture-latency observability surface | maybe | spec needed | M | Slice 3 perf wiring | [041](041-screencapturekit-migration.md) §Slice 2 perf gate | Slice 2's "p50/p95 measured" gate currently lives in the spike binary. To keep the capture path honest post-flip, surface per-capture latency through `cove runs` + `coved` metrics so regressions are observable in production, not just bench. Spec needed (no design doc). Depends: Slice 3 production wiring lands `controlserver/Capture` instrumentation point. |

## v0.3 implementation slices

The next implementation branches should stay directly based on `origin/main`
and should not stack on each other. Each slice should be reviewable by itself;
see [017](archive/017-v03-execution-roadmap.md) for files, gates, and docs updates.

1. Build executor scaffold and scratch VM lifecycle, with non-dry-run still
   gated. See [018](archive/018-v03-build-executor-scaffold.md).
2. Cache-hit materialization, so cached layers can apply without guest boot. See
   [019](archive/019-v03-cache-hit-materialization.md).
3. Cache-miss VM execution, block diff production, metadata persistence, and
   the point where non-dry-run `cove build` becomes supported. See
   [020](archive/020-v03-cache-miss-execution.md).
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

- **2026-05-10** (R419): OpenAI Agents SDK `SandboxRunConfig` helper row separated design-doc SHA from implementation SHAs. `36552c2` is "docs: design openai sandbox run config"; `4d61edd` is the Python helper; `27f9e24` is the integration walkthrough doc.
- **2026-05-10** (R417): Anthropic adapter v2 row separated design-update SHA from implementation SHAs. `55a2463` is "docs: update anthropic adapter design v2"; `33e5b30` is the `anthropicadapter` computer-use loop; `775537f` is the agent-sandbox provider wiring.
- **2026-05-10** (R416): VM lifecycle policy v2 row separated design-doc SHA from implementation SHAs. `12c391d` is "docs: design vm lifecycle policy"; the implementation chain `d1df12b/2202f46/80eea77/9749f29/cd899a1` is now annotated per design 031's SHA chain header.
- **2026-05-10** (R414): Per-VM resource quotas row separated design-doc SHA from implementation SHAs. `94bf2d2` is "docs: design per-vm resource quotas"; `62a71aa` is the `internal/vmquota` + APFS wrapper; `2bad0e8` is the `cove quota` CLI + `persistInstallQuota`. Matches design 032's SHA chain header.
- **2026-05-10** (R412): VM identity preservation row SHA chain expanded. Row was missing `4bd435d` (fork preserve identity follow-up); design 013 Phase 5 Slice 5a header lists both `7e4ed99` and `4bd435d` as the canonical pair. Reframed to call out design 013 phase + slice and added the missing SHA.
- **2026-05-10** (R410): NixOS guest distro row SHA chain expanded. Row previously cited `1ddd3b9, 8324750, 2427b2e, f1e6812` (one of which was the design doc commit). Aligned with design 036's full implementation chain: `8324750` (installer), `2427b2e` (vzscript), `f1e6812` (quickstart), `07835a9` (cli.md table).
- **2026-05-10** (R407): Linux Desktop autoprovisioning row SHA chain expanded. Row previously cited `e93a3e0, 449cbfa, 0cbc455, 4f71eb3` (one of which was the design doc commit). Aligned with design 037's full implementation chain: `f718d69, c624d3f, aebcf13, c3f7ead, 449cbfa, 0cbc455, 4f71eb3`.
- **2026-05-10** (R406): Refreshed the 2026-05-02 changelog entry that still claimed "Slice 2 of 023 ... is the only remaining v0.2.1 implementation"; both Slice 2 (`33fbe7e`/`c38025f`) and Slice 3 (`4d9043a`/`8e550d4`/`5d5876f`) had shipped.
- **2026-05-10** (R404): v0.2.1 `cove shell` row refresh. Slice 2 row text "Stdin /dev/null until Slice 3 v0.3 proto bump" was stale — Slice 3 shipped 2026-05-04. Reworded Slice 2 to point to Slice 3 and added an explicit Slice 3 row (`4d9043a`, `8e550d4`, `5d5876f`).
- **2026-05-10** (R400): Cirrus secrets row flipped from "spec landed; impl pending" to "Slices 1-2 shipped (`fe99629`, `ab7f159`)". Slice 1 (metrics redaction) and Slice 2 (cove-action `secrets:` input → `--secret-env`) both landed 2026-05-08 per `docs/strategy/cirrus-secrets-fix-2026-05-08.md`; the row text had not caught up.
- **2026-05-10** (R369): Roadmap row freshness pass. Bumped two v0.6 in-progress rows past their stale R62/R59 windows: test-coverage gap fill now reflects R71+ cadence with new commits (`1339f2f`, `61c2ca5`, `84ab32d`, `c7865e1`, `4150a02`); deadcode/SA4006 sweep extended through R83 (`2decbf9`) and post-R83 internal-pkg follow-ons (`3d54d6d`, `f5c14f4`). No status flips; both rows remain `should` / in progress.
- **2026-05-09** (R87): Roadmap design-row drift sweep. Fixed two v0.4 rows (`VM lifecycle policy v2`, `Soft-reset destructive probe matrix + orchestrator`) where the Depends-on column duplicated the Source design link instead of `none`. No status flips — designs 029 (`16853bb`), 038 (agent-sandbox v2 provider abstraction) ship-state already covered by adjacent rows (virtiofs runtime preflight; Anthropic adapter v2 + cove-action surfaces).
- **2026-05-08** (R62): v0.6 milestone refresh post-v0.5. Slice 3 row updated to "production wiring + flip default" anchored on spec `5794c94`; Slice 4 reclaimed for the CGWindowList drop (R60 had it as TCC pre-flight, which actually shipped inside Slice 1's `cove doctor sckit-preauth`). Added Cirrus secrets row (spec `7f196e9`, impl pending), test-coverage gap-fill row (R59-R62 cadence), deadcode/SA4006 sweep row (R54-R59 cadence), and capture-latency observability candidate (spec needed, depends on Slice 3 wiring). Every v0.6 row now carries S/M/L sizing and explicit `Depends:` lines.
- **2026-05-08** (R60): v0.5/v0.6 transition audit. v0.5 verified all-done (no rows promoted). v0.6 SCKit table grew a Status column: Slice 1 shipped at `8d55d7a`, Slice 2 spike done at `d0877b8` (perf TBD), Slices 3-4 not started. Added APFS clonefile-aware census (design 040 leftover) and guest→host artifact copy-out (Cirrus migration M-sized) as v0.6 candidates.
- **2026-05-08**: Design 040 storage budget Phases 0-5 fully shipped during R57 (`78b2e7b`, `ce1a2c0`, `e6a9850`, `5660b13`, `7e9ea28`, `292b81d`, `394b812`, `42714c0`, `e087a71`, `1c49740`, `cacce19`, `6cf3a93`). Design 041 ScreenCaptureKit migration accepted at `50bf8ca` with macOS 14.0 host floor; v0.6 milestone added with Slices 1-4. v0.5.0 tag cut later same day at `463c5ce` (tag object `8e4c767`).
- **2026-05-07**: v0.5 milestone code-complete at `8bd7a65`. Design 039 §7 facade move closed: `Capture` (`8dcf3e9`), `Lifecycle` (`09fc1d1`), `Agent` (`cc9d12f`), `Input` (`d6ee2e0`), `Network` (`8bd7a65`) all live in `internal/controlserver/`. RELEASE-NOTES-v0.5.0.md drafted (tag cut next day, see 2026-05-08 entry).
- **2026-05-05**: Added the v0.4 closeout milestone covering R36-R40 shipped work across agent adapters, fleet, daemon, lifecycle, quotas, NixOS, Linux desktop provisioning, image/network surfaces, reliability, Cirrus migration, and the fresh-VM-login fix cluster.
- **2026-05-05**: design [030](030-gha-executor-slice-2.md) landed for GHA executor Slice 2 cross-run cache reuse after T77 shipped `9e6253a`, `f06d554`, and `c0a1433`.
- **2026-05-04**: Reconciled shipped v0.3 rows: ControlServer phase 3 landed at `cede792`, the Anthropic sandbox-runtime adapter landed at `fafc32a`, and curated agentkit base images landed at `92f2272`.
- **2026-05-04**: designs [027](027-disk-io-tuning.md) and [028](028-block-device-passthrough.md) shipped. Disk I/O tuning landed at `fc7ff1e` and `a459076`; Linux NVMe Slice 2 landed at `8500ecb`, `968cbde`, and `1b7c947`; block device passthrough landed across `b522ab3`, `a78e891`, and `74d9527`.
- **2026-05-02**: design [025](025-cove-action-security.md) cove-action security architecture landed at `1db4830` (411 LOC). Threat model + token lifecycle + isolation invariants for the v0.4 cove-action GHA wrapper. Clears the security-gate prerequisite on design [021](archive/021-v04-ci-executors-tracks.md) Slice 1 implementation. Per user 2026-05-02: Slice 1 still v0.4-targeted; cove repo stays private; design [024](024-cove-runner-images.md) Slice 3 deferred indefinitely.
- **2026-05-02**: v0.2.1 Slice 1 implementations shipped: design [023](023-cove-shell-exec-ux.md) server-side at `17211bd` (289 LOC, 7 tests); design [024](024-cove-runner-images.md) image surface at `8a106dc` (1027 LOC, 8 tests). Slice 2 of 023 (standalone `cove shell <vm>` client) shipped same day at `33fbe7e` (status flipped at `c38025f`); Slice 3 bidi `ExecAttach` followed 2026-05-04 at `4d9043a`/`8e550d4`/`5d5876f`.
- **2026-05-02**: Added v0.2.1 milestone covering `cove shell <vm>` Slice 1 (design [023](023-cove-shell-exec-ux.md)), local image store + `cove image build/list/rm` Slice 1 (design [024](024-cove-runner-images.md)), and three CI/networking vzscripts (github-runner, gitlab-runner, tailscale).
- **2026-05-02**: Trimmed Buildkite track from v0.4 design [021](archive/021-v04-ci-executors-tracks.md); v0.4 CI work now covers GHA + GitLab only.
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
