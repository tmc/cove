# cove build — OCI-cached VM image builds

**Status**: draft v2 (post-round-2 Council + second-opinion review)
**Author**: cove team
**Date**: 2026-04-16
**Target**: cove 0.3 (depends on 0.1 OCI push/pull + 0.2 content-addressed store)

## Changes since v0

This revision addresses Council round-1 + round-2 verdicts, a second-opinion review, and decisions from a follow-up user interview:

### v2 (post-round-2 Council + second-opinion)

- **P0 — Linux guest swap + macOS tmpfs hardening.** v1's tmpfs-for-secrets reasoned about HOST swap but missed GUEST swap. On Linux guests, tmpfs pages under pressure to `/swapfile` inside `disk.img` — the very thing we ship. v2 mandates `noswap` mount option (kernel ≥6.4), `swapoff -a` + `swapon --show` verification gate, a fixed-size RAM-disk on macOS guests, thorough-mode swapfile zero-fill, and a loud warning on `# secret:` + `# compact: fast`.
- **P0 — Cross-machine block-diff instability is a known limitation, not a measurement.** v1 treated cross-run layer-digest stability as something to measure; the second-opinion review correctly flagged this as a PASS/FAIL gate, not a nice-to-have. v2 documents that APFS extent placement / inode / catalog state are non-deterministic across independent installs — two runners with identical cache KEYS may produce different cache LAYER BYTES. `--cache-from/--cache-to` is reframed as "population, not dedup-equivalent."
- **P1 — Agent protocol version, not binary digest.** v1 keyed on `agent_binary_digest`, so any agent security patch torched every cached layer in existence. v2 swaps to `agent_protocol_version` (semver from `agent/version.go`), with a CI lint: if `proto/agent.proto` changes but `AgentProtocolVersion` does not, the build fails.

### v1 (post-round-1 Council + user interview)

- **P0 — Secrets handling rewritten.** v0's `# cache-env:` was correctly flagged by Council as a security hole (tokens leaking into pushed OCI layers via bash history, unified logs, swap). v1 introduces a new `# secret:` directive backed by a guest-side tmpfs mount; `# cache-env:` is retained but narrowed to non-secret cache-influencing variables, with loud documentation.
- **P0 — APFS boot churn / compaction.** v0 ignored the fact that a plain shutdown leaves gigabytes of log/swap/diagnostic churn in the block diff. v1 adds a tiered compaction step (`fast`/`targeted`/`thorough`) with a benchmark-driven default, overridable per-step via `# compact:`.
- **P1 — `# mount:` is now a build-time error.** Host mounts violate OCI portability; the parser rejects them in build context with a pointer to `# cache-file:` / `# inject:` alternatives.
- **P1 — Strict parent hashing.** Cache key now consumes the parent's OCI manifest SHA-256 digest, never a local name/tag. Upstream base updates invalidate the chain correctly.
- **P2 — Scope reset.** Total bumped to ~3000 LOC / 15 days to cover secret-mount, compaction integration, ban-mount parser changes, and the benchmark harness.
- **v0.4 lookahead.** Added a Council round-2 question on signed external-secret-store support (1Password / Vault / SOPS / age).

---

## Goal

Give cove a `docker build`-shaped workflow for macOS/Linux VM images: chain `vzscript` steps on top of a base image, cache each step as an OCI layer keyed by content, and produce a pushable tagged image. On second build, steps that didn't change their inputs skip to a `clonefile` restore of the cached layer — no VM boot, no script execution, no cost.

This is the feature that turns cove from "`docker pull` for macOS VMs" (v0.1 parity with lume) into **"`docker build` for macOS VMs"** (genuine category differentiator — lume has no equivalent).

## Non-goals

- Replacing `vzscript run` for interactive/ad-hoc use. `cove build` is for declarative, reproducible image production.
- Multi-stage builds (Docker's `FROM x AS stage`). Single base + linear script chain in v0.
- BuildKit-style DAG parallelism. Sequential in v0; parallel later if telemetry shows a value.
- Cross-architecture builds. Apple Silicon host only (cove's whole target). Linux-on-ARM guests supported; x86 guests via Rosetta are opaque to the cache.
- Build-time network policy (Docker's `--network=none`). Out of v0; document that scripts run with the VM's default network.
- A Dockerfile-compatible parser. We use native vzscript format with a small header extension.

---

## Mental model

```
     cove pull ghcr.io/me/macos-15:sequoia               # base image M0
              │
              ▼
     ┌─────────────────┐
     │  vzscript:      │   cache-key = sha256(M0 + script + inputs)
     │  homebrew       │   if hit: clonefile restore L1-cached
     │                 │   if miss: boot VM, run script, diff blocks → L1
     └────────┬────────┘
              ▼
     ┌─────────────────┐
     │  vzscript:      │   cache-key = sha256(L1-key + script + inputs)
     │  golang         │
     └────────┬────────┘
              ▼
     ┌─────────────────┐
     │  vzscript:      │
     │  claude-code    │
     └────────┬────────┘
              ▼
     cove push ghcr.io/me/macos-15-workstation:latest    # M0 + L1 + L2 + L3
```

Each step produces a block-level delta (not a filesystem overlay like Docker). Layers stack by `clonefile`ing the parent and applying the delta.

## Why this is feasible

We already have the pieces:

| Need | Already in cove |
|---|---|
| Declarative script engine with deps | `vzscript` + `# runs-on:`, `# deps:`, `# inject:` headers |
| Deterministic command execution | `cmd/vz-agent` (vsock gRPC) |
| Instant VM forks | APFS `clonefile` (`clone.go`, `snapshots.go`) |
| OCI chunking + push/pull | v0.1 path from `design: cove disk handling + OCI` |
| Content-addressed blob store | v0.2 path (same doc) |

Net new: a cache-key scheme, a block-diff algorithm, a `cove build` orchestrator, two new vzscript header directives, a tmpfs-backed secret channel, and a tiered compaction step.

---

## Cache key

For a single vzscript step:

```
key = sha256(
    parent_manifest_digest       # OCI manifest SHA-256 of the input layer (see below)
  | script_content_hash          # sha256 of the full vzscript file (txtar-normalized)
  | agent_protocol_version       # vz-agent protocol semver; major bumps invalidate downstream
  | sorted_kv(cache_env)         # values of env vars declared in '# cache-env:' header
  | sorted_kv(cache_url_content) # sha256 of bodies fetched from '# cache-url:' entries
  | sorted_kv(cache_file_content)# sha256 of host files declared in '# cache-file:' header
  | sorted_names(secrets)        # NAMES ONLY of '# secret:' vars; values never hashed
  | compact_mode                 # 'fast' | 'targeted' | 'thorough'
)
```

Key properties:

- **Deterministic.** Same inputs → same key on any machine, any day.
- **Parent-chained.** Changing step N invalidates N+1, N+2, ..., as it should.
- **Input-complete.** Anything that influences the script's output must be declared via a `# cache-*:` header, or the user takes the cache-drift hit knowingly.
- **Agent-protocol-version-sensitive.** vz-agent protocol *major* version changes force a rebuild; minor versions are backwards-compatible and do not invalidate (see "Agent version semantics" below).
- **Secret-surface-sensitive but not secret-value-sensitive.** Adding or removing a `# secret:` entry invalidates the key (the *set of declared names* changes); rotating the value of an existing secret does not (values never touch the hash).

### Strict parent hashing

`parent_manifest_digest` MUST be the fully-resolved OCI manifest SHA-256 of the parent layer or base image — not a local name, not a tag. Docker's cache has historically leaked bugs here: users tag `:latest`, upstream republishes `:latest`, local cache keeps serving stale layers because the key was computed against the tag rather than the digest it resolved to at build time.

We resolve `--base <ref>` to a digest exactly once at build start and pin that digest through the whole pipeline. The result: **upstream base-image update → digest changes → cache miss → rebuild.** Spell this out in docs; it is a feature, not a bug.

### Agent version semantics

The cache key uses `agent_protocol_version` (a semver string from `agent/version.go` constant `AgentProtocolVersion`), NOT the binary digest. Minor version bumps (1.0 → 1.1) are backwards-compatible: a v1.1 agent can apply layers cached by v1.0 or earlier. Major bumps (1.x → 2.0) invalidate the cache. This prevents agent security patches from torching the entire ecosystem's build cache.

**Contributor rule:** Bumping the protocol version in `agent/version.go` must accompany any change to `proto/agent.proto`. A CI lint verifies: if `proto/agent.proto` changes in a commit but `AgentProtocolVersion` is unchanged, the build fails with "protocol change requires version bump in agent/version.go". Prevents the 'forgot to bump' class of cache-poisoning bugs.

### Known limitation — cross-machine layer-digest stability

> **⚠️ Cross-machine layer-digest equality is not guaranteed.** Block-diff operates on APFS extent placement, inode numbers, and catalog state — none of which are deterministic across independent macOS installs. Two runners running the same build script against the same parent may produce identical cache KEYS but different cache LAYER BYTES. The benchmark harness (see companion doc) will quantify this before v0.3 ships.

Implication for `--cache-from / --cache-to`: these flags enable cross-runner cache POPULATION; layer-digest equality across machines is not guaranteed in v0.3 — consult the benchmark results for measured stability.

### New vzscript header directives

```
# cache-env: BUILD_NUMBER CIPHER_SUITES    # NON-SECRET inputs that influence the cache key
# cache-url: https://go.dev/VERSION?m=text # fetch + hash body at build time
# cache-file: ~/.ssh/known_hosts           # hash host file contents
# cache-ttl: 7d                            # optional: invalidate older than 7 days
# secret:    GITHUB_TOKEN OPENAI_API_KEY   # secrets mounted via tmpfs, never hashed
# compact:   targeted                      # override default compaction for this step
```

Absence of all cache-* directives means "inputs = script content + parent only". That's fine for pure scripts that do nothing but `brew install a b c` with no version pins, but documented as caveat emptor.

### Secrets (`# secret:`) — read this before using `# cache-env:`

> ### WARNING — `# cache-env:` is NOT for secrets.
> Values passed via `# cache-env:` are **hashed into the cache key AND embedded in the script's runtime environment** on the guest. That means:
> - The value ends up in bash history, unified logs, swap, and any `env` dump the script happens to run.
> - Those artifacts are captured by the block diff and baked into the pushed OCI layer.
> - **Any secret you pass via `# cache-env:` will leak to anyone who pulls the image.**
>
> Use `# cache-env:` only for non-secret variables whose *value* should influence the cache key — e.g. a build-number, a flag like `CIPHER_SUITES=modern`, a feature toggle. Use `# secret:` for tokens, passwords, API keys, SSH keys, etc.

The `# secret:` directive works as follows:

1. At build start, the orchestrator reads the declared names from the **host** environment. Missing names are a fatal error (fail-fast, not silently empty).
2. **Guest swap must be off before tmpfs is mounted.** On Linux, the agent runs `swapoff -a`, then verifies with `swapon --show` — if output is non-empty, **fail the build** with: `cove build: cannot guarantee no-swap for secrets (swapon still shows active swap). Check guest kernel ≥6.4 and no systemd-swap unit.` On macOS guests, host swap is handled separately (see thorough compaction below).
3. Before the script runs, vz-agent creates a tmpfs mount at `/tmp/cove-secrets/` inside the guest:
   - **Linux**: `mount -t tmpfs -o rw,noexec,nosuid,nodev,mode=0700,noswap tmpfs /tmp/cove-secrets`. The `noswap` option requires kernel ≥6.4; if unavailable, the build fails.
   - **macOS guest**: a small fixed-size RAM disk via `diskutil erasevolume APFS tmpfs_secrets $(hdiutil attach -nomount ram://131072)` (64 MB, 131072 × 512-byte sectors).
4. For each declared secret, the agent writes `/tmp/cove-secrets/<NAME>` with mode 0600, owner root:wheel (or equivalent).
5. The script reads values from `/tmp/cove-secrets/<NAME>`. Example: `TOKEN=$(cat /tmp/cove-secrets/GITHUB_TOKEN)`.
6. On script completion (success or failure), agent unmounts the tmpfs. The RAM pages evaporate; nothing ever touched the APFS `disk.img` backing store.
7. The block diff runs AFTER unmount, so the delta never sees secret bytes.

Why this is strictly safer than `--secret` in Docker BuildKit: BuildKit mounts secrets into the container's filesystem namespace, which can leak via `mount` output or `/proc`. Our tmpfs lives only in VM RAM, has swap disabled, and the VM's backing disk image is the *only* thing we ship. No intermediate export path exists.

**Warning on `# secret:` + `# compact: fast`.** If a script declares both `# secret:` and `# compact: fast`, the build emits a loud warning: `cove build: # secret: declared with # compact: fast — guest swap may retain plaintext. Switch to # compact: targeted or thorough, or remove # secret: and use env-only for non-sensitive values.` Rationale: `fast` mode does no compaction, so any stray swap activity during the script run (however unlikely after `swapoff -a`) would not be zeroed before the block diff.

**Cache-key interaction.** Only the sorted *names* of declared secrets are hashed — never values. Rationale: rotating `GITHUB_TOKEN` shouldn't invalidate a cache that will still function with the new token. But adding or removing secrets *does* invalidate, because the script's expected environment shape changed.

**Roadmap — v0.4 signed manifests.** v0.3 ships tmpfs-only, sourcing from host env vars. v0.4 adds support for fetching secret values from external stores (1Password Connect, HashiCorp Vault, SOPS, age) via a signed manifest accompanying the image. See open question 9.

### What we intentionally do **not** key on

- **Wall-clock time** or ISO8601 timestamps — would break caching by definition.
- **Host username, hostname, machine serial** — the key must be portable across developers on the same team.
- **GUI state, screenshot pixels, OCR output** — the VM's screen is not part of the build product.
- **Host filesystem contents the script doesn't touch** — only declared inputs count.
- **Values of declared secrets** — see above.

---

## `# mount:` is prohibited in build context

When the invoking command is `cove build` (as opposed to `cove vzscript run`), the vzscript parser emits a fatal error if any step declares `# mount:` (host-path virtiofs share). Rationale:

- Host mounts are unbounded, unhashed inputs. Any file the script reads through a mount escapes the cache key.
- Host mounts are host-specific. A build that works on the author's laptop (because `~/projects` exists) fails on CI. That's the opposite of what an OCI image promises.
- Images that depend on host state cannot be pushed and pulled meaningfully.

The error message points users to the right alternatives:

```
vzscript step 'my-step' declares `# mount: ~/src` — not allowed in `cove build` context.
Host mounts break OCI portability: the pushed image would reference host state that
doesn't exist for anyone else who pulls it.

Fix by declaring the dependency explicitly:
  - small config files:  `# cache-file: ~/src/config.yaml`  (hashed into cache key, read at build time)
  - source trees:        `# inject: /opt/src src.txtar`     (embedded into the layer)
  - network fetches:     `# cache-url: https://...`         (hashed + fetched at build time)
```

Ad-hoc / interactive `cove vzscript run` continues to allow `# mount:` — it's only the build path that bans it.

---

## Compaction — taming APFS boot churn

macOS and Linux guests accumulate nontrivial write churn across a script run that has nothing to do with the intended layer content: unified-log rotation, diagnostic reports, swapfile activity, APFS metadata reshuffling, `atime` updates, locate DB rebuilds. A plain shutdown leaves these as block-level differences, and the block diff faithfully captures them. Observed result in early experiments: a 200 MB "install one brew formula" step produces a 1.5–3 GB delta — most of which is noise.

We address this with a guest-side compaction step between `script-finished` and `diff-blocks`:

| Mode | Action | Expected overhead | Expected delta savings |
|---|---|---|---|
| `fast` | No compaction. Shutdown only. | 0s | 0 (baseline) |
| `targeted` | Zero-fill known churn paths: `/var/log`, `/var/db/diagnostics`, swapfile, `/private/var/folders/*/C/*` (macOS) or `/var/log`, `/var/cache`, swapfile (Linux). | 3–8s | Most of the noise for most scripts |
| `thorough` | Full free-space zero-fill: `fstrim -v /` (Linux) or `diskutil apfs eraseFreeVolumeSpace free /` (macOS), plus explicit guest swapfile zeroing (see below). | 30–60s | All of it |

Under `thorough` compaction, before the block-diff runs, the guest's swapfile is explicitly zeroed to ensure no plaintext from any process (not just `# secret:` scripts) survives into the pushed layer:

- **Linux**: `swapoff /swapfile && dd if=/dev/zero of=/swapfile bs=1M count=$(stat -c %s /swapfile | awk '{print $1/1048576}') && mkswap /swapfile && swapon /swapfile`
- **macOS guest**: host swap is handled separately (the macOS VM dynamic swap store lives under `/private/var/vm/` and is cleared by `eraseFreeVolumeSpace`).

### Default mode — TBD, benchmark-driven

User flagged concern that `thorough` adds 30–60s per step, which could dominate an otherwise cache-friendly 5-step build. `targeted` is much cheaper but incomplete. We defer the default selection to a **separate benchmark harness doc** that will run representative workloads (`homebrew`, `golang`, `claude-code`, mixed) across all three modes and report:

- Per-step overhead added by compaction
- Per-step delta size reduction
- End-to-end push size for a realistic 5-step workstation build
- **Same-machine** cross-run layer-digest stability (does the same script produce the same delta across two runs on the same host?) — cross-machine stability is a known limitation, not a measurement goal; see the "Known limitation" callout in the Cache key section

The benchmark result picks the default. Until then, the implementation supports all three modes behind `--compact <mode>` with no default set at the CLI layer (orchestrator defaults to `targeted` as a placeholder; final default TBD).

### Per-step override

Script authors can override via header:

```
# compact: thorough
```

This is useful for the final step of a build (worth the extra 60s to shrink the pushed image) or for a first-step `brew install` (where delta-size reduction dwarfs the compaction cost).

The `compact` mode is hashed into the cache key, so switching modes correctly invalidates downstream layers.

---

## Layer production

When a step is a cache miss:

1. **Fork base.** `clonefile` the parent-step's `disk.img` into a scratch VM directory.
2. **Boot, mount secrets tmpfs, run script, unmount tmpfs, shut down.** Standard `cove run`/vz-agent/`cove vzscript run` path plus the tmpfs lifecycle described above. Quiesce the filesystem (shutdown rather than suspend) so block states are flushed.
3. **Compact.** Based on `# compact:` mode (or build-level default), run targeted zero-fill or full `fstrim`/`eraseFreeVolumeSpace` before the diff.
4. **Diff against parent.** Block-level comparison of the two `disk.img` files: for each 64 KB block (APFS native), if `sha256(parent_block) != sha256(child_block)`, emit the child block into the delta. Zeroed blocks from compaction collapse to the well-known zero-digest and contribute nothing to the delta.
5. **Chunk the delta.** Run through the v0.1 `registry/chunker.go` — 512 MB LZ4 chunks with sparse-zero detection.
6. **Store layer locally.** Write chunks to the v0.2 store, write a layer manifest under the cache key.
7. **Destroy scratch VM.** `clonefile` means it's cheap; we don't keep intermediate scratch VMs around past the end of `cove build` unless `--keep-intermediate` is set.

### Why block-level, not filesystem-level

Docker diffs filesystems (overlayfs, copy-up on write). We can't: the guest filesystem is inside an opaque `disk.img` from the host's perspective. We don't mount it.

Block-level diffing has upsides:

- **Smaller deltas for large-file modifications.** A `brew upgrade` that overwrites 500 MB of Ruby gems produces a <500 MB delta; Docker would emit the full file.
- **Format-agnostic.** Works whether the guest uses APFS, HFS+, ext4, xfs.
- **No cross-FS code paths.** We're diffing raw bytes.

Downside:

- **More I/O during diff.** Reading two 60 GB files and hashing 64 KB blocks. ~30–60 s on M-series SSD. Budget this in.
- **Block alignment sensitivity.** If the guest re-allocates a file's blocks (defrag, TRIM), we see it as a change even though content is identical. Document; accept the minor cache miss rate. (The compaction step makes this *more* likely, not less — an intentional tradeoff.)

### `block_diff.go` algorithm

```go
// Compare two disk.img files block by block.
// Emits (offset, data) pairs for every changed 64 KB block.
//
// Parallelism: shards the file range across N goroutines (N=NumCPU, capped at 8).
// Uses SHA-256 for block fingerprints; xxhash could speed us up 5-10x with a CGO
// dep, so we stick with stdlib crypto/sha256 for zero-dep purity.
//
// Sparse-awareness: uses SEEK_DATA / SEEK_HOLE (Darwin fcntl F_LOG2PHYS_EXT is
// less portable) to skip hole regions — huge win for mostly-empty guest disks.
func DiffDisks(parent, child string) (*Delta, error) { ... }
```

---

## Layer application (restore cached step)

When a step is a cache hit:

1. **Verify the cache entry integrity.** Read the cached layer manifest; verify all chunk digests exist in the store (either locally or pullable).
2. **Fetch missing chunks.** Pull from the registry if we have a remote ref but only the manifest locally.
3. **Clonefile the parent.** `disk.img` forks instantly.
4. **Overlay the delta.** For each (offset, chunk) in the delta, decompress LZ4 and `WriteAt()` into the child `disk.img`.
5. **Move on to next step.** No VM boot; no script execution.

The cache-hit path is I/O-bound (writing the delta), not CPU-bound. On a full cache hit across a 5-step pipeline, we do 5 clonefiles + delta applications, total wall time dominated by SSD throughput. Ballpark: a 30 GB workstation image with 3 GB of accumulated delta = ~10 seconds to rematerialize, vs the ~40 minutes it'd take to actually run `brew install` the first time.

---

## The `cove build` subcommand

```
cove build <name> \
    --base <ref>                    # required: base image to pull
    --script <name|path> ...        # repeat for each step
    --tag <ref>                     # output tag; can repeat
    [--push]                        # push after build
    [--dry-run]                     # plan steps + keys, don't run
    [--no-cache]                    # skip cache, re-run every step
    [--cache-from <ref>]            # pull cache layers from this image before build
    [--cache-to <ref>]              # push cache layers to this image after build
    [--keep-intermediate]           # leave scratch VMs behind for debugging
    [--chunk-size <mb>]             # passed to chunker; default 512
    [--compact <mode>]              # fast | targeted | thorough (default: benchmark-driven)
```

### Example

```bash
cove build macos-workstation \
    --base ghcr.io/tmc/macos-15:sequoia \
    --script homebrew \
    --script golang \
    --script claude-code \
    --script cove-itself \
    --tag ghcr.io/tmc/macos-15-workstation:latest \
    --tag ghcr.io/tmc/macos-15-workstation:$(git rev-parse --short HEAD) \
    --cache-from ghcr.io/tmc/macos-15-workstation:cache \
    --cache-to ghcr.io/tmc/macos-15-workstation:cache \
    --push
```

### Output format

```
=> step 1/4: homebrew                     [cache hit: L1-a7f3c2...]     0.8s
=> step 2/4: golang                       [cache hit: L2-3d9e1b...]     1.2s
=> step 3/4: claude-code                  [CACHE MISS — running...]
   boot vm ...................... 12s
   run script ................... 4m18s
   compact (targeted) ........... 6s
   shutdown ..................... 6s
   diff blocks .................. 42s
   chunk + store ................ 28s
   => layer L3-c8a7e2... (1.4 GB)        total step: 5m52s
=> step 4/4: cove-itself                  [cache hit: L4-9f2e8a...]     1.1s
=> pushing ghcr.io/tmc/macos-15-workstation:latest
   skip: 180 existing chunks (M0 base)
   push: 24 new chunks (L3 only)  ......... 2m14s
=> built and pushed in 10m18s (cache hits: 3/4; pushed 24 chunks)
```

On second build with nothing changed: `~30s` total (4 cache hits, no VM boots, no pushes).

### Cache population modes

- **Local-only** (default): hits/misses check `~/.vz/store/build-cache/`.
- **`--cache-from <ref>`**: additionally, on cache miss, try `HEAD /v2/<repo>/blobs/<cache-key>` on the remote. This is how CI shares caches across runners.
- **`--cache-to <ref>`**: after the build, push any newly-produced cache layers to the remote cache ref.

This mirrors BuildKit's registry cache backend. A CI setup with `--cache-from + --cache-to` to a dedicated `:cache` tag enables cross-runner cache POPULATION; layer-digest equality across machines is not guaranteed in v0.3 — consult the benchmark results for measured stability (see the "Known limitation" callout in the Cache key section).

---

## On-disk layout additions

```
~/.vz/store/
├── blobs/sha256/<digest>              # existing v0.2
├── build-cache/
│   ├── keys/<cache-key>.json          # NEW: {layer_digest, parent_digest, script_hash, ...}
│   └── layers/<layer-digest>.json     # NEW: manifest of chunks comprising this layer
```

The cache is just a small JSON index on top of the existing blob store. Blobs themselves live in `blobs/sha256/` and are refcounted across (VMs, templates, snapshots, build-cache). `cove store gc` (v0.2) sees build-cache entries as roots and respects them.

---

## Interaction with v0.1 and v0.2

- **v0.1** (push/pull): unchanged. `cove build` produces an OCI image that v0.1's `cove pull` can consume normally (as a "normal" stacked image — other tools including lume don't need to know it was built layer-by-layer).
- **v0.2** (store + GC): build cache is a root set for GC, alongside VMs and templates. No new GC logic; existing flock + 1h mtime window covers build-cache writes.
- **v0.3** (this doc): adds `cove build`, block diff, cache-key scheme, header directives (`cache-*`, `secret`, `compact`), tmpfs secret channel, compaction tiers, build-time `# mount:` ban.

---

## Determinism and cache-drift

The biggest honest risk: **builds are only reproducible if the script's inputs are fully declared.**

Unpinned `brew install foo` can produce different layer bytes tomorrow (upstream update). That's fine if users accept it — they get cache hits until `brew` upstream changes, and a miss + re-run when it does.

What's **not** fine is silent drift: a script that reads `/etc/motd` without declaring it in `# cache-file:`, then one day someone changes `/etc/motd` on the host and we serve a stale cached layer that no longer matches reality.

Mitigations:

1. **`cove build --no-cache`** is always available as an escape hatch.
2. **Cache-TTL optional** — `# cache-ttl: 7d` invalidates after a week, for scripts that pull unpinned upstream state.
3. **Cache verify mode (`cove build --verify-cache`)** — run every step as if it's a cache miss, produce the layer, compare to what cache said. Slow but catches drift. Recommended nightly in CI.
4. **Documentation** — clear callout that cache keys reflect declared inputs only, with examples of how to pin: use `# cache-url:` against upstream version files, `# cache-env:` against CI commit SHAs, etc.

---

## Comparison to alternatives

| Approach | Notes |
|---|---|
| **Docker-on-macOS** | Different product (Linux containers, not macOS VMs). Out of scope. |
| **Packer + Vagrant** | Builds VMs but produces opaque artifacts; no content-addressed layer caching. Slower iteration. Ecosystem is Linux-VM centric; macOS-guest support is second-class. |
| **Tart (tart.run)** | Has `tart push/pull` to OCI. No `tart build` equivalent. Same gap lume has. |
| **Lume** | `lume push/pull` to GHCR. No build. **Cove matches `push/pull` in v0.1; `cove build` in v0.3 is the differentiator.** |
| **Raw `cove vzscript run`** | Today's workflow. Works fine for one-offs, no caching, no artifact to share with teammates. `cove build` is the productized form. |
| **Docker BuildKit `--secret`** | Closest analog to our `# secret:`. BuildKit mounts secrets into a container FS namespace; we mount into guest-only tmpfs that never touches the disk image. Strictly tighter leak surface. |

---

## Risks & mitigations

| Risk | Mitigation |
|---|---|
| Block diff is slow | Parallel hashing, SEEK_HOLE for sparse regions, budget 30–60s/step as acceptable |
| Cache drift from undeclared inputs | `--no-cache`, `--verify-cache`, `# cache-ttl`, docs |
| Non-deterministic upstreams (brew, apt) | Users pin versions; cove doesn't try to enforce |
| Runaway cache size | GC respects build-cache roots; `cove store prune --build-cache --older-than 30d` |
| Scratch VM leaks on crash | `cove build` writes a lockfile + PID; recovery on next run GCs scratch dirs |
| Guest state that escapes `disk.img` | Document: only `disk.img` changes are captured. Shared folders, clipboard, NVRAM delta handled separately (aux.img is a declared layer in v0.1 manifest) |
| Agent protocol version mismatch | `agent_protocol_version` is in the cache key; major-version bumps invalidate, minors are backwards-compatible. CI lint ties proto changes to version bumps |
| Block-diff produces different bytes across machines for same script | Benchmark harness quantifies; if instability >5%, `--cache-from` across runners is population-only, not dedup-equivalent |
| Parallel `cove build` on same base | Scratch dirs are UUID-named; no collision. Lock only needed on the final manifest write |
| Secrets leaking into pushed layers | `# secret:` → tmpfs-only; `# cache-env:` docs flag it loudly as non-secret-only; parser warns on heuristic matches (`*_TOKEN`, `*_KEY`, `*_PASSWORD`) in `# cache-env:` |
| Users put secrets in `# cache-env:` anyway | Build-time lint: if a `# cache-env:` name matches heuristics above, emit warning with pointer to `# secret:` |
| Compaction inflates per-step latency | Tiered modes + per-step override; benchmark harness picks default; `fast` mode always available for devs who prefer speed over layer size |
| Compaction causes false-positive churn next run | Compaction is deterministic (zero-fill of same paths); same-machine cross-run stability measured by benchmark harness. Cross-machine stability is NOT guaranteed — see Known limitation above |
| `# mount:` snuck into a build | Parser fatal error with remediation hint (see dedicated section) |
| User tags `--base :latest` expecting caching | Resolved to digest once at build start; docs explain the trade-off |

---

## Scope

| Piece | LOC | Days |
|---|---|---|
| `build.go` — subcommand, orchestrator, progress output | ~400 | 2 |
| `build_cache.go` — cache key, local lookup, remote lookup via `--cache-from` | ~300 | 1.5 |
| `block_diff.go` — parallel SHA-256 block diff with SEEK_HOLE | ~250 | 1.5 |
| `build_apply.go` — clonefile + delta apply for cache-hit path | ~180 | 1 |
| vzscript header extensions (`cache-env`, `cache-url`, `cache-file`, `cache-ttl`, `secret`, `compact`) | ~150 | 1 |
| `build_parser_mount_ban.go` — build-context parser that rejects `# mount:` with remediation msg | ~80 | 0.5 |
| `build_secrets.go` — host-env read, tmpfs mount/unmount via agent, 0600 file writes | ~250 | 1.5 |
| `build_compact.go` — targeted/thorough zero-fill dispatch (macOS + Linux guest paths) | ~200 | 1 |
| Scratch VM lifecycle (create, run, destroy, crash-recovery) | ~220 | 1 |
| Benchmark harness integration — wire `--compact` modes to measurement sink | ~150 | 1 |
| Parent-digest resolution — `--base` ref → manifest digest, pinned for pipeline | ~70 | 0.5 |
| Tests — cache hit/miss, determinism across machines, drift detection, secret-non-leak, compaction savings, mount-ban | ~500 | 2.5 |
| Docs — `docs/features/build.md`, secrets warning, Dockerfile analogy, CI recipes | ~250 | 1 |
| **Total** | **~3000** | **~15 days** |

---

## Open questions

1. **Cache key hash choice.** Stdlib `crypto/sha256` keeps us CGO-free. `xxhash` (pure-Go port available) is ~5× faster but not cryptographically robust; for cache keys it's fine (collision probability still negligible). Worth the dep? Lean: stick with SHA-256, pure-stdlib.
2. **Block size.** 64 KB matches APFS default. 4 KB would catch finer deltas; 1 MB would be faster to diff but miss small changes. 64 KB is the right sweet spot.
3. **Sparse detection.** Use `fcntl(F_LOG2PHYS_EXT)` (Darwin-native, pure syscall via `golang.org/x/sys/unix`) or portable `SEEK_DATA`/`SEEK_HOLE` (`lseek` with non-default whence)? `SEEK_DATA`/`SEEK_HOLE` is available on macOS since 10.12; portable and simpler. Lean: portable seeks.
4. **Cache entry format — JSON vs protobuf.** We already use protobuf for the control socket; JSON for vzscripts. Cache entries are small structured data — JSON keeps debugging easy (`jq` works), protobuf keeps the format tight. Lean: JSON.
5. **`--cache-to` target — dedicated tag or shared with final?** Docker BuildKit uses both patterns. Dedicated `:cache` tag is cleaner (doesn't pollute the deployable ref). Lean: dedicated `:cache` tag by convention.
6. **Scratch VM isolation.** Should scratch VMs be fully headless and network-restricted by default? Gives determinism (no clipboard interference, no random Bonjour discovery) but breaks scripts that legitimately need network. Lean: network on, GUI off; `# build-network: none` header opt-out for scripts that want isolation.
7. **Parallel step execution.** BuildKit parallelizes independent steps in a DAG. vzscript `# deps:` already declares a DAG. Do we respect it in `cove build`, or execute in CLI-declared order only? Lean: CLI order in v0, DAG-parallel in v0.4.
8. **Interop with lume.** Lume has no `build` — but images produced by `cove build` are standard OCI, so lume's `pull` gets them as plain images (no cache semantics, just the final disk state). Does this matter enough to call out? Lean: document it as a feature ("cove builds, everyone runs").
9. **v0.4 signed secret manifests — which architecture?** v0.3 ships tmpfs-only, sourcing secret values from host env vars. v0.4 wants to support external secret stores so CI doesn't have to materialize tokens in its own env. Three candidate designs for Council round-2 to weigh in on:
    - **(A) URI scheme in script header.** `# secret-from: GITHUB_TOKEN=1password://vault/item/field` — cove shells out to an existing CLI (`op`, `vault`, `sops`, `age`) to resolve. Pro: zero new code to own; users already have these tools. Con: orchestrator becomes a fan-out shell invoker; trust model is "whatever the CLI does."
    - **(B) Age-encrypted sidecar OCI layer.** The image ships a small encrypted layer containing a secret manifest; consumers pull the image, decrypt with their private key, cove plumbs values into tmpfs. Pro: self-contained image, no runtime deps on external stores. Con: build is a credentials flow (who holds the recipient public keys?); rotation requires a rebuild.
    - **(C) Go plugin API for secret providers.** `type SecretProvider interface { Fetch(name string) ([]byte, error) }` with built-ins for env, 1Password, Vault, SOPS, age. Pro: extensible, testable, cove owns the trust model. Con: most code to write; a plugin ecosystem is an ongoing commitment.
    - **User preference**: wants Council's input before committing.
10. **Default `# compact:` mode.** Held pending benchmark harness results. See compaction section. Candidate defaults: `targeted` (safe middle), `thorough` (smallest layers, slowest), or `fast` (fastest, largest layers — only viable if cache hit rate is near 100% in practice).

---

## What this unlocks (product framing)

- **Actual parity and a step beyond.** Lume matched → lume bypassed on the one feature that matters for reproducible dev/CI.
- **CI multiplier.** Teams tag a `macos-15-workstation:latest` image rebuilt nightly, every PR runner pulls the prebuilt in 30s instead of a 40-minute setup.
- **Share workstations the way teams share containers.** `cove pull team/macos-dev:sequoia` gives a new hire a day-1-ready environment in two commands.
- **The "GitOps for macOS VMs" frame.** Vzscript files + cache-from/cache-to refs + `cove build` in CI = declarative, reproducible, version-controlled VM images.

The arc: **cove v0.1 ships portability (match lume), v0.2 ships efficiency (content-addressed store), v0.3 ships productivity (builds + cache).** By v0.3, cove isn't "cove vs lume" — it's "cove, because it's the only macOS VM tool with a real build pipeline."
