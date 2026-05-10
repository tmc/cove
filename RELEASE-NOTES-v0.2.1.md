# cove v0.2.1 — release notes

Image surface release. The local image store, fork-from-image, the
`cove shell <vm>` exec client, and three computer-use adapter bridges
land together. The v0.3 build pipeline ships as a runnable preview
under `cove build`, gated to local VM bases. 174 commits since v0.1.3.

## Highlights

### `cove image` surface complete

Local pre-baked, forkable VM images with offline transport. Images
live at `~/.vz/images/<name>/<tag>/` and contain `manifest.json`, the
clonefile-backed `disk.img`, `aux.img`, `machine.id`, and
`hw.model`. `vmstate` is intentionally excluded — vmstate binds to
the {machine.id, aux, MAC, disk} tuple, so resuming a forked image
without re-deriving identity would corrupt the snapshot.

```bash
cove image build -from base-vm -tag macos-15:dev    # snapshot a stopped VM
cove image list
cove image inspect macos-15:dev -json               # manifest + live downstream forks
cove image push macos-15:dev cove-macos15.tar -gzip # offline transport
cove image load cove-macos15.tar -tag macos-15:dev  # extract back
cove image gc -older-than 30d -dry-run              # sweep unreferenced
cove image rm macos-15:dev                          # refused while forks exist
```

`cove image rm` and `cove image gc` re-check fork count immediately
before deletion to close the planning → remove TOCTOU window.
Tarball extraction restricts entries to the five known files
(`TypeReg` only) and refuses zip-slip, symlinks, and oversize
entries before any filesystem write. `ParentImage` lineage does not
cross hosts.

### `cove run -fork-from <image-ref>`

Boot a fresh VM by forking a local image. APFS clonefile shares
storage copy-on-write until first divergent write. `-ephemeral`
drops the `.ephemeral` sentinel so `cove gc` sweeps the fork on
stop. VM-name wins on collision with image-ref.

```bash
cove run -fork-from macos-15:dev -ephemeral
```

### `cove shell <vm>` standalone exec client

Docker-shaped exec UX for cove. `cove shell <vm>` opens an
interactive `bash -l` against a running VM; `cove shell <vm> --
argv...` runs a one-shot command and propagates stdout, stderr, and
the exit code. SIGWINCH forwards to `agent-exec-resize`; SIGINT
detaches the main cove handler so Ctrl-C reaches the guest.

```bash
cove shell macos-15
cove shell macos-15 -- ls -lah /Users
```

Server-side `agent-exec-attach`, `agent-exec-resize`,
`agent-exec-signal` ride on the existing per-VM control socket and
reuse `control.token` auth — no proto bump in this slice. Stdin is
read-only; bidirectional stdin and a new additive `ExecAttach` RPC
are deferred to design 023 Slice 3 in v0.3.

### Per-run artifact bundling

Each `cove run -fork-from` invocation lazily creates
`~/.vz/runs/<run-id>/` with:

- `manifest.json` — run id, vm name, fork ref, started/ended
  timestamps, exit status. Written atomically (temp + rename) on
  shutdown for both success and failure paths.
- `events.jsonl` — control-socket request log.
- `stdout.log`, `stderr.log`.
- `screenshots/`.

Plain `cove run <vm>` is unaffected.

### Three computer-use adapter bridges

Substrate-only Python helpers that drive a running cove VM as a
computer-use target for the three major model providers. No SDK
abstraction; each is a single file that translates provider tool
calls into cove control-socket commands and feeds the next
screenshot back.

```bash
python3 adapters/anthropic-bridge/computer_use.py --vm macos-15 --task "..."
python3 adapters/google-bridge/computer_use.py    --vm macos-15 --task "..."
python3 adapters/google-bridge/vertex-ai/computer_use.py --vm macos-15 --project my-gcp --task "..."
```

The OpenAI Agents SDK adapter (`adapters/openai-agents-python/`,
shipped in v0.1.3) is the reference for full SDK integration; the
three new bridges are deliberately substrate-only so the wire shape
stays auditable. See the cookbook walkthroughs under
`docs/examples/`.

### `cove run -linux -shell`

Attach the host terminal to an interactive guest shell after Linux
boot. Allocates a PTY via the agent, forwards SIGWINCH to
`ResizeExecTTY` and SIGINT to `SignalExec(SIGINT)`. Stdin is
read-only in v0.2; bidirectional stdin lives under design 023
Slice 3 in v0.3.

### `vzscripts` for runners and mesh networking

Three new recipes:

- `vzscripts/github-runner` — install and register a self-hosted
  GitHub Actions runner. Direct escape hatch for the GH Actions
  billing block.
- `vzscripts/gitlab-runner` — same shape for GitLab CI projects
  (shell executor on macOS arm64).
- `vzscripts/tailscale` — install Tailscale via Homebrew and bring
  the daemon up with `--ssh`. Idempotent.

### v0.3 build pipeline preview

The `cove build` executor lands as a runnable preview, gated to
local VM bases. Cache hits skip guest execution; misses fork a
scratch VM and run vzscript steps through the agent. Layer
manifests are verified before apply, and the final VM directory can
be handed to `cove push`. Three compaction modes — `fast`,
`targeted` (default), `thorough` — run between guest execution and
the layer diff and are included in the cache key. `# secret:`
mounts host environment variables through a guest tmpfs (Linux) or
RAM disk (macOS) with guest swap disabled and unmounted before
compaction.

```bash
cove build my-image --base local-vm --script build.vzscript --dry-run
cove build my-image --base local-vm --script build.vzscript
```

This is **preview**, not v0.3 GA. Registry-base execution and
registry cache import/export remain deferred. Non-dry-run still
requires a local VM directory base and fails with `cove build:
non-dry-run requires local VM base directory` for registry refs.

### Secrets MVP

The first external-secret adapter slice is present for build scripts.
`# secret-from: KEY=<uri>` resolves secrets through a small URI
registry, with two providers in this release:

- `env://NAME` reads from the host environment.
- `file:///path/to/secret` reads a local file after permission checks.

`cove secret probe <uri>` checks whether a URI resolves and prints
only the resolved byte length, not the secret value. This is an MVP
surface for build-time secret resolution; 1Password, Vault, SOPS,
rotation, and encrypted-at-rest storage are not included.

### Stabilization fixes

This tag also includes the post-v0.2.1 stabilization cluster that
landed before release prep:

- GUI delegate and iTerm2 proxy paths now snapshot shared state under
  lock before use, closing concurrency races found in the T39 audit.
- Linux installer VM configurations now attach the same Virtio socket
  device as normal Linux runtime configurations, so control-socket
  probes during install no longer fail with `no socket devices
  configured on VM`.
- Linux install disks use durable attachments, and the post-install
  verifier reports an explicit "installer produced no partition table"
  error with retry guidance when a blank disk is observed.
- Disk I/O benchmark docs now record that the current Ubuntu Desktop
  virtio-blk vs NVMe comparison is blocked by first-boot provisioning
  reliability, so no throughput claim is made in this release.

### Nix package + module

`flake.nix` and `nix/darwin-module.nix` for installing cove via
`nix run`/`nix profile install` and as a nix-darwin module. Pure
Nix builds work after dropping the local `apple` replace directive
in `go.mod` (commit `5927f56`).

### Designs and roadmap

Designs 021–025 land. Highlights:

- **023** — `cove shell <vm>` exec UX (Slice 1+2 shipped in this
  release; Slice 3 deferred to v0.3).
- **024** — `cove image` runner-image surface (Slice 1 shipped;
  push/pull deferred to v0.4 behind privacy gating).
- **025** — `cove-action` security architecture, blocking design
  021 Slice 1.

ROADMAP and SUMMARY are refreshed to track the new shipped/done
states.

## Install

```bash
brew upgrade cove
# or
go install github.com/tmc/vz-macos@v0.2.1
# or
nix run github:tmc/vz-macos
```

Build from source:

```bash
go build -o cove .
codesign -s - -f --entitlements internal/autosign/vz.entitlements ./cove
```

## Commands new since v0.1.3

- `cove image build | list | rm | inspect | push | load | gc`
- `cove run -fork-from <image-ref> [-ephemeral]`
- `cove shell <vm> [-- argv...]`
- `cove run -linux -shell`
- `cove ctl agent-exec-attach | agent-exec-resize | agent-exec-signal`

## Breaking changes

None. No CLI flags removed; no on-disk layouts changed.

## Known limitations

- `cove shell` stdin is read-only until the v0.3 proto bump adds
  `ExecAttach`. Interactive REPLs (`python3`, `irb`, `psql`) work
  for output but cannot accept piped input mid-session.
- `cove image push` / `load` are tarball-only. The OCI registry
  surface (push to / pull from a public or private registry) is
  scoped to v0.4 and gated on the trademark / public-flip decision.
- `ParentImage` lineage is local-only and does not survive across
  hosts. After `cove image load`, downstream forks must be
  re-derived against the loaded image.
- The Vertex AI computer-use bridge exits cleanly without ADC
  configured but does not retry against alternative auth sources;
  install `google-auth` or run `gcloud auth application-default
  login` first.
- Ubuntu Desktop first-boot reliability is still being investigated
  under T42. The installer/control substrate is more durable, but this
  release does not claim a polished first-boot Ubuntu Desktop UX.
- macOS first-boot provisioning has two open issues: #18 tracks the
  Authorization Services dialog remaining visible while elevated work
  completes, and #19 tracks auto-login falling back to the login-screen
  watchdog on first boot.
- The disk I/O benchmark remains blocked until Ubuntu Desktop reaches a
  stable `agent: available` state; do not treat current runs as a
  virtio-blk vs NVMe throughput comparison.
- Public registry, signed agentkit channels, and any v1 announce
  remain blocked on the trademark / rename decision tracked in
  `docs/research/trademark-cove.md`.

## Deferred to v0.3 / v0.4 / v1.0

- **v0.3** — `ExecAttach` proto RPC for bidirectional `cove shell`
  stdin (design 023 Slice 3); registry-base `cove build` execution;
  registry cache import / export (`--cache-from`, `--cache-to`);
  Anthropic v0.4 SDK adapter (design 022).
- **v0.4** — `cove image push` / `pull` to OCI registries (design
  024 Slice 2), gated behind `cove-action` security architecture
  (design 025); CI executors (GH Actions + GitLab; design 021);
  1Password, Vault, SOPS, and age secret providers.
- **v1.0** — public curated `cove` image registry; signed agentkit
  base image channels. Both blocked on trademark counsel or a
  rename plan; do not ship under the `cove` name without resolution.
- **Indefinite** — Packer plugin shim; BuildKit-style parallel step
  execution; external secret stores (1Password, Vault, SOPS, age).

## Caveats

- Apple Silicon only. macOS 14+ recommended on the host.
- Guest macOS 13, 14, 15 tested. FileVault may interfere with
  kcpassword-driven auto-login on macOS 15.
- `cove inject` still requires `sudo` for correct root:wheel
  ownership on LaunchDaemon plists; launchd silently ignores
  daemons with incorrect ownership.

## Audit trail

Full commit log: `git log --oneline v0.1.3..v0.2.1`. Roadmap:
`docs/designs/ROADMAP.md`. Designs landed in this release: 021
(v0.4 CI executors), 022 (v0.4 Anthropic adapter), 023 (cove shell
exec UX), 024 (cove runner images), 025 (cove-action security
architecture).

## Binary fingerprint

- Tag: v0.2.1
- Tag SHA: 86f7f4e1f2866beb5b80ccfd282fd77637996219
- Tag points at commit: dd5f58fcaf39d12186e58bd2d517a44eed73088a
- Binary: cove (darwin/arm64)
- SHA256: d5896de5889ac38c4fdfc840747c1d107d5cc786c8389ed67cd804c38d4ee002
- Build command: `go build -o cove ./`, then codesign per `internal/autosign/vz.entitlements`
