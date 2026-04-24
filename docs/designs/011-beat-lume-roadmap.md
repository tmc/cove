# cove roadmap - beat lume, do not mimic it

**Status**: draft v0
**Author**: cove team
**Date**: 2026-04-22
**Horizon**: cove 0.1 -> 0.4

## Why this doc exists

The existing design set already defines the major feature tracks:

- `001` - `cove serve` HTTP + MCP
- `002` - disks, OCI push/pull, and lume interop
- `003` - `cove build` and OCI layer caching
- `005` - external secret providers
- `006` - Linux guest support

What was missing was one concrete roadmap that says which of those tracks matter
most, what ships first, and how cove wins against lume without turning into a
Go reimplementation of lume.

## Goal

Make cove the default local VM tool for Apple Silicon developers and CI users
who need macOS or Linux guests, and make it clearly stronger than lume in the
areas that matter most:

1. Faster local iteration through suspend/resume, APFS clonefile snapshots, and
   disposable clones.
2. More reliable guest control through the vsock guest agent instead of SSH as
   the primary control plane.
3. More deterministic automation through VZScript, OCR, and pre-boot disk
   injection instead of vendor-model-driven setup loops.
4. A stronger image-production story through `cove build`, local content
   addressing, and secrets handling that does not leak into published artifacts.

## Non-goals

- Do not chase lume's exact UX surface command-for-command.
- Do not switch the main guest-control path to SSH.
- Do not make browser VNC the primary GUI story.
- Do not embed a vendor-specific LLM loop into cove itself.
- Do not emit lume annotations by default when pushing OCI images.

## Product stance

Cove should treat lume as:

- a benchmark for where agent-facing ergonomics must be good enough,
- an interoperability target at the OCI and MCP/HTTP boundary,
- and a product to beat on local speed, state management, guest control, and
  reproducible image creation.

That means:

- **Match at the boundary**: HTTP, MCP, OCI pull, optional OCI compatibility.
- **Beat in the core**: snapshots, suspend/resume, guest agent, VZScript, Linux
  developer experience, and image builds.
- **Skip the wrong fights**: built-in Anthropic loops, SSH-first architecture,
  and VNC-first local UX.

## Locked decisions

These are explicit roadmap constraints, not open questions:

1. **The guest agent stays primary.** No roadmap milestone should depend on SSH.
   Cove's main path is vsock gRPC for exec, file I/O, clipboard, and agent
   control, and roadmap effort should reinforce that rather than add parallel
   SSH-first workflows.
2. **Local state is the moat.** APFS clonefile snapshots, suspend/resume, and
   disposable clones stay first-class and inform every release.
3. **Interop is asymmetric.** Cove pulls lume images and may offer an explicit
   compatibility mode on push, but the default cove schema stays cove-native.
4. **Remote access is subordinate to local ergonomics.** HTTP, MCP, and any
   future browser display path exist to expose cove's core, not to replace the
   native AppKit + CLI workflow.
5. **Deterministic automation beats hosted model loops.** Disk injection and
   guest-agent automation are the baseline. OCR is a constrained fallback for
   screens that cannot be modified offline or reached through the agent. Any
   model-assisted path is optional and layered above that baseline.

## Release roadmap

### cove 0.1 - Reliable local macOS + agent-ready surface

**Objective**

Ship the local macOS path in a form that is obviously usable by both humans and
agents, without giving up cove's single-binary local-first model.

**Why this beats lume**

Lume already proves there is demand for HTTP, MCP, OCI movement, and unattended
automation. Cove wins 0.1 by shipping those boundaries while still keeping the
native local advantages lume does not have: snapshots, suspend/resume, VZScript,
disk injection, and the guest agent.

**Must ship**

1. Harden `cove serve` HTTP + MCP and treat it as product, not prototype.
   Snapshot, suspend/resume, and rollback primitives must be visible to agents,
   not just lifecycle basics.
2. Ship OCI pull/push with one-way lume pull compatibility and explicit
   compatibility mode only where needed.
3. Fix local reliability bugs that undermine the "better than lume" claim:
   `-headless` must never open a window, and SIP/recovery automation must
   reliably clear the Recovery `Options` picker path.
4. Finish agent health and LaunchAgent/FDA groundwork so user-session control is
   dependable.
5. Publish docs and examples that make the agent-facing story obvious:
   HTTP API, MCP usage, Node example, push/pull flow, and machine-readable
   `cove dump-docs` output for CLI/API/MCP wrapping.

**Ship gate**

- `cove up -user <name>` reaches a usable macOS desktop from a fresh install
  without Setup Assistant handholding.
- `cove run -headless` does not create a GUI window.
- `cove serve --mcp` and `cove serve -http` both operate a pre-existing VM
  end-to-end (create-VM is a CLI-only path for 0.1).
- Agents can invoke snapshots and pause/resume directly over HTTP/MCP.
- `cove pull` of a lume-produced image boots successfully in cove.
- The Recovery automation path can disable and re-enable SIP on a real VM.
- `cove dump-docs` emits enough structured data for a client or agent wrapper to
  discover the CLI/API/MCP surface without scraping help text.

### cove 0.2 - Linux developer workstation moat

**Objective**

Make cove the best local Linux VM tool on Apple Silicon for developers who need
more than containers: nested KVM, Rosetta, shared folders that work, and a real
shell/control plane.

**Why this beats lume**

Lume supports Linux, but cove can win the higher-value workflow: "I need a fast
Linux VM on my Mac that behaves like a serious developer machine, not just a VM
that boots."

**Must ship**

1. Linux shell over the guest agent with PTY, resize, signal forwarding, and
   clock sync.
2. Ubuntu, Debian, Fedora, and Alpine installers with a single obvious path for
   `cove up --linux`.
3. Nested KVM on supported hardware, with an explicit hardware gate and an
   actionable fallback on unsupported chips.
4. VirtioFS UID/GID auto-mapping and Rosetta setup as defaults, not manual
   post-install chores.
5. Agent health, agent upgrade, and user-session agent mode robust enough that
   Linux and macOS both lean on the same control architecture.

**Ship gate**

- `cove up --linux --nested` reports working KVM on supported M3/M4 hosts.
- `cove linux shell` handles `vim`, `htop`, and `tmux` correctly through host
  terminal resize and Ctrl+C/Ctrl+D flows.
- Alpine is a fast-demo path and Ubuntu/Debian/Fedora are stable daily-driver
  paths.
- Shared-folder workflow does not require manual chmod games for the primary
  guest user.

### cove 0.3 - Build and caching moat

**Objective**

Turn cove from "a VM runner with image pull/push" into "the fastest way to
produce and iterate on macOS and Linux VM images locally."

**Why this beats lume**

This is the strongest differentiation opportunity in the entire roadmap. If
`cove build` works, cove becomes the VM equivalent of `docker build` with local
APFS acceleration and guest-aware scripting. Lume does not have an equivalent
core.

**Must ship**

1. `cove build` with content-addressed cache keys and vzscript step chaining.
2. Local content-addressed blob store and GC safety for pulled and built
   artifacts.
3. APFS block-diff caching with benchmark-backed claims only; do not over-claim
   cross-machine cache stability until the churn harness passes.
4. Secret handling through tmpfs-backed `# secret:` flow so credentials do not
   leak into pushed images.
5. A small set of canonical build examples: CI runner, dev workstation, security
   sandbox.

**Ship gate**

- A cache hit skips guest execution for unchanged steps.
- A build artifact can be pushed, pulled onto a second VM, and used immediately.
- Secrets used during build do not remain in the resulting disk image.
- Cross-machine cache reuse is either benchmark-proven and enabled, or clearly
  documented as deferred.

### cove 0.4 - Shared-host and CI hardening

**Objective**

Make cove safe and boring to run on shared Mac minis, CI fleets, and long-lived
automation hosts.

**Why this beats lume**

The winner in this space is not the project with the flashiest demo. It is the
one operators trust on real machines. Cove should use 0.4 to turn its local
technical advantages into operational credibility.

**Must ship**

1. External secret-provider delegation (`1password://`, `vault://`, `sops://`,
   `age://`) on top of the v0.3 tmpfs flow.
2. Stronger provenance and artifact metadata around pulled and built disks.
3. Shared-host auth guidance hardened around keychain vs token-file vs
   per-VM-auth modes.
4. Agent fleet hygiene: predictable upgrades, version reporting, and failure
   visibility.
5. Optional browser display bridge only if it meaningfully reduces remote-ops
   friction; it remains secondary to native GUI and CLI.

**Ship gate**

- CI can fetch secrets into a build without hanging on MFA/TTY prompts.
- Operators can tell which image, agent version, and provenance data a VM came
  from.
- Shared-host deployment has one recommended secure mode and docs that match it.

## Selective adoption from lume

These are useful ideas from lume that cove should adopt only where they support
the roadmap above:

1. **Machine-readable docs export** for CLI/API/MCP surface. This helps agents
   and SDKs wrap cove accurately.
2. **Versioned unattended presets** for OS-specific setup flows. Use them to
   organize automation, but keep execution in VZScript/OCR/disk injection.
3. **Optional browser display bridge** for remote use. Keep it optional and
   secondary.
4. **Non-registry object-store backends** only if real users need them after
   OCI registry flow is solid.

## Things cove should deliberately not copy

1. **SSH-first guest control**. It is the wrong center of gravity for cove.
2. **Vendor-model-specific automation loops inside the core binary**. Keep cove
   model-neutral and substrate-oriented.
3. **VNC-first local UX**. Native AppKit window + CLI remains the primary local
   experience.
4. **Schema coupling to lume defaults**. Compatibility is a flag, not the core
   identity.

## Immediate execution order

If the team needs one concrete order of attack rather than four abstract
releases, use this:

1. Fix headless and Recovery/SIP reliability.
2. Harden `cove serve`, docs, and lume-image pull compatibility.
3. Finish LaunchAgent/FDA/agent health work.
4. Ship Linux shell + nested KVM + distro set.
5. Ship `cove build` + local content store + secrets tmpfs.
6. Add external secret delegation and optional remote display bridge only after
   the above are solid.

## Success test

This roadmap is working if, by the end of 0.3, the honest pitch is:

> Use lume if you want a remote-first agent wrapper around Apple VMs.
> Use cove if you want the fastest, most scriptable, most reproducible local
> macOS/Linux VM workflow on Apple Silicon.

If cove cannot make that claim clearly, then the roadmap has drifted back toward
parity work instead of category-defining work.
