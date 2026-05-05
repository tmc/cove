# cove v0.4.0 release notes

cove v0.4.0 is the release where the project stops being just a local VM tool
and becomes an operator-owned substrate for agent runs, CI jobs, fleet control,
and Linux guest provisioning. The release is still private and local-first, but
the surface is now broad enough to build real workflows around.

## Headline

- `cove agent-sandbox run` now spans Anthropic, OpenAI, Gemini, and Vertex.
  Anthropic runs inside the Go CLI, OpenAI uses the local Python
  `cove-sandbox` adapter, and Gemini / Vertex keep their bridge shape.
- `cove fleet` now lets one trusted host route selected control-socket commands
  to another trusted host over SSH.
- `cove install -nixos` now installs NixOS as a first-class Linux guest.

## All ships

### VM lifecycle policy

Design 031 landed as a real per-VM policy surface:

- Design: [`docs/designs/031-vm-lifecycle.md`](docs/designs/031-vm-lifecycle.md)
- Policy persistence: `d1df12b` (`vmpolicy: add policy persistence`)
- Policy CLI: `2202f46` (`policy: add cove policy cli`)
- Enforcement loop: `80eea77` (`lifecycle: enforce vm policy`)

The policy file lives at `~/.vz/vms/<name>/policy.json` and records idle
timeout, maximum age, and run budget thresholds. The runtime loop now stops a VM
cleanly when one of those thresholds is exceeded and emits `vm_policy_stop`
metrics with a reason.

### Per-VM resource quotas

Design 032 made CPU, memory, and disk intent durable:

- Design: [`docs/designs/032-vm-quotas.md`](docs/designs/032-vm-quotas.md)
- Implementation: `94bf2d2` (`docs: design per-vm resource quotas`)

Quotas live at `~/.vz/vms/<name>/quotas.json`. The release notes and CLI now
carry the operator's intended CPU, memory, and disk caps together instead of
scattering them across transient flags.

### Cove daemon

Design 033 introduced the long-lived host coordinator:

- Design: [`docs/designs/033-cove-daemon.md`](docs/designs/033-cove-daemon.md)
- CLI scaffold: `a9a2a9b` (`daemon: add cove daemon cli`)

`coved` is a user-session coordinator, not a replacement for per-VM control
sockets. It exists to own lifecycle policy, image GC scheduling, and network
drift work that should not be recomputed by every one-shot CLI invocation.

### Fleet Slice 1

Design 034 shipped remote host routing for trusted Mac hosts:

- Design: [`docs/designs/034-fleet-slice-1.md`](docs/designs/034-fleet-slice-1.md)
- Remote config and SSH tunnel helpers: `695ae2e`
- CLI and routing: `9f993a5`

`cove fleet add/ls/rm` registers a remote host, and `cove --fleet=<name> ...`
routes selected control-socket commands through an SSH tunnel to that host.

### OpenAI SandboxRunConfig backend

Design 035 extended the Python adapter from `ComputerTool` to sandbox runs:

- Design: [`docs/designs/035-openai-sandbox-run-config.md`](docs/designs/035-openai-sandbox-run-config.md)
- Python helper: `4d61edd` (`python: add sandbox run config helper`)
- Docs/example integration: `27f9e24` (`docs: add openai agents integration`)

The local `cove-sandbox` package now exposes a `SandboxRunConfig` helper for
`Runner.run()` workflows, while keeping the adapter local and VM-backed.

### NixOS guest support

Design 036 made NixOS a first-class Linux guest:

- Design: [`docs/designs/036-nixos-guest-support.md`](docs/designs/036-nixos-guest-support.md)
- Installer support: `8324750` (`nixos: add guest installer support`)
- Base recipe: `2427b2e` (`vzscript: add nixos base recipe`)
- Quickstart: `f1e6812` (`docs: add nixos quickstart`)

`cove install -nixos` now emits a declarative install bundle, boots the
installer through EFI, and finishes as a normal Virtualization.framework Linux
guest after install.

### Linux Desktop autoprovisioning

Design 037 documents the first-boot Linux Desktop path:

- Design: [`docs/designs/037-linux-autoprov.md`](docs/designs/037-linux-autoprov.md)
- Provisioning groundwork: `a451c5f` (`add provisioning system`)
- Auto-login verification: `d430a1f` (`provision: verify staged auto-login plist`)

The release carries the desktop autoinstall and auto-login contract, but the
first-boot reliability work is still a live concern on some hosts. Treat this
as documented operator behavior, not a promise that every desktop boot is fully
polished.

Post-install macOS provisioning also pre-warms the native authorization prompt
before Data-volume attach, so the admin-password dialog appears at the start of
the privileged step instead of late in disk injection. Auto-login staging now
validates the exact `kcpassword` bytes and root-owned metadata alongside the
`loginwindow.plist` checks.

## Migration from v0.3

There are no intentional breaking CLI removals in this release.

Existing flows still work:

- `cove run`
- `cove build`
- `cove image build/list/rm/inspect`
- `cove ctl`
- `cove up`

The new surfaces are additive:

- `cove policy`
- `cove quota`
- `cove daemon`
- `cove fleet`
- `cove install -nixos`
- the Python `cove-sandbox` adapter and `SandboxRunConfig` helper

If you are coming from v0.3, the main migration work is operational rather than
semantic: decide which hosts should run coved, which images should be tagged
and cached, and whether your agent runs should stay local or move into the new
Python `SandboxRunConfig` path.

## Known gaps

- Ubuntu Desktop first-boot reliability is improved, but still depends on host
  and guest OS behavior during first boot. The v0.4 contract is stronger
  artifact validation plus watchdog fallback, not a guarantee that every
  desktop image skips every login-screen transition.
- vmstate Phase 5 follow-on work is still open. Track that in `#232`.
- Public OCI distribution and a public cove image channel remain private.
- The Cirrus migration guide is a draft. It is documentation, not a shipped
  automatic conversion path.
- GitLab CI remains partial in the integration matrix and still uses the shell
  runner shim shape.

## Breaking changes

None. The v0.4 release is additive from the v0.3 CLI surface.
