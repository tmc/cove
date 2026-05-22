# cove v0.3.0 release notes

Build/cache GA release. `cove build` now runs non-dry-run builds
against local VM bases, materializes cache hits without guest
execution, executes cache misses in scratch VMs, verifies layer
metadata before apply, and leaves a VM directory that can be handed
to `cove push`.

This release keeps the v0.3 boundary deliberately local: registry-base
build execution, registry cache import/export, external secret stores,
and parallel build execution remain deferred.

## Highlights

### `cove build` local-base execution

`cove build` is no longer dry-run-only for local VM bases. A build
forks the base VM into a scratch directory, runs vzscript steps through
the guest agent, records block-delta layers, and writes build metadata
for the final VM directory.

```bash
cove build dev-image --base ~/.vz/vms/macos-dev --script build.vzscript
cove push ~/.vz/build-scratch/<id> ghcr.io/me/dev-image:v1 --dry-run
```

Registry-base non-dry-run execution remains deferred. Registry refs are
still planning-only and fail before script loading when used for
non-dry-run builds.

### Cache-hit materialization

Cache hits validate complete step metadata, cache TTL, compact mode,
and layer manifest digests before applying cached layers. A full cache
hit path applies cached deltas without starting the guest and reports
the hit count in the final summary.

```text
Build complete
  cache hits: 1/1
```

### Cache-miss execution

Cache misses fork scratch VMs, run the step body through the agent,
compact according to the selected mode, diff the resulting disk against
the parent, and persist the layer manifest only after verification.
Failed scripts do not poison the cache. `--keep-intermediate` preserves
the scratch VM for inspection on failure.

### Secrets MVP

Build scripts can declare tmpfs-backed secrets:

```text
# secret: GITHUB_TOKEN
guest-exec sh -lc 'wc -c /tmp/cove-secrets/GITHUB_TOKEN'
```

On Linux guests, cove disables guest swap before mounting tmpfs. On
macOS guests, secrets use a RAM disk. Secrets are unmounted before
compaction and before the block diff.

The URI resolver MVP also supports build-time `secret-from` directives:

```text
# secret-from: API_TOKEN=env://API_TOKEN
# secret-from: SSH_KEY=file:///Users/me/.ssh/id_ed25519
```

Only `env://` and `file://` providers ship in v0.3. External stores
such as Vault, AWS Secrets Manager, GCP Secret Manager, 1Password, SOPS,
and age are deferred. `cove secret probe <uri>` verifies resolution and
prints only the resolved byte length, not the secret value.

### Build compaction

Build steps support `fast`, `targeted`, and `thorough` compaction modes
through `--compact` and per-script `# compact:` headers. `targeted` is
the default and is included in the cache key.

`--compact thorough` on macOS guests now targets the writable Data
volume, uses `dd` plus virtio TRIM because APFS rejects
`diskutil secureErase freespace`, and preflights host capacity before
inflating sparse images.

### Published fork benchmarks

The fork-only benchmark and boot-to-agent fork benchmark are published
under `bench/fork-time/` on named M4 hardware. These are the numbers to
use for release claims; soft reset is not an isolation primitive.

### Agent adapters

The OpenAI Agents SDK adapter v1 is part of the v0.3 release surface,
with live-smoke and package-check documentation under
`adapters/openai-agents-python/`.

The Anthropic sandbox-runtime adapter also ships as a substrate bridge
for computer-use style workflows.

### Additions since initial v0.3 cut prep

The v0.3 line also picked up the round-30/31 packaging and operator
polish needed to make the build/cache surface usable as an agent
sandbox substrate:

- **Run metrics**: forked runs now write structured lifecycle metrics to
  `~/.vz/runs/<run-id>/metrics.jsonl`, with JSONL as the default local
  sink and optional OTLP export through `OTEL_EXPORTER_OTLP_ENDPOINT`.
  The first metric set covers run start/end, fork materialization, VM
  start, and agent-ready timing. See `docs/features/metrics.md`.
- **Network policy surface**: `cove run` and `cove up` document
  `-network` / `--net` modes for `nat`, `bridged:<iface>`,
  `host-only`, and `none`, plus control-socket port forwarding for
  host-to-guest TCP access. See `docs/features/networking.md`.
- **Agent sandbox quickstart**: `docs/quickstart-agent-sandbox.md`
  packages the computer-use story across OpenAI Agents SDK, Anthropic
  Claude computer use, Gemini computer use, fork-per-task isolation, and
  per-run artifacts.
- **GitHub Actions executor verification**: the private `cove-action`
  wrapper has now been verified end-to-end for simple commands,
  multiline scripts, and intentional guest-command failure. Exit codes
  propagate through the action output instead of being swallowed.

## Install

```bash
brew upgrade cove
# or
go install github.com/tmc/cove@v0.3.0
# or
nix run github:tmc/vz-macos
```

Build from source:

```bash
go build -o cove .
codesign -s - -f --entitlements internal/autosign/vz.entitlements ./cove
```

## Commands and flags new to the GA surface

- `cove build <name> --base <local-vm-dir> --script <file>`
- `cove build --compact fast|targeted|thorough`
- `cove build --keep-intermediate`
- `# secret:` vzscript directive
- `# secret-from:` vzscript directive with `env://` and `file://`
- `cove secret probe <uri>`
- `cove run -fork-from ...` metrics in `~/.vz/runs/<run-id>/metrics.jsonl`
- `cove run --net nat|bridged:<iface>|host-only|none`
- `cove ctl port-forward start|stop|list`

## Deferred

- Registry-base `cove build` execution. Non-dry-run builds require a
  local VM base directory.
- Registry cache import/export through `--cache-from` and `--cache-to`.
  The flags are reserved and rejected before planning.
- Public curated `cove` image registry and signed agentkit image
  channels.
- External secret stores beyond `env://` and `file://`.
- BuildKit-style parallel step execution. v0.3 builds run sequentially.
- Packer plugin shim.
- Product-name resolution before any public registry or signed channel.
- Fresh public `agentkit/linux-base` image refresh is still in flight for
  this cycle; do not describe it as shipped until the image build and
  verification land.

## Known limitations

- Ubuntu Desktop first-boot reliability is still being investigated.
  Do not treat v0.3.0 as a first-boot polish release, and do not make
  virtio-blk vs NVMe disk-I/O throughput claims until that path is
  stable.
- Registry refs are planning-only for non-dry-run `cove build`.
- `--cache-from` and `--cache-to` are reserved, not implemented.
- `secret-from` supports only `env://` and `file://` providers.
- Build execution is sequential.
- SIGINT during a build preserves cache integrity, but the diagnostic is
  still terse.

## Breaking changes

None. No CLI flags were removed.
