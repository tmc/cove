# cove → lume export

Status: dry-run only. Live publish is gated until the user reviews the
manifest shape against a real lume consumer.

## What lume publishes

Reference image: `ghcr.io/trycua/ubuntu-noble-vanilla:latest`.

Manifest shape:

```json
{
  "schemaVersion": 2,
  "mediaType": "application/vnd.oci.image.manifest.v1+json",
  "config": {
    "mediaType": "application/vnd.oci.empty.v1+json",
    "size": 2,
    "digest": "sha256:44136fa3...caaff8a"   // sha256("{}")
  },
  "layers": [
    {
      "mediaType": "application/vnd.oci.image.layer.v1.tar;part.number=1;part.total=41",
      "annotations": {"org.opencontainers.image.title": "disk.img.part.aa"},
      ...
    },
    // 41 disk parts, addressed aa..bo (two-letter base-26 from 1..52),
    // each ~500 MiB except the last
    {
      "mediaType": "application/vnd.oci.image.config.v1+json",
      "annotations": {"org.opencontainers.image.title": "config.json"}, ...
    },
    {
      "mediaType": "application/octet-stream",
      "annotations": {"org.opencontainers.image.title": "nvram.bin"}, ...
    }
  ],
  "annotations": {"org.opencontainers.image.created": "..."}
}
```

The disk parts byte-concatenate to a single gzipped tar stream. The tar
archive contains one regular file (the disk image). The `cpuCount` /
`memorySize` / `os` / `display` / `macAddress` / `diskSize` config blob
ships as a separate layer addressed by `org.opencontainers.image.title`.

## What cove now produces (dry-run)

`cove push --format lume --dry-run <vm> <ref>` builds the same shape.
On a 30 GB snap-bench VM with default chunk size:

- 35 parts at 512 MiB each (last part shorter)
- empty config blob with the OCI standard `{}` digest
- `config.json` sidecar carrying cove's projected lume config
- `nvram.bin` sidecar from cove's `aux.img` (32 MB on macOS)
- `org.opencontainers.image.created` annotation

The exporter and importer share `ociimage.LumeTarLayerMediaTypePrefix`
and the title constants, so what we produce can be parsed by what we
already consume — see `TestBuildLumeManifestRoundTripsThroughParser`.

## Cove → lume config projection

| Cove source                   | Lume field    | Default if missing |
|-------------------------------|---------------|--------------------|
| linux-disk.img exists         | `os: linux`   | `macos`            |
| `vmconfig.Config.CPU`         | `cpuCount`    | `4`                |
| `vmconfig.Config.MemoryGB`    | `memorySize`  | `4 << 30` (4 GiB)  |
| disk size (stat)              | `diskSize`    | n/a                |
| `mac.address` file (validated)| `macAddress`  | omitted            |
| n/a                           | `display`     | `1024x768`         |

Lume-only fields not surfaced from cove:

- `display` — cove doesn't track per-VM display preferences in
  `vmconfig.Config`. We emit a 1024x768 default; the consumer can
  override at runtime.

Cove-only fields not projected to lume:

- `vmconfig.Config.PostInstallRecipes` — cove-specific provisioning
  scripts; meaningless to lume.
- `vmconfig.Config.Volumes` — VirtioFS shared-folder mounts; lume
  doesn't define a peer.
- `vmconfig.Config.Agent` — cove's guest-agent capability tracking.
- `hw.model`, `machine.id` — macOS Virtualization framework hardware
  identity. Lume re-derives or randomizes these on first boot. We
  ship them as cove sidecars (not lume sidecars), so a lume consumer
  ignores them and a cove consumer would re-import them.

## Reverse-trip verification

`buildLumeManifest` output round-trips through `ociimage.IsLumeManifest`
+ `ociimage.ParseLumeManifest` cleanly:

```
TestBuildLumeManifestRoundTripsThroughParser  PASS
```

Manifest comparison (cove export vs lume reference image):

| Field                              | cove export | lume reference |
|------------------------------------|-------------|----------------|
| `schemaVersion`                    | 2           | 2              |
| `mediaType`                        | `application/vnd.oci.image.manifest.v1+json` | same |
| `config.mediaType`                 | `application/vnd.oci.empty.v1+json` | same |
| `config.digest`                    | `sha256:44136fa3...caaff8a` | same |
| `config.size`                      | 2           | 2              |
| disk layer mediaType prefix        | `application/vnd.oci.image.layer.v1.tar;part.number=N;part.total=M` | same |
| disk layer title                   | `disk.img.part.aa..` | same       |
| sidecar mediaTypes                 | `image.config.v1+json`, `octet-stream` | same |
| sidecar titles                     | `config.json`, `nvram.bin` | same    |
| top-level annotations              | `org.opencontainers.image.created` | same |

Lume reference image carries `artifactType: application/vnd.unknown.artifact.v1`
which our exporter does NOT emit. The OCI spec says clients SHOULD treat
the absence as fall-back to config mediaType — confirm against a real lume
consumer before relying on this.

## Known gaps and follow-ups

1. **Live publish path.** `--format lume` errors on non-dry-run today.
   The follow-up: `lumePushImage` mirroring `pushImage`'s upload calls
   (UploadBlob + PushManifest), but tar-streamed instead of LZ4'd. Needs
   reusable upload logic abstracted out of `push.go`.

2. **`artifactType` field.** Lume sets it; we don't. Probably harmless
   (clients fall back to `config.mediaType`) but should be confirmed.

3. **Streaming part split.** Today we tar+gzip to a temp file, then read
   it back to compute per-part digests. For a 30 GB VM this writes ~17 GB
   of temp (the gzip ratio). A streaming digest-as-you-tar implementation
   would halve disk I/O. Not urgent — registry uploads dominate runtime.

4. **`LumeConfig` decoder mismatch.** `internal/ociimage/lume.go`'s
   `DecodeLumeConfig` reads `cpu` (int) and `memory` (string) — but real
   lume images carry `cpuCount` (int) and `memorySize` (uint64 bytes).
   The import path's projection currently won't pick up CPU/memory from
   real lume images. The export code uses the correct schema (`lumeConfigOut`
   in `lume_push.go`). Filing the import-side fix as a follow-up — out
   of scope here.

5. **Display preferences.** Cove doesn't track these. If users want
   round-trip fidelity, add `Config.Display` to `internal/vmconfig`.

## Files touched

- `lume_push.go` (new): exporter logic
- `lume_push_test.go` (new): 8 tests covering part naming, config
  projection, part splitting math, manifest shape, reverse-trip
  parsing, observed schema match
- `push.go`: added `--format` flag and dispatch
- `docs/research/cove-lume-export.md` (this file)
