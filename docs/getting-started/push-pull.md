---
title: Push & Pull Images
---
# Push & Pull Images

Cove pushes and pulls macOS and Linux VM images to any OCI-compatible registry -- ghcr.io, AWS ECR, Docker Hub, a self-hosted Harbor, anything that speaks OCI distribution v2.

```bash
# Pull a prebuilt macOS image
cove pull ghcr.io/trycua/macos-sequoia-vanilla:15.2

# Boot it
cove run
```

Cove's pull path accepts both cove-native and lume-produced manifests, so migrating a library of lume images to cove is a single command per image.

## Pull

```bash
cove pull <ref> --dry-run                         # validate the pull target
cove pull <ref> --as my-macos --dry-run           # name the new VM
cove pull <ref> --dry-run --manifest manifest.json # validate a local manifest
```

Current implementation supports registry pulls for cove-native LZ4 manifests.
Use `--dry-run` to fetch and validate the manifest and target without writing a
disk, or `--manifest` to validate local manifest JSON.

What happens:

1. The manifest is fetched and its annotations are parsed (cove-native or lume -- both work).
2. `~/.vz/vms/<name>/disk.img.partial` is pre-allocated sparse at the full uncompressed disk size.
3. Chunks are pulled in parallel (4 at a time) into `~/.vz/store/blobs/sha256/`, then LZ4-decompressed, digest-verified, and `WriteAt`-ed at their fixed offsets. Zero chunks skip the write entirely and stay as holes.
4. `aux.img`, `hw.model`, and VM config are written.
5. On success, `disk.img.partial` is atomically renamed to `disk.img` and `disk.provenance` is written with the source manifest digest.

If the pull is interrupted (network loss, Ctrl-C, power), `disk.img.partial` stays on disk and `disk.img` is absent. Cove refuses to boot a VM in that state:

```
cove: VM <name> has incomplete disk (pull was interrupted).
Delete <path> and rerun cove pull, or use cove pull --resume <ref> to continue.
```

Rerunning `cove pull` reuses any verified blobs already present in `~/.vz/store/`, so an interrupted pull does not redownload chunks that landed before the failure.

## Local Store

`cove pull` stores cove-native OCI blobs under `~/.vz/store/blobs/sha256/<digest>`. Blob writes are atomic: cove writes a temp file, verifies the SHA-256 digest and byte size, fsyncs, and renames into place. Multiple VMs pulled from images sharing chunks reuse the same local blob files.

Garbage collect unreferenced blobs with:

```bash
cove store gc
```

GC takes an exclusive `~/.vz/store/gc.lock`. Pulls take the same lock in shared mode, so GC cannot delete in-flight blobs. GC also keeps blobs modified within the last hour, which protects recently interrupted pulls before their provenance graph is complete.

## Push

```bash
cove push <vm> <ref>                            # push disk.img as OCI tag
cove push <vm> <ref> --base <base-ref>          # delta push (skip unchanged chunks)
cove push <vm> <ref> --chunk-size 256           # override 512 MB default
cove push <vm> <ref> --dry-run                  # chunk and summarize locally, no upload
cove push <vm> <ref> --dry-run --manifest-out manifest.json
cove push <vm> <ref> --lume-compat              # emit dual cove + lume annotations
```

Push chunks `disk.img` into 512 MB fixed-offset chunks, LZ4-compresses each chunk, and uploads only the chunks not already on the registry. Fixed offsets make delta push and parallel uploads straightforward; `HEAD /v2/<repo>/blobs/<digest>` skips any blob the registry already has.

Delta push with `--base <ref>` pulls the base manifest first and only uploads chunks whose uncompressed content digest differs. Typical result: a fresh Xcode install on top of a vanilla macOS base uploads single-digit GBs instead of 60.

Push compresses non-zero disk chunks as LZ4 OCI layers, skips sparse zero
chunks, uploads missing blobs, and publishes the manifest tag. Use `--dry-run`
to see how many chunks and bytes a push would produce without touching the
network.

## Lume compatibility

Cove is asymmetric by design:

- **Pull**: reads both cove-native (`org.tmc.cove.*`) and lume (`org.trycua.lume.*`) annotations. A lume-produced image imports with no flags.
- **Push**: emits cove-native annotations only by default.

If you need a pushed image to be consumable by both cove and lume (mixed-tool teams, handoff to a lume-based pipeline), pass `--lume-compat`:

```bash
cove push my-vm ghcr.io/me/macos-15:tag --lume-compat
```

The resulting manifest carries both annotation sets with identical values. Cove's default stays cove-native-only so our schema isn't coupled to lume's evolution -- `--lume-compat` is the one-flag escape valve when you need interop. See [`cove push` flags](../reference/cli.md#push) for the full syntax.

## Size reality

macOS images are large. A vanilla macOS 15 base is ~20 GB pushed (LZ4-compressed, chunked). A developer image with Xcode, Homebrew, and tooling lands at 25-40 GB. This matches what lume sees with the same underlying disks -- cove doesn't magically shrink the bytes, it just moves them efficiently.

Where you win:

- **Layer dedup on the registry.** Re-pushing an image where only late chunks changed uploads only those chunks. Delta push against a base tag skips base chunks entirely.
- **Zero-chunk sparse handling.** Long zero regions from a fresh install get a well-known zero digest, never cross the wire, and restore as sparse holes on the puller.
- **Parallel range fetches.** A 20 GB pull on a fast link is bounded by your pipe, not by a single TCP stream.

Run [`cove compact`](../reference/cli.md#compact) before a push to zero-fill free space in the guest; the sparse-chunk detector then drops those regions from the upload.

## Auth

Cove follows Docker's credential precedence so setup matches muscle memory:

1. **macOS keychain** via `docker-credential-osxkeychain` (if referenced from `~/.docker/config.json`).
2. **`~/.docker/config.json`** auth entries.
3. **Environment**: `COVE_REGISTRY_TOKEN` (any registry), `GITHUB_TOKEN` (only for `ghcr.io`).
4. **Anonymous** (pull-only, public images).

Each step is tried in order; the first that resolves credentials for the target registry wins.

### Credential helper setup

`docker-credential-osxkeychain` ships with Docker Desktop. If you don't run Docker Desktop, it isn't installed by default. Install it via Homebrew:

```bash
brew install docker-credential-helper
```

Or fetch the signed binary directly from the Docker CLI releases:

```bash
# Replace VERSION with the latest from https://github.com/docker/docker-credential-helpers/releases
curl -L -o /tmp/helper.tar.gz \
  https://github.com/docker/docker-credential-helpers/releases/download/v0.8.1/docker-credential-osxkeychain-v0.8.1.darwin-arm64
sudo install /tmp/helper.tar.gz /usr/local/bin/docker-credential-osxkeychain
```

Then wire it up by adding `"credsStore": "osxkeychain"` to `~/.docker/config.json`.

If you skip the helper entirely, set `COVE_REGISTRY_TOKEN` or `GITHUB_TOKEN` and cove picks those up directly -- handy for CI where keychain access isn't available.

## Verifying a pulled image

```bash
cat ~/.vz/vms/<name>/disk.provenance
```

`disk.provenance` records the source manifest digest. It's written only after a successful atomic rename, so its presence is a proof that the disk image is complete and verified at pull time.

> [!NOTE]
> In v0.1 `disk.provenance` is unsigned and informational only. A local attacker with write access to the VM directory can edit it. Ed25519-signed provenance (embedded in aux storage) lands in v0.2.

## Snapshots as images

VM state and disk snapshots can be pushed as OCI tags:

```bash
cove snapshot push checkpoint1 ghcr.io/me/macos-15:checkpoint1
```

This keeps local snapshots using APFS `clonefile` (0-byte, 0-ms restore) but lets you publish a snapshot to a registry when you need to share it. See [Snapshots](../features/snapshots.md) for the local-side story.

## Related

- [HTTP API](../reference/http-api.md) -- pull and push over the HTTP control plane (v0.2).
- [Snapshots](../features/snapshots.md) -- local snapshot model and `cove snapshot push`.
