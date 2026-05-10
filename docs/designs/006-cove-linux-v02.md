# cove Linux support — making it fantastic

**Status**: canonical v5 (Council round-2 signoff 2026-04-17)
**Author**: cove team
**Date**: 2026-04-16 (signed off 2026-04-17)
**Target**: cove 0.2 (some items slip to 0.3)

## Changelog

- **Council round-2 signoff (2026-04-17)**: No deltas, just signoff notes on the two v5 questions.
  - **Drop-bidi-entirely**: concurred. Security rationale made explicit (long-lived bidi streams validate auth once at upgrade time — a leaked token can issue SIGKILL indefinitely while the socket stays open; unary forces stateless per-call auth). Systems Engineer separately flagged that localhost µs RTTs make the bidi-vs-unary latency argument moot.
  - **Bootstrap story**: `go build` must be zero-dependency; protoc-gen-es et al. must not be required for `make` or `go build`. Concrete Makefile:
    ```makefile
    .PHONY: proto
    proto:  # only for contributors editing proto/*.proto — commits Go stubs
    	go run github.com/bufbuild/buf/cmd/buf@latest generate \
    		--template proto/buf.gen.yaml --path proto/

    .PHONY: release-clients
    release-clients:  # CI-only target, generates TS/Swift into release tarball
    	npm install -g @bufbuild/protoc-gen-es @connectrpc/protoc-gen-connect-es
    	buf generate --template proto/buf.gen.clients.yaml --path proto/
    ```
    Generated Go stubs (`proto/agentpb/*.go`, `proto/agentpbconnect/*.go`) remain committed. TypeScript/Swift clients are built only in the `Release` GitHub Actions workflow and archived as `cove-clients-v0.2.tgz` alongside the binary. Standard `go build` touches neither npm nor network — contributor bootstrap stays ~10 seconds.
- **v5 (post-round-3 Council + honest rationale)**: Fifth-round amendments, all confined to Pillar 4e and the Transport subsection:
  - **Dropped the `ExecInteract` bidi RPC.** The v4 proto additions, client `Interact` session interface, and cove-linux-shell capability-probe / fallback machinery are all removed. Kept the v3 unary additions (exec_id + tty on ExecRequest, ResizeExecTTY, SignalExec, SetTime) and the existing server-streaming `ExecStream` for stdout/stderr. One shape, no fallback path, no capability negotiation.
  - **Rationale shift (honest version).** v4 justified the bidi path with "proxies break bidi". That's partially stale for 2026 — managed cloud LBs (AWS ALB since Nov 2023, Cloudflare since 2019, GCP for years) handle bidi fine. The honest v5 reasons for staying unary are: (a) no localhost latency win — 99% of cove traffic is localhost at µs RTTs where unary vs bidi is invisible; (b) simpler auth model — unary RPCs get per-call auth, a held-open bidi stream means one auth check covers every signal including SIGKILL, a strictly worse security posture; (c) the real bidi-fragility edge case is corporate egress (Zscaler, Netskope, Island, on-prem nginx <1.13.10 that request-body-buffers and silently hangs rather than returning a clean Unimplemented), relevant to the "cove CLI → remote cove serve over corporate network" future use case; (d) bidi is strictly additive later — if v0.4 telemetry shows remote-serve users benefit, we add `ExecInteract` as opt-in without breaking v0.2 clients.
  - **connect-go + buf stay.** One connect-go handler serves gRPC + gRPC-Web + HTTP/JSON, pure Go, proxy-safe. `http.ServeMux` wrapper maps Docker-shaped URLs to internal connect calls. All unchanged from v4.
  - **Self-host protoc-gen-es (drop buf.build remote).** The Council correctly flagged vendor risk. `buf generate` now runs with local binaries only (protoc-gen-go, protoc-gen-connect-go, protoc-gen-es, protoc-gen-connect-es via npm or Go install). Published TypeScript / Python / Swift clients come from release-time `buf generate` runs, archived as release artifacts. No dependency on buf.build registry being up at install time. The registry remains an optional convenience for users who want auto-updating SDKs, but cove's own CI does not depend on it.
  - **Net scope: -0.5 day from v4.** Removing the bidi handler, the capability probe, and the fallback path reclaims the +0.5 day v4 added for them. Revised v0.2 total: **~13 days**.
- **v4 (post-agent-API review, bidi-optional + connect-go)**: Fourth-round amendments, all confined to Pillar 4e:
  - **Added `ExecInteract` bidi stream RPC as an optional fast path** for capable clients (native gRPC over HTTP/2, connect-go with HTTP/2). Multiplexes resize, signal, and stdin into a single stream that shares per-`exec_id` state with the unary handlers. Unary RPCs (`ResizeExecTTY`, `SignalExec`) remain canonical and proxy-safe; `ExecInteract` returns `Unimplemented` over HTTP/1.1 and cove CLI falls back automatically.
  - **Adopted connect-go + buf explicitly** for the cove agent gRPC service and the `cove serve` HTTP API. One connect-go handler serves native gRPC (HTTP/2), gRPC-Web, and Connect's HTTP/JSON from one codepath. No new dependency — `proto/agentpbconnect/agent.connect.go` is already in the tree. No CGO (pure Go; preserves cove's cgo-free pitch).
  - **Docker-shaped URLs via http.ServeMux wrapper** in front of connect-go: external `/v1/vms/:name/exec/:id/resize` maps to the internal `/cove.v1.Agent/ResizeExecTTY` call. Docker-compatible shape externally, connect machinery internally.
  - **Go client gains `Interact(ctx, execID) (InteractSession, error)`** with a narrow `Send`/`Recv`/`CloseSend` surface. cove linux shell probes `Interact` once at startup and falls back on `Unimplemented`. Bidi SIGWINCH round-trip drops from ~10ms unary/HTTP/1.1 to ~2ms bidi/HTTP/2; noticeable over any tunnel, invisible on localhost.
  - **buf.build for schema ops**: `buf generate` for Go stubs, `buf lint` for proto backwards-compat enforcement, buf.build registry for zero-maintenance TypeScript/Python/Swift/Kotlin clients.
  - **Net scope: -0.5 day from v3.** The +0.5 day for the bidi handler (shared state, truly additive) is offset by -1 day from replacing the hand-rolled HTTP adapter with connect-go (one handler serves gRPC + JSON + gRPC-Web). Revised v0.2 total: **~13.5 days**.
- **v3 (post-round-2 Council + agent-API design review)**: Third-round amendments, all confined to Pillar 4e's terminal-integration spec:
  - **Reference analogues studied**: apple/containerization's `SandboxContext.proto` (`ResizeProcess`, `KillProcess`, `CloseProcessStdin`), containerd's `Tasks` service (`ResizePty`, `Kill`, `CloseIO`), and Moby/Docker's HTTP shape (`/exec/:id/resize`, `/exec/:id/start`, `/exec/:id/signal`). The first two are the canonical gRPC designs; Docker's HTTP layout is what users already have muscle memory for.
  - **Protocol shape flipped from v2's bidi-oneof approach to unary side-channel RPCs** keyed by a caller-generated `exec_id` (UUID). Rationale: HTTP/1.1 reverse proxies, AWS ELBs, and Node.js HTTP clients reliably butcher long-lived bidirectional gRPC streams. Both containerd and Apple's SandboxContext settled on unary side-channels for the same reason. `exec_id` over PID avoids PID-wrap disambiguation issues.
  - **Signals via `SignalExec(exec_id, signal)`** with POSIX signal numbers — launch set is SIGINT (2), SIGTERM (15), SIGKILL (9). SIGWINCH specifically is **not** routed through `SignalExec`; resize has its own `ResizeExecTTY(rows, cols)` RPC so the payload is typed.
  - **`SetTime` RPC added**: host suspend/resume causes guest clock drift that breaks TLS handshakes and `make`/`ninja` timestamp comparisons. Cheap to implement, saves a recurring user complaint. P1.
  - **File transfer stays inline over the ExecStream** for now. We deliberately do **not** adopt Apple's dedicated-vsock `ProxyVsockRequest` pattern yet; the firewall/timeout surface is not worth it until users are routinely moving multi-GB files.
  - **HTTP URL shape mirrors Docker exactly** for zero cognitive load: `POST /v1/vms/:name/exec`, `POST /v1/vms/:name/exec/:id/start`, `POST /v1/vms/:name/exec/:id/resize?h=24&w=80`, `POST /v1/vms/:name/exec/:id/signal`. Docker-shaped, not Kubernetes-shaped.
  - **Concrete `proto/agent.proto` diff included** (`ExecRequest.exec_id`, `ExecRequest.tty`, new `ResizeExecTTYRequest/Response`, `SignalExecRequest/Response`, `SetTimeRequest/Response`, three new unary RPCs on the `Agent` service).
  - **Go client surface stays lean**: three new `AgentClient` methods — `ResizeExec`, `SignalExec`, `SetTime` — each a single-call, no Options/Result-struct ceremony. We explicitly reject Docker's verbose options-struct idiom in Go here.
  - **`cove linux shell` wiring spelled out**: generate exec UUID per session, `tty=true`, SIGWINCH handler forwards via `ResizeExec`, SIGINT handler forwards via `SignalExec(2)` with escalation (2nd Ctrl+C → SIGTERM, 3rd → SIGKILL) instead of tearing the stream down.
  - **Pillar 4e PTY wiring line in the scope table bumped from 0.5 day to ~1.5 days** to cover: ExecRequest schema bump (exec_id + tty), guest-side PTY allocation, three new RPCs (ResizeExecTTY, SignalExec, SetTime) including guest-side handler implementations, plus host-side SIGWINCH/SIGINT forwarding. v0.2 total moves from ~13 to ~14 days.
- **v2 (post-round-2 Council review)**: Second-round amendments:
  - **[P0 PTY signals / resize]** The single most critical addition. `proto/agent.proto` lacks channels for SIGWINCH and signals, which breaks `vim`, `htop`, `less`, `tmux`, `k9s`, and any curses tool the moment the user resizes their terminal, and orphans guest processes on Ctrl+C. Added a new Pillar 4e "Terminal integration / PTY requirements" subsection with two new RPCs (`ResizeWindow`, `SendSignal`), host-side SIGWINCH + SIGINT wiring in the `cove linux shell` wrapper, and integration points in `agent_client.go:933` (`UserExecStream`).
  - **[P1 Alpine pulled into v0.2]** Alpine is the HN-demo darling (~2s boot inside VZ). Launch distro set is now 4 (Ubuntu + Debian + Fedora + Alpine). Arch + NixOS remain deferred to v0.3. Alpine installer strategy documented; `LinuxVariant` constant in `linux_installer.go:147` needs Alpine added. Alpine boot time is now the explicit lead of the HN-demo framing.
  - **[P1 Actionable nested-virt error]** Error string updated from the bare `"nested virtualization requires M3/M4 chip on macOS 15+"` to the recoverable `"nested virtualization requires M3/M4 chip on macOS 15+. Run without --nested to boot a standard VM (KVM will be disabled)."` so users recover in one re-run.
  - **[P1 CI failure alerting]** Nightly cron must open a GitHub Issue on build/cosign failure (title: `Nightly image rebuild failed: <distro> <date>`), auto-assigned to @cove-release, with optional Slack/Discord webhook documented as user config. Silent cron failure = stale CVE-vulnerable images, which is unacceptable given our published-image SLA.
  - **[P1 CUDA/Nvidia disclaimer]** Added a prominent "What cove Linux is NOT / Hardware limitations" subsection at the top of Pillar 4 calling out that Apple Silicon does not support PCIe GPU passthrough — no CUDA, no Nvidia drivers, virtio-GPU is software-rendered only.
  - **[P2 Tabs fallback]** md2html does not support JS-driven tabs; don't block launch on it. `docs/features/linux.md` uses sequential `###` headings per distro (Ubuntu / Debian / Fedora / Alpine). Revisit a native tab component in a future docs iteration.
  - **[P2 VirtioFS secondary-user limitation]** Documented: only the primary provisioned user (default UID 1000) gets write access on the auto-mapped mount. Secondary users (UID 1001+) see files but can't write. Doc-noted in the VirtioFS UID/GID section.
  - **[QA Preserve isolated tests]** The mega-test (`integration_linux_nested_test.go`) catches feature-interaction bugs but must run **after** isolated single-feature tests pass. Keep `testLinuxNetwork`, `testLinuxAgent`, etc. as fast-failing unit-ish integration tests; do not replace them with the mega-test.
  - Scope table revised: Pillar 2 now 4 distros (+1 day); PTY wiring +0.5 day. v0.2 total goes to **~13 days**.
- **v1 (post-round-1 Council review)**: Amendments applied from the Council's round-1 verdict:
  - **[P0-A]** Hardware-gate nested virt. M1/M2 physically lack nested virt; calling `SetNestedVirtualizationEnabled(true)` crashes or fails config validation. Added explicit `vz.IsNestedVirtualizationSupported()` check, fail-fast path, per-chip matrix, and the concrete diff against `linux.go:41`.
  - **[P0-B]** Dropped `cove ssh`. Replaced with `cove linux shell <vm>` that wraps the existing `agent-exec-stream --pty /bin/bash` primitive. Removed the per-VM SSH keypair, `~/.cove/ssh/<vm-name>_id_ed25519`, and related plumbing.
  - **[P0-C]** New sub-pillar: VirtioFS UID/GID auto-mapping for Linux guests. Host UID 501 ↔ guest UID 1000 mismatch is currently a silent papercut; `cove run` now appends `uid=/gid=` to `mount_virtiofs` automatically for Linux guests.
  - **[P1-D]** Launch distro set cut from 6 to 3 (Ubuntu + Debian + Fedora). Arch, Alpine, NixOS move to v0.3.
  - **[P1-E]** Pillar 3 gated on a nightly GitHub Actions cron rebuild + cosign signing (keyless via GitHub OIDC). No manual pushes.
  - **[P2-F]** Dropped user-mode networking (gvisor-tap-vsock). CGO surface is inconsistent with cove's pure-Go pitch; deferred to v0.3 with a native implementation.
  - **[KVM test harness P1]** Golden integration test must exercise nested + VirtioFS + Rosetta + vz-agent simultaneously. New target: `integration_linux_nested_test.go`.
  - **[P2 DX]** Linux docs are one page with tabbed code blocks per distro, not per-distro subpages.
  - Scope table revised: v0.2 total is ~12 days (was ~16.5).

## Goal

Take cove's Linux guest support from "works for simple cases" to **the default tool Mac-based developers reach for when they need a Linux VM**, specifically: KVM inside the guest, GPU-ish acceleration where possible, turnkey distros beyond Ubuntu, Rosetta for x86 translation, a genuinely fast `cove up --linux` path, and an image registry analogous to `cove pull` for macOS.

The underlying observation: Apple Silicon with Virtualization.framework is, per-core, the fastest Linux VM host most developers own. But nobody reaches for it. Docker Desktop gets the container traffic, UTM gets the "I need a Linux desktop" traffic, and Parallels gets the commercial traffic. Cove's opening is **nested virt (KVM inside Linux inside VZ)** — Apple enabled this on M3/M4 and it's underexploited.

## Non-goals

- x86 guests at full speed. Rosetta translation is good-enough for dev tooling, not compute workloads.
- Replacing Docker Desktop for container workflows. We're going after VM users, not container users (though "cove + k3s in a guest" is a fine outcome).
- GUI Linux desktops as a core use case. We document how, but the focus is headless + shell + KVM development.
- Supporting every distro at launch. v0.2 ships Ubuntu, Debian, Fedora, Alpine. Arch and NixOS land in v0.3. Anything else can happen community-driven.
- macOS x86 guest support. Out of framework's scope.

## Today's state

### What works
- **Ubuntu Server 24.04 ARM64** via cloud-init autoinstall (`linux_installer.go`). Fully unattended install.
- **EFI boot** via `VZEFIBootLoader` with NVRAM variable store.
- **Direct kernel boot** via `VZLinuxBootLoader` when kernel+initrd are provided.
- **Virtio-blk, Virtio-net NAT, Virtio-sound, Virtio-console, Virtio-rng, Virtio-balloon**. Standard guest surface.
- **VirtioFS shared folders** (but see known issue: host user UID/GID mismatch, TCC blocking vz-agent).
- **Rosetta for x86-64** binaries in ARM64 Linux guests (via `rosetta.go`; guest registers Rosetta as an ELF interpreter).
- **Serial console** to stdout by default.
- **vz-agent** over vsock with the same protocol as macOS guests (gRPC service). This is the shell-access primitive; no SSH needed.

### What's broken or missing

1. **Nested virtualization is disabled.** `linux.go:41` detects `NestedVirtualizationSupported()` on M3/M4 but never actually calls `SetNestedVirtualizationEnabled(true)`. The comment says a previous attempt "tripped into undefined-instruction crashes in overlayfs." We need to reinvestigate — this was fixed upstream in Ubuntu 24.04.1+ and kernel 6.8+. M1/M2 hardware physically lacks the feature; any code path that flips the bit must hardware-gate first.
2. **Only Ubuntu installer.** No Debian, Fedora, Alpine. cloud-init NoCloud is universal but each distro has its own autoinstall idiom (Debian preseed, Fedora kickstart, Alpine `setup-alpine` answers file). Arch and NixOS deferred to v0.3.
3. **No OCI image distribution for Linux guests yet.** v0.1 OCI work ships for macOS; Linux should be *easier* (smaller images, faster pushes, no SLA concerns). Must ship with a nightly rebuild cron + cosign signing pipeline, not manual pushes.
4. **Slow first boot.** cloud-init autoinstall is ~8-15 minutes for Ubuntu. Installing multiple distros = multiple coffee breaks.
5. **VirtioFS UID/GID mismatch is a silent footgun.** Host UID (typically 501) ≠ guest UID (typically 1000). Users hit Permission denied and reach for `chmod -R 777` or manual mount options.
6. **Rosetta mount is manual post-install.** User must run `sudo mkdir -p /run/rosetta && sudo mount -t virtiofs ...`. Should be a vzscript.
7. **No GPU path.** Virtio-GPU works but is software-rendered in the guest; no Metal passthrough, no Paravirtualized GL. Apple's VZMacGraphicsDeviceConfiguration is macOS-only.
8. **No cgo-free cloud-init ISO builder.** We shell out to `hdiutil` and `mkisofs`/`genisoimage` if available (`linux_installer.go`). Both optional; failure path is opaque.
9. **Known pain**: host UID on VirtioFS mounts doesn't match any sane guest user by default. vz-agent running as root via LaunchDaemon can't see host-owned files in the mount because TCC blocks Full Disk Access on daemons.
10. **No "cove run -distro fedora"** shortcut. `cove install -linux` takes many flags; users copy-paste ~10-line invocations.

---

## The pitch: "Developer's Linux on Mac"

Four pillars. Each is a v0.2 deliverable with clear tests.

### Pillar 1: KVM works (nested virtualization)

**What**: `cove up --linux --nested` produces a Linux VM in which `kvm-ok` reports KVM-enabled, `lsmod | grep kvm` shows `kvm_arm64`, and running a second nested Alpine VM inside boots in <10s.

**Why this matters more than any other pillar**:
- `minikube`, `k3d`, `kind` all prefer KVM when available. Without nested virt, you hit QEMU-TCG slowness.
- Integration tests for tools that drive libvirt, firecracker, cloud-hypervisor, or Podman Machine need KVM inside the VM.
- Kernel developers testing ARM64 guests get full cycle times.
- It's the single feature most requested of UTM/lume that neither does well.

**Hardware gating — critical prerequisite**:

Nested virtualization is a physical CPU feature. M1 and M2 chips **do not have it**. Calling `SetNestedVirtualizationEnabled(true)` on an M1/M2 host crashes during VM configuration validation or aborts at boot. The existing helper `vz.IsNestedVirtualizationSupported()` (defined at `osversion.go:360`, backed by `virtualization_15.m:450`) is the gate — it returns `false` on M1/M2 and on hosts below macOS 15, and `true` only on M3/M4 running macOS 15+.

Per-chip feature matrix:

| Chip | macOS 14 | macOS 15 | Notes |
|------|----------|----------|-------|
| M1 (all variants) | No | No | Hardware lacks nested virt |
| M2 (all variants) | No | No | Hardware lacks nested virt |
| M3 / M3 Pro / Max | No | **Yes** | Requires macOS 15.0+ |
| M4 / M4 Pro / Max | No | **Yes** | Requires macOS 15.0+ |

The current stale comment in `linux.go` around line 41:

```go
// TODO: SetNestedVirtualizationEnabled(true) tripped into undefined-instruction
// crashes in overlayfs; re-enable after reproducing on kernel 6.8+.
// if vz.IsNestedVirtualizationSupported() {
//     config.SetNestedVirtualizationEnabled(true)
// }
```

Proposed diff:

```go
// linux.go  (around line 41)
-   // TODO: SetNestedVirtualizationEnabled(true) tripped into undefined-instruction
-   // crashes in overlayfs; re-enable after reproducing on kernel 6.8+.
-   // if vz.IsNestedVirtualizationSupported() {
-   //     config.SetNestedVirtualizationEnabled(true)
-   // }
+   if opts.Nested {
+       if !vz.IsNestedVirtualizationSupported() {
+           return nil, fmt.Errorf("nested virtualization requires M3/M4 chip on macOS 15+. Run without --nested to boot a standard VM (KVM will be disabled).")
+       }
+       config.SetNestedVirtualizationEnabled(true)
+   }
```

`opts.Nested` is wired from the `--nested` flag on `cove run` / `cove up`. Absent the flag we never flip the bit, so M1/M2 users (the majority of the installed base today) pay zero cost.

**Work**:
1. Replace the stale comment with the hardware-gated call above. Verify on an M3 Pro host; confirm `vz.IsNestedVirtualizationSupported()` returns `false` on an M2 host and surface the error cleanly.
2. Reproduce the old "overlayfs crash" on current Ubuntu 24.04.2 + kernel 6.8+. If still present, keep `--nested` behind a big warning; if gone, promote to the default when hardware supports it.
3. Guest-side vzscript `enable-kvm` that installs `libvirt-daemon-system`, `qemu-system-arm`, and joins the user to `kvm` + `libvirt` groups.
4. Document the known limits: nested guest memory is carved out of the outer VM; nested guest can't itself nest; perf is ~70-80% of native KVM on bare metal.
5. **Golden integration test — nested + VirtioFS + Rosetta + vz-agent simultaneously.** New target `integration_linux_nested_test.go`. The test:
   - Boots Ubuntu 24.04 ARM64 with `--nested`.
   - Mounts a host directory via `-v` (exercising the P0-C uid/gid auto-map path).
   - Executes a host-mounted x86_64 binary via Rosetta (exercising `-rosetta`).
   - Runs `kvm-ok` inside the guest via the vz-agent shell primitive and asserts KVM-enabled.
   - Boots a second nested Alpine VM and asserts it comes up.
   This single test catches regressions across every feature introduced in this design.

**Estimate**: 3 days (includes the combined test harness).

### Pillar 2: The distro buffet (v0.2: four distros)

**What**: `cove install -linux -distro <ubuntu|debian|fedora|alpine>` works for all four, unattended. Ubuntu/Debian/Fedora complete in under 10 minutes each; **Alpine completes in under a minute and boots cold in ~2 seconds inside VZ** — the HN-demo headline feature.

**Why four, and why Alpine is the lead HN demo**:
- Alpine inside VZ boots cold in ~2 seconds. That's the video we put at the top of the launch post. Nothing else in the Apple-Silicon-Linux-VM space gets close.
- The rest of the set covers ~90% of enterprise and dev workloads (Ubuntu/Debian/Fedora).
- Four curated distros is already more ambitious than Lume (which ships exactly one — Ubuntu) and more trustworthy than UTM (community gallery, no curation).
- Six distros = six autoinstall idioms against upstream drift. Each one decays independently, each one becomes a maintenance trap. Arch + NixOS stay on the v0.3 roadmap.

**Why (per distro)**:
- Ubuntu default is fine for some; painful for others (systemd baggage, snap, late LTS releases). A Fedora or Debian user faced with an Ubuntu-only tool walks away.
- Alpine is the fastest-booting Linux in VZ, the smallest image, the obvious first-screen of any benchmark post. Ships the HN narrative.
- Standard surface: every distro uses cloud-init or a distro-native autoinstall. We pick the right one per distro.

**Work**:
1. Abstract `linux_installer.go` to have a per-distro strategy interface. The interface ships with four implementations in v0.2:
   ```go
   type DistroInstaller interface {
       Name() string // one of: "ubuntu", "debian", "fedora", "alpine"
       DownloadImage(ctx, arch, dest) error
       BuildAutoinstallData(opts ProvisionOpts) (userData, metaData []byte, err error)
       KernelCmdline() string // for direct-boot path, if supported
   }
   ```
2. Add Alpine to the `LinuxVariant` constant at `linux_installer.go:147`. Today that constant enumerates the supported distros; Alpine must be wired through the install/run codepaths and the `-distro` flag's allowed values.
3. Implement installers (v0.2):
   - Ubuntu: existing cloud-init NoCloud path. Refactor to interface.
   - Debian: preseed late-command + automatic d-i netcfg. Or cloud-init for Debian 12+ (bookworm) cloud image.
   - Fedora: kickstart via kickstart-on-CDROM + Fedora cloud image base.
   - **Alpine**: boot the Alpine ARM64 ISO, inject a `setup-alpine` **answers file overlay** (Alpine's native unattended-install format — a simple KEY=value file that `setup-alpine -f` consumes end-to-end). The overlay carries hostname, keyboard, timezone, mirror, SSH-enablement, disk target, and the provisioned user account. Done.
4. v0.3 additions (out of scope for this design's delivery):
   - Arch: archinstall config profile. Boot Arch ISO, cloud-init runs archinstall with answers file.
   - NixOS: NixOS installer image + `/etc/nixos/configuration.nix` derivation written via NoCloud.
5. Each v0.2 distro gets a `cove install -linux -distro X` test on CI. Golden image artifact archived.
6. Publish pre-built images for the four v0.2 distros to `ghcr.io/tmc/cove-linux-<distro>:<version>` so `cove pull` is the one-command path. (See Pillar 3 for publishing pipeline.)

**Estimate**: 4 days total for four installers + CI wiring (Alpine adds ~1 day over the v1 three-distro scope; the `setup-alpine` answers-file path is simpler than cloud-init but still needs its own CI job + image publishing).

### Pillar 3: OCI distribution works for Linux, too (and faster)

**What**: `cove pull ghcr.io/tmc/cove-linux-fedora:40` creates a Fedora VM in under 2 minutes including the download of a ~1.5 GB image.

**Why**:
- Linux images are ~1-3 GB compressed. Much smaller than macOS (10-25 GB). Pull UX is coffee-cup, not lunch-break.
- CI wins are disproportionate: pull a known-good image per job, tear down. No per-runner distro install.
- Zero Apple SLA concerns; the tool can reasonably publish canonical images under the cove project's namespace for Linux.

**CI pipeline — non-negotiable ship gate**:

Pillar 3 does not ship until the automated pipeline is merged. No manual `docker push` ever. The pipeline:

1. **GitHub Actions cron (nightly)** rebuilds each published image from the upstream distro base. Ubuntu nightly tracks `ubuntu-24.04` cloud image; Debian tracks `debian-12-genericcloud-arm64`; Fedora tracks the latest `Fedora-Cloud-Base`; Alpine tracks the latest `alpine-virt-<version>-aarch64.iso`.
2. **Build step** uses `cove build` with the upstream base as input; tags output as `cove-linux-<distro>:<version>` and `:latest`.
3. **Signing** uses `cosign` keyless via GitHub OIDC. Every pushed image and manifest is signed; `cove pull` verifies signatures by default.
4. **CVE SLA**: by publishing these images under our namespace, cove owns the support cadence. The nightly rebuild is the mechanism that keeps us out of the "outdated Fedora sitting on ghcr forever" trap.
5. **Provenance**: SLSA provenance attestations attached via `cosign attest`. Users can verify the image was built from the expected commit.
6. **Failure alerting — non-negotiable.** Silent cron failure means stale, CVE-vulnerable images served to users. The workflow must include:
   - On build or cosign failure, a GitHub Actions step that opens a GitHub Issue in the cove repo using `gh issue create` or `actions/github-script`. Title: `Nightly image rebuild failed: <distro> <date>`. Body includes the failing job link and the tail of the build log.
   - Issue auto-assignment to the `@cove-release` team (label: `release-blocker`).
   - Optional Slack/Discord webhook step gated on a `NOTIFY_WEBHOOK_URL` repo secret; document this as user config in `docs/features/linux.md` so downstream forks can wire their own channel.
   - Rationale: we own the CVE SLA on published images. Staleness detected by a user is a miss; staleness detected by our own pipeline is the expected behavior.

**Work**:
1. Ensure v0.1 OCI push/pull works the same way for Linux VMs. (Mostly free — the push/pull path is disk-image-level, not OS-aware.)
2. Standardize the manifest: `org.tmc.cove.os` annotation = `linux`, `org.tmc.cove.distro` = `fedora-40`. Lets `cove pull` pick the right boot loader (EFI vs direct-kernel) automatically.
3. **Wire the nightly rebuild + cosign pipeline + failure-issue opener as a GitHub Action.** Publishes the four v0.2 distro images under `ghcr.io/<cove-org>/cove-linux-<distro>:<version>`. Failure path opens a tracked Issue. Ship gate for this pillar.
4. `cove linux images list` shows available official images. First-class, not buried.
5. Document the exact reproducible-build recipe — users can verify the published images match their own builds.

**Estimate**: 3 days (OCI wiring + nightly cron + cosign signing).

### Pillar 4: User-experience polish

**What**: `cove up --linux` Just Works for 80% of users without extra flags.

#### What cove Linux is NOT — hardware limitations

Before the features: a deliberately up-front list of what cove Linux **cannot** do on Apple Silicon. Surfacing these in docs and in Pillar 4's deliverable prevents the most common user-confusion threads and protects the launch-thread narrative.

- **No PCIe GPU passthrough.** Apple Silicon does not expose PCIe devices to Virtualization.framework guests. You cannot run CUDA, Nvidia drivers, or any PCI-attached GPU inside a cove Linux VM. For ML/GPU workloads, use cloud GPUs. Virtio-GPU provides software-rendered graphics only.
- **No hardware passthrough in general** — no PCIe devices, no direct-attached disks, no discrete Thunderbolt passthrough.
- **No macOS-style Metal acceleration in the guest.** `VZMacGraphicsDeviceConfiguration` is macOS-guest-only; Linux guests get virtio-GPU's software-rendered path.
- **Nested virt is M3/M4-only on macOS 15+** (see Pillar 1). M1/M2 users get a standard Linux VM without KVM.
- **x86 Rosetta is for compatibility, not performance.** It runs developer tools (compilers, CLIs, language runtimes) fine; it is not an x86 compute platform.

This list goes verbatim near the top of `docs/features/linux.md` and is referenced from `comparison.md`. We'd rather ship with an explicit "no" than field a dozen "why can't I get CUDA working" issues.

Sub-items:

**4a. Distro shortcut on `cove run`**: `cove run --linux --distro fedora` (same semantics as macOS `cove up -user`).

**4b. Rosetta auto-mount**: a default vzscript `rosetta-setup.vzscript` that runs on first boot:
```
guest-exec mkdir -p /run/rosetta
guest-exec mount -t virtiofs rosetta /run/rosetta
guest-exec /run/rosetta/rosetta --register
```
Wired to `cove up --linux -rosetta` so users don't hand-craft it.

**4c. Networking (v0.2 ships NAT + bridged; user-mode deferred)**: v0.1's NAT and bridged paths are preserved. User-mode networking (gvisor-tap-vsock) is **dropped from v0.2**. Rationale:
- Adds CGO and an external Go dependency surface inconsistent with cove's pure-Go / cgo-free pitch.
- `network_filehandle.go` already handles raw-frame capture for the bridged path.
- For users on restrictive VPNs: document `-network none` + vsock agent access as the supported path — the vz-agent primitive (Pillar 4e) removes most of the "I need network just to get a shell" pressure.
- Revisit in v0.3 with a native/cgo-free implementation, or drop permanently if the vsock path is enough.

**4d. Faster cold boot**: use direct kernel boot (VZLinuxBootLoader) by default on subsequent boots instead of EFI — saves ~10-15s. We already stage kernel+initrd during install (`loadInstalledLinuxBootArtifacts`); use them unconditionally unless the user set `-efi`.

**4e. Shell access via vz-agent**: `cove linux shell <vm>` wraps the existing `agent-exec-stream --pty /bin/bash` primitive.

- Implementation: thin wrapper over the code path in `ctl.go:884`, which is backed by `UserExecStream` in `agent_client.go:1053`. The vz-agent already provides a PTY-attached exec over vsock; `cove linux shell` is a CLI-ergonomic wrapper.
- Zero network config. Zero SSH keys. Zero `authorized_keys` management.
- Works under corporate VPNs that block arbitrary ports.
- Survives host sleep cleanly (the vsock connection re-establishes; no stale TCP state).
- Why SSH-over-vsock is architecturally redundant: cove already runs vz-agent on every provisioned Linux VM. Running sshd inside the guest, generating keypairs on the host, managing `authorized_keys` file injection, handling key rotation, reserving a vsock port for sshd — all of that duplicates a primitive we already own. The vz-agent exec path is the right primitive; SSH-over-vsock is the wrong layer for cove to own.
- Consequence: we do **not** generate `~/.cove/ssh/<vm-name>_id_ed25519`. Users who want SSH can still install and configure it themselves; cove doesn't opinionate that path.

##### Terminal integration / PTY requirements (P0, the single most critical addition — v3 rewrite post-agent-API review)

The existing `cove linux shell` primitive is functional for non-interactive commands and for shells that never resize and never receive signals. That is not the real world. `proto/agent.proto` today has no channel for SIGWINCH, no channel for guest signals, and no way to address an in-flight exec out-of-band. The v2 version of this doc proposed a bidi-oneof extension to the existing `UserExecStream`. After reviewing the canonical prior art, we're reversing that decision.

**Reference analogues we studied**:

- **apple/containerization `SandboxContext.proto`** (Apple's own Swift-first container runtime on VZ). Uses unary RPCs `ResizeProcess(process_id, rows, cols)`, `KillProcess(process_id, signal)`, `CloseProcessStdin(process_id)` — side-channel to a separately-established stdio stream. `process_id` is a caller-generated string. Notable: Apple also has a `ProxyVsockRequest` RPC that punches a **dedicated vsock channel** for file-transfer workloads rather than multiplexing them over the exec stream.
- **containerd `Tasks` service** (`/containerd/api/services/tasks/v1/tasks.proto`): `ResizePty(container_id, exec_id, width, height)`, `Kill(container_id, exec_id, signal, all)`, `CloseIO(container_id, exec_id, stdin)`. Also unary side-channel, also keyed by caller-generated IDs. This is the design that every runtime (runc, crun, Kata) has converged on.
- **Moby/Docker HTTP API**: `POST /exec/:id/resize?h=<rows>&w=<cols>`, `POST /exec/:id/start` (hijacks connection for stdio), `POST /exec/:id/signal`. The URL shape is what every CLI tool user already has muscle memory for.

The consistent verdict across all three: **bidi streams are where portability goes to die**. HTTP/1.1 reverse proxies, AWS ELBs, and Node.js HTTP clients all mishandle long-lived bidirectional gRPC streams in ways that bite in production. Unary side-channel RPCs keyed by an opaque ID are the right shape.

**Design decisions (verdicts we're adopting)**:

1. **Unary side-channel RPCs, not bidi-oneof**. We add `ResizeExecTTY`, `SignalExec`, `SetTime` as plain unary RPCs. The existing stdio path (`ExecStream`) stays bidi, but control operations leave it.
2. **Addressing by caller-generated UUID `exec_id`, not PID**. PIDs wrap and can't disambiguate a rapid-fire exec/kill/relaunch. The client generates a UUIDv4 per exec and passes it into `ExecRequest`; every subsequent control RPC references that `exec_id`.
3. **Signal forwarding via `SignalExec(exec_id, signal_int)` with POSIX signal numbers**. Ship SIGINT (2), SIGTERM (15), SIGKILL (9) at launch. SIGWINCH specifically is **not** routed here — it has a typed payload, so it gets its own RPC (`ResizeExecTTY` with `rows`/`cols`).
4. **HTTP URL scheme mirrors Docker exactly**. The `cove serve` v2 docs endpoint shape is `POST /v1/vms/:name/exec`, `POST /v1/vms/:name/exec/:id/start`, `POST /v1/vms/:name/exec/:id/resize`, `POST /v1/vms/:name/exec/:id/signal`. Zero cognitive load for anyone who's ever used `docker exec`.
5. **File transfer stays inline over the ExecStream for now**. We do **not** adopt Apple's `ProxyVsockRequest` dedicated-vsock pattern yet — punching additional vsock channels adds firewall and timeout complexity we don't need until multi-GB transfers become a routine workflow. Revisit when the data says to.
6. **`SetTime` RPC pulled in for free**. Host suspend/resume causes guest clock drift. That drift breaks TLS handshakes (cert validity windows), `make`/`ninja` (file-timestamp comparisons go negative), and log timestamps. One RPC, trivial guest-side implementation (`clock_settime(CLOCK_REALTIME, ...)`), handles it. P1.

**Concrete `proto/agent.proto` diff**:

```protobuf
message ExecRequest {
+  string exec_id = 10;   // caller-generated UUID
   repeated string args = 1;
   // ... existing fields ...
+  bool tty = 6;          // request pseudoterminal
}

+message ResizeExecTTYRequest {
+  string exec_id = 1;
+  uint32 rows = 2;
+  uint32 cols = 3;
+}
+message ResizeExecTTYResponse {}

+message SignalExecRequest {
+  string exec_id = 1;
+  int32 signal = 2;   // POSIX signal: 2=SIGINT, 9=SIGKILL, 15=SIGTERM
+}
+message SignalExecResponse {}

+message SetTimeRequest {
+  google.protobuf.Timestamp time = 1;
+}
+message SetTimeResponse {}

service Agent {
  // ... existing Ping, Info, Exec, ExecStream ...
+  rpc ResizeExecTTY(ResizeExecTTYRequest) returns (ResizeExecTTYResponse);
+  rpc SignalExec(SignalExecRequest) returns (SignalExecResponse);
+  rpc SetTime(SetTimeRequest) returns (SetTimeResponse);
}
```

##### Transport: connect-go + buf

The cove agent gRPC service and the `cove serve` HTTP API run on the **same connect-go handler**. connect-go serves three wire protocols from one codepath:

- **Native gRPC over HTTP/2** — the cove CLI's preferred transport.
- **gRPC-Web** — browser-friendly; lets a future cove web console talk to the agent without a proxy.
- **Connect's HTTP/JSON** — curl-friendly, proxy-friendly; the path every non-Go client (Node.js MCP example, Python CI scripts, ad-hoc `curl`) takes.

Everyone shares **one `.proto`**. Node.js / Python / `curl` clients hit the JSON path; cove CLI uses native gRPC for latency. Cove already has `proto/agentpbconnect/agent.connect.go` in the tree, so no new dependency is pulled in for v0.2.

**Docker-shaped URLs via http.ServeMux wrapper**: connect-go's default URL is `/cove.v1.Agent/ResizeExecTTY`. We wrap with an `http.ServeMux` that maps `/v1/vms/:name/exec/:id/resize` → the internal connect call. Best of both: Docker-compatible URLs externally, connect machinery internally. The URL shape is stable across transports.

**buf tooling — fully local generation, no buf.build registry dependency**:

- `buf generate` produces the Go stubs we commit; replaces the ad-hoc `protoc` invocation. The generator set is entirely local: `protoc-gen-go`, `protoc-gen-connect-go`, `protoc-gen-es`, `protoc-gen-connect-es`. All are installed via `go install` or `npm install` — no network call to buf.build at generate time.
- `buf lint` enforces proto backwards-compatibility (field-number reservations, no breaking renames) on every PR that touches `proto/`. Pure local tool.
- **Published TypeScript / Python / Swift / Kotlin clients come from release-time `buf generate` runs**, archived alongside the release as artifacts (e.g. `cove-clients-v0.2.tgz`). Users `npm install` or `pip install` the archived artifact; no dependency on the buf.build registry being up at install time.
- The buf.build schema-registry remote-client feature remains an **optional convenience** for users who want auto-updating SDKs tracking `main`, but cove's own CI, release pipeline, and docs examples do not depend on it. The Council's vendor-risk flag is resolved by this self-hosted path.

**No CGO**: connect-go is pure Go. Preserves cove's cgo-free pitch (the same reason we dropped gvisor-tap-vsock in v1).

##### Why unary only (and not bidi)

We considered adding `ExecInteract(stream ExecInteractRequest) returns (stream ExecInteractResponse)` as an opt-in fast path for capable clients (v4's approach). We chose **not** to. The honest reasoning:

- **No localhost latency win.** 99% of cove traffic is localhost. RTTs are µs. Unary vs bidi is invisible on this path. The "SIGWINCH drops from 10ms to 2ms" number only appears over a tunnel, and even there the human eye does not perceive it.
- **Simpler auth model.** Unary RPCs get per-call auth. A bidi stream holding an open connection means one auth check at stream-open covers every subsequent signal — including `SignalExec(9)` / SIGKILL. That is a strictly worse security posture: a compromised or stale token can kill processes for as long as the stream lives. Unary forces re-auth per control operation.
- **Corporate egress proxies still break bidi.** The 2023-era "proxies universally break bidi" framing is partially stale in 2026 — managed cloud LBs (AWS ALB since November 2023, Cloudflare since 2019, GCP for years) handle bidirectional HTTP/2 streams fine. But corporate egress is a different story: **Zscaler, Netskope, Island, and some on-prem nginx (<1.13.10)** still request-body-buffer outbound HTTP/2, which silently hangs bidi streams rather than returning a clean `Unimplemented`. For cove's future "cove CLI → remote `cove serve` over corporate network" use case, silent hang is the worst failure mode. Unary HTTP/1.1-compatible calls degrade predictably; bidi degrades invisibly.
- **Bidi is strictly additive.** If v0.4 telemetry on remote `cove serve` shows users would materially benefit from a multiplexed control stream, we can add `ExecInteract` as an opt-in RPC without breaking any v0.2 client. Nothing we decide today locks that door.

We're not claiming "proxies are universally broken" — that's the 2023 reading. The honest 2026 read is **bidi buys us nothing we need for localhost, introduces an auth-scope regression, and the only environment it would help (corporate-network remote serve) is exactly the one environment where enterprise egress proxies still break it**. Unary is the right default; bidi remains a clean additive option for later if demand materializes.

Guest-side handlers (in `cmd/vz-agent`):
- `ResizeExecTTY`: look up exec by `exec_id`, call `ioctl(ptyFd, TIOCSWINSZ, &winsize{rows, cols})`.
- `SignalExec`: look up exec by `exec_id`, `syscall.Kill(-pgid, syscall.Signal(sig))` (negative PID → process group, so signals hit the whole pipeline, not just the leader).
- `SetTime`: `clock_settime(CLOCK_REALTIME, timestamp)`. Requires CAP_SYS_TIME; the vz-agent LaunchDaemon already runs as root in the guest.

**Go client surface** (`agent_client.go` additions — keep it lean, explicitly reject Docker's verbose Options/Result struct idiom for Go; v5 is unary-only):

```go
// agent_client.go — unary control path (proxy-safe, works on HTTP/1.1 and HTTP/2)
func (c *AgentClient) ResizeExec(ctx context.Context, execID string, rows, cols uint32) error
func (c *AgentClient) SignalExec(ctx context.Context, execID string, signal int32) error
func (c *AgentClient) SetTime(ctx context.Context, t time.Time) error
```

Three unary methods, three positional args max. Output still flows over the existing server-streaming `ExecStream` RPC; the unary methods above handle all control-plane operations.

**`cove linux shell` wiring (v5 — unary-only, no capability probe)**:

1. On shell startup, generate a UUIDv4 per session and POST an `ExecCreateRequest` with `tty=true` and the caller-generated `exec_id`. Server acknowledges with `{"id": "<uuid>"}`.
2. Start the existing server-streaming `ExecStream` RPC for stdout/stderr. This is the v3 shape and is unchanged.
3. Before the first stdin byte, read the host TTY size via `ioctl(TIOCGWINSZ)` and send `ResizeExec(ctx, execID, rows, cols)`. Handles the "host terminal was already resized before launch" case.
4. Register a `SIGWINCH` handler on the host. On every fire, re-read the TTY size and call `ResizeExec(ctx, execID, rows, cols)`. If the call ever returns `Unimplemented` (belt-and-suspenders — a correctly-deployed v0.2 agent will never do this), log a warning once and stop forwarding resizes for this session. No debounce unless the stream backs up.
5. Register a `SIGINT` handler on the host. **Swallow** the local SIGINT (do not let it tear down the stream). Forward as `SignalExec(execID, 2)` to the guest. Core fix for the "Ctrl+C orphans guest processes" bug.
6. **Signal escalation on repeated Ctrl+C**: if the user hits Ctrl+C again within a short window (say, 2 seconds) and the guest hasn't exited, escalate: second Ctrl+C → `SignalExec(execID, 15)` (SIGTERM). Third → `SignalExec(execID, 9)` (SIGKILL). Matches what users already expect from `kill` / `systemctl stop`.
7. On normal shell exit (user types `exit` or the guest process terminates cleanly), let `ExecStream` end naturally. **No explicit signal** — the guest's own exit is the event.
8. Document the full signal-forwarding + escalation matrix in `docs/features/linux.md` next to the shell example.

**HTTP surface (cove serve v2 docs update)** — mirror Docker exactly so every Docker user is instantly fluent. (Unchanged from v3; v4 added the `http.ServeMux` wrapper that maps these Docker-shaped URLs to internal connect calls. v5 drops the bidi-related note but keeps the URL shape.)

- `POST /v1/vms/:name/exec` — body is `ExecCreateRequest`-shaped (args, env, tty, workdir, user); server generates or accepts an `exec_id`; returns `{"id": "<uuid>"}`.
- `POST /v1/vms/:name/exec/:id/start` — hijacks the HTTP connection for bidirectional stdio streaming over HTTP/2, or server-stream fallback over HTTP/1.1. (The HTTP-hijack pattern is the one Docker-compatibility nuance worth preserving; CLIs already handle it.)
- `POST /v1/vms/:name/exec/:id/resize?h=24&w=80` — SIGWINCH equivalent.
- `POST /v1/vms/:name/exec/:id/signal` — body `{"signal": 2}`. Numeric POSIX signals.

The URL shape above is stable across transports (native gRPC, gRPC-Web, Connect HTTP/JSON). External URLs stay Docker-shaped; internal machinery stays connect-native.

**P0/P1 scope breakdown**:

- **[P0]** `exec_id` + `tty` fields on `ExecRequest` + guest-side PTY allocation.
- **[P0]** `ResizeExecTTY` RPC (proto + guest handler + client method).
- **[P0]** `SignalExec` RPC (proto + guest handler + client method).
- **[P0]** `cove linux shell` host-side SIGWINCH + SIGINT forwarding with escalation ladder.
- **[P0]** Connect-go handler + `http.ServeMux` Docker-URL wrapper (replaces v3's hand-rolled HTTP adapter; net scope reduction).
- **[P1]** `SetTime` RPC (proto + guest handler + client method + optional auto-call on host resume-from-suspend).
- **[P1]** `cove-serve` HTTP routes matching the Docker shape (exec/start/resize/signal).

**Why this is in the P0 shipping gate**: without PTY resize + signal forwarding, `cove linux shell` is a toy. With it, it's the best Linux-VM shell UX on Apple Silicon — and the protocol surface tracks the canonical designs from Apple, containerd, and Docker rather than inventing a new shape. This is the one bug class that would dominate the HN comment thread if we shipped without it.

**4f. Tmux/session-first**: `cove linux shell <vm> --tmux` opens a tmux session inside the `agent-exec-stream --pty` path, with nice defaults (timesync, colored prompt, journalctl aliases). Tmux state persists across shell invocations.

**4g. VirtioFS UID/GID auto-mapping for Linux guests (P0)**:

**Problem**: Host UID on macOS (typically 501 for the first user) does not match the guest's provisioned user (typically UID 1000 for the first non-system user on Ubuntu/Debian/Fedora). Mount `~/projects` into a Linux guest via `-v` and the guest `ubuntu` user gets Permission denied on writes. Today, users work around this with `chmod -R 777` on their project directory, or with manual `uid=`/`gid=` mount options that they have to remember on every run. This is the single biggest papercut in the "mount my host code into a Linux VM" workflow.

**Fix**: for Linux guests, `cove run` must **automatically** append `uid=<guest-user-uid>,gid=<guest-user-gid>` to the `mount_virtiofs` options. The guest UID/GID come from the user account created during install (recorded in the VM's `config.json`). If the config.json has no explicit mapping (e.g. older VMs), fall back to `1000:1000` — the standard first-non-system-user convention on every v0.2 distro.

**Implementation sketch**:
- Extend the VirtioFS mount path for Linux guests only — macOS guests don't mount virtiofs the same way, so this is a Linux-only branch.
- Read `GuestUserUID` / `GuestUserGID` from the VM's `config.json` (populated during `linux_installer.go`'s provisioning step).
- Default `1000:1000` if those fields are absent.
- Generate the `mount_virtiofs -o uid=<uid>,gid=<gid>` command in the auto-mount vzscript.
- Add one integration test case: mount a host directory, `touch` a file from the guest user, verify success.

**Limitation — document in `docs/features/linux.md`**: only the **primary provisioned user** (default UID 1000) gets write access on the auto-mapped mount. Secondary users created inside the guest (UID 1001+) will see files but can't write them, because `uid=`/`gid=` on a FUSE-style filesystem pins ownership to a single UID/GID pair at mount time. Guidance for users:

- For single-user dev workflows (the common case): use the primary provisioned user. This is what `cove up --linux` sets up.
- For multi-user guests: apply group permissions inside the guest (`chmod g+w`, add secondary users to the primary user's group) or mount additional VirtioFS tags with different UID mappings.
- This is a VirtioFS/FUSE limitation, not a cove one; it's the same behavior you'd get mounting a macOS-shared folder in a Linux VM anywhere else.

**Why P0**: "mount your project into a Linux VM" is a core cove pitch. It's broken today without this fix. Any user who tries it hits Permission denied and bounces.

**Estimate for Pillar 4 (total, v5 revision)**:
- 4b Rosetta auto-mount + 4d direct-kernel default: 1 day
- 4e Shell via vz-agent (thin wrapper over existing primitive): 0.5 days
- 4e PTY wiring — `ExecRequest` schema bump (`exec_id` + `tty`), guest-side PTY allocation, three unary RPCs (`ResizeExecTTY`, `SignalExec`, `SetTime`) with proto + guest handlers + Go client methods, host-side SIGWINCH/SIGINT forwarding with escalation ladder: 1.5 days
- 4e connect-go + buf adoption with fully-local generation (one handler serves gRPC + JSON + gRPC-Web; Docker-shaped URLs via `http.ServeMux` wrapper; protoc-gen-es et al. installed locally, published client SDKs archived as release artifacts, no buf.build registry dependency): **-1 day net** (replaces v3's hand-rolled HTTP adapter scope)
- 4g VirtioFS UID/GID auto-map: 0.5 days

**Net Pillar 4 total: 2.5 days** (v4 was 3.0 with the bidi handler; v5 drops that +0.5 day).

---

## Local KVM testing — the bit that unlocks everything

We can't ship Pillar 1 on vibes. We need a CI-ish harness that boots a cove VM with nested virt, installs libvirt/KVM inside, runs a nested Alpine VM, and asserts it came up. On developer laptops, the same harness lets contributors reproduce.

### Design

A new script at `vzscripts/kvm-test.vzscript`:

```
# kvm-test — verify KVM works inside a cove Linux guest.
# deps: ubuntu-kvm-userspace

guest-exec kvm-ok
guest-exec lsmod | grep kvm_arm
guest-shell bootstrap.sh

-- bootstrap.sh --
#!/bin/bash
set -eu

# Install virt stack.
apt-get update -q
DEBIAN_FRONTEND=noninteractive apt-get install -y \
  qemu-system-arm libvirt-daemon-system libvirt-clients bridge-utils virtinst

# Pull a tiny Alpine ARM64 image.
curl -fsSL -o /tmp/alpine.iso https://dl-cdn.alpinelinux.org/alpine/v3.20/releases/aarch64/alpine-virt-3.20.3-aarch64.iso

# Boot it.
virt-install \
  --name nested-alpine \
  --memory 512 --vcpus 1 --os-variant alpinelinux3.16 \
  --cdrom /tmp/alpine.iso --disk size=4 \
  --graphics none --noautoconsole --wait 120

# Verify it's up.
virsh list | grep nested-alpine | grep running
```

CI pipeline:
1. Boot a cove Linux VM with `-nested`. The boot path must short-circuit with the hardware-gated error on M1/M2 runners (the CI fleet includes both generations; we skip the nested suite on unsupported hardware).
2. Run `cove vzscript run kvm-test`.
3. If exit 0, KVM works. If not, capture serial console and fail the build.

### Golden combined integration test

Per the round-1 Council note, the shipping bar includes a **single combined test that exercises nested + VirtioFS + Rosetta + vz-agent simultaneously**. Target file:

**`integration_linux_nested_test.go`** (new):
- Boots Ubuntu 24.04 ARM64 with `--nested`.
- Mounts a host directory via `-v` and verifies writes succeed as the guest user (exercises P0-C UID/GID auto-map).
- Executes a host-mounted `x86_64` binary via Rosetta (`arch --x86_64 /mnt/host/hello` returns expected output).
- Runs `kvm-ok` via `cove linux shell` (the vz-agent primitive) and asserts KVM-enabled.
- Boots a second nested Alpine VM via virt-install and asserts `virsh list` shows it running.
- Captures serial console on any failure.

One test, one job, full stack exercised.

#### Preserve isolated single-feature tests (QA amendment, v2)

The combined test is a feature-interaction regression catcher, **not** a replacement for isolated tests. CI ordering matters:

1. **First: isolated per-feature integration tests** — `testLinuxNetwork`, `testLinuxAgent`, `testLinuxRosetta`, `testLinuxVirtioFS`, `testLinuxPTYResize`, `testLinuxPTYSignal`, etc. These are the fast-failing unit-ish integration tests. Each exercises exactly one feature. When one breaks, the failure tells you exactly where to look.
2. **Then: the combined `integration_linux_nested_test.go` mega-test.** Runs only after the isolated suite passes. Catches bugs that only appear when features compose (e.g. nested virt + VirtioFS UID mapping colliding, or Rosetta + vz-agent PTY signals).

Do **not** delete the isolated tests in favor of the mega-test. Mega-test failure without isolated-test failure tells you "a feature-interaction bug"; mega-test failure when an isolated test is also red is redundant noise. Ship both, run in that order, fail fast on the cheap ones.

### What we'd need from cove itself

- **Nested toggle on `cove run`**: `cove run --linux --nested`. Adds hardware-gated `config.SetNestedVirtualizationEnabled(true)` (see Pillar 1 diff). Fails fast with the exact error string on M1/M2.
- **CPU bump default for nested mode**: nested libvirt wants ≥4 cores. `--nested` should auto-bump `-cpu` if user hasn't set it explicitly.
- **vsock for nested guests**: expose host vsock through to the inner Alpine? Probably not needed for v0.2; the inner VM has its own net.
- **Test matrix in CI**: nested + Rosetta + VirtioFS + vz-agent all on. One golden job (the `integration_linux_nested_test.go` target above).

### Estimate

KVM-test harness + combined integration test: folded into Pillar 1's 3-day estimate.

---

## Risks & open questions

| Risk | Mitigation |
|---|---|
| Nested virt trips the old overlayfs bug | Reproduce on Ubuntu 24.04.2 + kernel 6.8+; if still present, gate `--nested` behind `-unsafe-nested` flag and doc the limits |
| M1/M2 users request nested and get confused by failure | The hardware-gate error string is explicit **and actionable** — `nested virtualization requires M3/M4 chip on macOS 15+. Run without --nested to boot a standard VM (KVM will be disabled).` Users recover in one re-run. Docs state the per-chip matrix up front |
| Distro install flakiness on CI | Timeouts + serial-console capture on failure; golden images are pull-first, install-second as fallback |
| x86 Rosetta too slow for real work | Document it's for "binary compatibility", not "run x86 databases in prod" |
| Maintaining 4 distro installers rots | CI tests + nightly rebuild cron catch regressions; failure-issue opener on cron failure surfaces decay immediately; cosign signing ensures published images are traceable |
| Our published images + SLA-adjacent issues | Linux is freely redistributable; no Apple concern. Fedora/Debian licenses allow redistribution; Ubuntu requires a "Canonical" note in docs. Nightly rebuild keeps CVE exposure bounded |
| gvisor-tap-vsock integration cost | Dropped from v0.2; revisit with native implementation in v0.3 or leave dropped |
| VirtioFS UID/GID default of 1000 breaks for guests with non-standard users | Config.json records the actual provisioned UID during install; 1000 is only the fallback for legacy VMs |

Open questions:
1. Do we ship cove-official Linux images, or just document "build your own with these scripts"? Product framing — shipping images is a support commitment (the nightly rebuild cron is how we meet it).
2. Default distro if user runs `cove up --linux` without `-distro`? Ubuntu 24.04 LTS is safe; Debian 12 is also fine. Pick one.
3. Do we integrate Lima (`lima-vm/lima`) — which itself targets VZ — as a competitor or a collaborator? They focus on the per-user "run a Linux shell" use case. We're broader. Mention them fairly in comparison.md.
4. Should we support the Asahi stack (Fedora Asahi Remix) as a distro, acknowledging it targets bare metal? Probably no — too niche for v0.2 and users who want Asahi don't virtualize.
5. When do Arch/NixOS return? Target v0.3, gated on the four-distro harness (Ubuntu/Debian/Fedora/Alpine) proving stable through one full release cycle.

---

## Scope summary

| Pillar / task | Days | v0.2 / v0.3 |
|---|---|---|
| Pillar 1: Nested virt + hardware gate + combined KVM/VirtioFS/Rosetta/agent test | 3 | v0.2 |
| Pillar 2: 4 distros (Ubuntu, Debian, Fedora, **Alpine**) + CI | 4 | v0.2 |
| Pillar 3: OCI images + nightly rebuild cron + cosign signing + failure-issue opener | 3 | v0.2 |
| Pillar 4g: VirtioFS UID/GID auto-map | 0.5 | v0.2 |
| Pillar 4e: `cove linux shell` (agent-exec-stream wrapper) | 0.5 | v0.2 |
| **Pillar 4e: PTY wiring — `ExecRequest` exec_id+tty, guest PTY alloc, `ResizeExecTTY` + `SignalExec` + `SetTime` unary RPCs, host SIGWINCH/SIGINT forwarding** | **1.5** | **v0.2 (SetTime P1, rest P0)** |
| **Pillar 4e: connect-go + buf adoption (local generation, no buf.build registry dep; one handler serves gRPC + JSON + gRPC-Web; Docker-shaped URLs via `http.ServeMux` wrapper; replaces hand-rolled HTTP adapter)** | **-1 net** | **v0.2 (P0)** |
| Pillar 4b + 4d: Rosetta auto-mount + direct-kernel default | 1 | v0.2 |
| Docs: single-page `docs/features/linux.md` with sequential `###` per-distro sections (Ubuntu / Debian / Fedora / Alpine) | 1 | v0.2 |
| **v0.2 total** | **~13 days** | **v0.2** |

Plus v0.3 stretch items (Arch + NixOS installers, user-mode networking if revisited, native tabbed-code-block docs component): ~5 days.

---

## Success criteria for v0.2

1. `cove up --linux --nested` on an M3/M4 host produces a VM where `cove vzscript run kvm-test` passes. On M1/M2 hosts, the same command fails fast with `nested virtualization requires M3/M4 chip on macOS 15+. Run without --nested to boot a standard VM (KVM will be disabled).`
2. `cove install --linux --distro debian` works first-try on CI; same for Fedora; same for Ubuntu; same for Alpine.
3. `cove install --linux --distro alpine` completes install in under a minute and the installed VM cold-boots in ~2 seconds (the HN-demo headline number).
4. `cove pull ghcr.io/<cove-org>/cove-linux-ubuntu:24.04` boots a ready VM in under 2 minutes. All pulled images pass cosign keyless verification.
5. Nightly rebuild cron is green for every published image for at least one full week before the v0.2 tag. On any failure, a GitHub Issue is opened automatically with label `release-blocker` and assigned to `@cove-release`.
6. `cove run --linux --rosetta` lands at a shell where `arch --x86_64 uname -m` returns `x86_64`.
7. `cove linux shell <vm>` opens an interactive PTY-attached shell over vsock in under 500ms, without the user touching SSH keys or knowing the VM's IP. Resizing the host terminal correctly reflows `vim`/`htop`/`less` inside the shell; Ctrl+C interrupts the running guest command without orphaning it.
8. `cove run -v ~/projects:/mnt/projects --linux` results in a mount where the guest user can create files without `chmod` or manual `uid=` options (P0-C).
9. Isolated feature tests (`testLinuxNetwork`, `testLinuxAgent`, `testLinuxRosetta`, `testLinuxVirtioFS`, `testLinuxPTYResize`, `testLinuxPTYSignal`) are green; then `integration_linux_nested_test.go` is green on M3/M4 CI runners and exercises nested + VirtioFS UID/GID + Rosetta + vz-agent simultaneously.
10. `docs/features/linux.md` is one page with sequential `###` per-distro sections (Ubuntu / Debian / Fedora / Alpine). Every claim has a vzscript or command example next to it. (Tabs-as-JS deferred to a future docs iteration.)
11. comparison.md's Linux-VM-support row goes from "Yes" to a more compelling "Yes (nested KVM, Rosetta, 4 curated distros incl. 2-second-boot Alpine, signed OCI images, PTY-correct vsock shell)".

## What this unlocks

- cove becomes the obvious answer to "best way to run a Linux VM on Apple Silicon for devs" — currently fragmented between Lima, UTM, Parallels, orbstack, Docker Desktop (for container use), and "just SSH to a cloud VM".
- Linux CI pipelines that target ARM64 + KVM get a fast local reproducer.
- Kernel and virtualization devs get an actually-good iteration loop on their Macs.
- Cove gets a second audience (Linux devs) without giving up the macOS-VM pitch.

The frame: **cove 0.1 matches lume on macOS portability; cove 0.2 leapfrogs everyone on Linux developer ergonomics.**

## Verified 2026-05-10

- Generated Go stubs `proto/agentpb/agent.pb.go` and
  `proto/agentpbconnect/agent.connect.go` are committed; standard
  `go build` is zero-dependency.
- `proto/agent.proto:19-26` declares the v3 unary surface: `Exec`,
  `ExecStream` (server-streaming), `ResizeExecTTY`, `SignalExec`,
  `SetTime`. Matches v5 "no bidi" decision.
- **Drift, additive only**: `proto/agent.proto:23` later added the bidi
  `ExecAttach(stream)` RPC for `cove shell <vm>` (design 023 Slice 3,
  v0.3). v5's "no bidi" was correct for v0.2 scope; the v0.3 addition
  is opt-in and does not violate the v5 security/auth rationale because
  `ExecAttach` runs over the local control-socket broker, not the
  remote `cove serve` path the v5 rationale targeted.
- `proto/buf.gen.yaml` present; `proto/buf.gen.clients.yaml`
  (release-time TS/Python/Swift client gen) NOT yet shipped.
  `swift/VZControl/` shows the Swift client lives in tree directly,
  not behind a release-only generator step.
