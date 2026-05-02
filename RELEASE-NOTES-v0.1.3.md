# cove v0.1.3 — release notes

Maintenance + foundation release. Ships the v0.3 build-executor
scaffold (content-addressed store, cache-key planning, block deltas),
the Linux turnkey distro track, fork lineage tooling, OpenAI Agents
SDK adapter, and the local-signing release pipeline.

78 commits since v0.1.1.

## What's new

### `cove build` cache foundation (v0.3 prep)

The first three slices of the v0.3 build executor land as opt-in
foundation, no public surface yet:

- Local content-addressed store at `~/.vz/store/` with GC retention
  for build-cache blobs.
- Build-cache key planning with dry-run support; reports local cache
  hits in the plan.
- Block-delta primitives that store disk deltas as blobs in the
  store. Build-cache metadata persists across runs.

Track 2 of the v0.3 design (`docs/designs/018-v03-build-executor-scaffold.md`,
`019-v03-cache-hit-materialization.md`) consumes these primitives in
follow-up releases. No CLI breaking changes in v0.1.3.

### Linux: turnkey distro installers, VirtioFS ownership, Rosetta default

Three v0.2 Linux improvements ship under the `cove run -linux` path:

- Turnkey distro installers — single-flag boot of common Ubuntu variants.
- VirtioFS UID/GID auto-mapping so guest writes land under the host
  user instead of root.
- Rosetta enabled by default on Apple Silicon hosts; nested KVM gated
  on supported host CPUs.

### Fork lineage tree (`cove vm tree`)

Following Phase 4 of design 013, `cove vm tree` shows the parent
chain of fork-from / clone-of relationships. `--json` and `--orphans`
modes for scripting and cleanup. `cove vm delete --cascade` removes
a subtree.

### OpenAI Agents SDK adapter

`cove-sandbox` Python package exposes cove as an OpenAI Agents
`ComputerTool` backend. See `adapters/openai-agents/README.md`.

### `cove doctor` Full Disk Access probe

`cove doctor` now reports whether the in-guest agent has TCC Full
Disk Access. Distinguishes ENOENT from FDA-blocked on `readdir` of
mounted shares — addresses the "stat works, ls hangs" symptom from
v0.1.0/v0.1.1.

### Nix package manager vzscript

`cove vzscript run nix` installs Nix on a macOS guest VM via the
Determinate Systems multi-user installer. Parallels the existing
`homebrew` recipe.

### Soft-reset benchmark harness

`bench/fork-time/` and `bench/soft-reset/` ship under `bench/` for
reproducing the empirical numbers behind designs 013 and 015.

## What's fixed

- `fix(helper)`: set HOME for the LaunchDaemon to prevent the cove
  helper from looping on `mkdir ./.vz` against `/`.
- `fix(helper)`: preserve the sudo invoking UID so re-entrant calls
  drop privileges correctly.
- `fix(serve)`: reconcile symlinked-VM routes (legacy `~/.vz/<name>/`
  layout consistency between list and proxy paths).
- `fix(up)`: resolve the up target before install so `cove up -vm X`
  doesn't race the registry.
- `fix(screenshot)`: keep headless capture on the framebuffer path.
- `fix(keychain)`: avoid an unsafe global dereference.
- `fix(version)`: fill build info for `go install`-built dev binaries.
- `fix(doctor)`: exclude `VZRECOVERY` from FDA probe results.

## Release pipeline change: local signing only

`docs/release-pipeline.md` is rewritten. The 6 GitHub-secrets table is
gone. Signing happens on the maintainer's workstation via
`scripts/release-local.sh` (or `make release-local`); the GitHub
Actions release workflow no longer holds Apple credentials.

This eliminates the supply-chain risk of a signed-malware
distribution path through CI. Trade-off: maintainer is the bottleneck
per release; cask bumps are manual.

## Compatibility

- No CLI breaking changes since v0.1.1.
- `~/.vz/store/` directory is new; safe to delete (regenerates on
  next `cove build`).
- LaunchDaemon plist regeneration is recommended for users hitting
  the v0.1.0/v0.1.1 helper crash-loop — `sudo cove inject-agent`.

## Upgrade

```bash
brew upgrade cove           # via Homebrew tap (cask updated post-release)
# or
go install github.com/tmc/cove@v0.1.3
```
