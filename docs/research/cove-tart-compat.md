# cove ↔ tart OCI compatibility

## Goal

Make `cove pull` accept `cirruslabs/tart` images directly, and make
`cove push --format tart` produce images that `tart pull` can consume.

Symmetric to the lume compat shipped in `2d9af57` (push) and the dispatch
machinery in `d5bf2fc` (pull integration tests).

## Tart format facts

Verified against the cirruslabs/tart Swift sources at
`~/go/src/github.com/cirruslabs/tart/Sources/tart/`.

### Manifest envelope (`OCI/Manifest.swift`)

```
schemaVersion: 2
mediaType:     application/vnd.oci.image.manifest.v1+json
config:        application/vnd.oci.image.config.v1+json   ← stub for Docker Hub
layers:        [config, disk×N, nvram]
```

The `config` blob is an `OCIConfig` with only `architecture` (`arm64`),
`os` (`darwin`), and a `config.Labels` map. It exists for Docker Hub
compatibility; tart does not read it back.

### Layer media types

| MediaType                                          | Role         | Notes |
|----------------------------------------------------|--------------|-------|
| `application/vnd.cirruslabs.tart.config.v1`        | VMConfig JSON | exactly one |
| `application/vnd.cirruslabs.tart.disk.v2`          | Disk chunk   | one per 512 MiB |
| `application/vnd.cirruslabs.tart.disk.v1`          | Legacy disk  | refused on pull |
| `application/vnd.cirruslabs.tart.nvram.v1`         | NVRAM blob   | exactly one |

### Disk chunking (`OCI/Layerizer/DiskV2.swift`)

- **Layer size limit**: `layerLimitBytes = 512 MiB` per layer (hardcoded).
- **Buffer size**: 4 MiB internal compression buffer.
- **Hole granularity**: 4 MiB; tart skips writing all-zero blocks but does
  not annotate them in the manifest. Pull uses `truncate(2)` to pre-size
  the output disk to the sum of `uncompressed-size` annotations, so
  unwritten regions stay sparse.
- **Compression**: `(data as NSData).compressed(using: .lz4)` — Apple's
  `Compression` framework's LZ4 algorithm. **Not the LZ4 frame spec
  (`0x184D2204`).** Apple's `.lz4` produces a *block-stream* format
  documented in `<compression.h>`:

  ```
  ┌──────────┬─────────┬─────────┬─────────────┐
  │ 'bv41'   │ raw_sz  │ comp_sz │ LZ4 block…  │   compressed block
  └──────────┴─────────┴─────────┴─────────────┘
  ┌──────────┬─────────┬─────────────┐
  │ 'bv4-'   │ raw_sz  │ raw bytes…  │              uncompressed block
  └──────────┴─────────┴─────────────┘
  ┌──────────┐
  │ 'bv4$'   │                                       end-of-stream
  └──────────┘
  ```

  Cove ships its own pure-Go decoder/encoder for this framing (see
  `internal/ociimage/applelz4.go`). The actual LZ4 block decompression is
  handled by `pierrec/lz4`'s `lz4.UncompressBlock`, which already supports
  the raw block format Apple wraps.

### Layer annotations (per disk layer)

```
org.cirruslabs.tart.uncompressed-size           ← uncompressed bytes (UInt64)
org.cirruslabs.tart.uncompressed-content-digest ← sha256 of uncompressed bytes
```

Both annotations are required on every disk layer. Cove's pull validates
they are present and rejects the manifest if either is missing
(`OCIError.LayerIsMissing*Annotation` in tart).

### Manifest annotations

```
org.cirruslabs.tart.uncompressed-disk-size   ← total uncompressed bytes (UInt64)
org.cirruslabs.tart.upload-time              ← ISO8601 timestamp
```

### Manifest labels (in OCIConfig.config.Labels, not annotations)

```
org.cirruslabs.tart.disk.format              ← raw|compressed (cove only emits raw)
```

## Detection rule

`ociimage.IsTartManifest` returns true iff:

1. **Positive signal**: at least one layer's `mediaType` starts with
   `application/vnd.cirruslabs.tart.`.
2. **Negative signal (rejection)**: no cove annotations on the manifest
   (CoveUncompressedDiskSize, CoveHWModelDigest, CoveAuxDigest,
   CoveUploadTime, plus the lume-aliased keys), AND no lume tar layer
   (`application/vnd.oci.image.layer.v1.tar` prefix or `disk.img.part.*`
   title).

Both signals are needed: a tart manifest never carries cove or lume
markers, and a cove or lume manifest will not carry the tart media-type
prefix. The rejection clause guards against edge cases where someone
mixes annotations across formats — cove's lume detection already does
the same to keep dispatch deterministic.

The dispatch order in `ociimage.ParseManifest` is:

```go
if IsLumeManifest(m) { … FormatLume … }
if IsTartManifest(m) { … FormatTart … }
… FormatCove fallback …
```

Lume goes first because it has the stricter negative signal (it rejects
*all* cove-style annotations, which a tart manifest doesn't carry but a
cove one might if it was lume-compat-tagged).

## Implementation phases

Each phase is one atomic commit on `feat/cove-tart-compat`.

### Phase 1: pull (read-only)

- `internal/ociimage/applelz4.go` — pure-Go encoder/decoder for Apple's
  LZ4 block-stream framing. `Decompress(io.Reader) (io.Reader, error)`
  and `Compress(io.Reader) (io.Reader, error)` plus a one-shot byte API.
- `internal/ociimage/tart.go` — `IsTartManifest`, `ParseTartManifest`,
  `TartManifest` struct (`ConfigLayer *Descriptor`, `NVRAMLayer
  *Descriptor`, `DiskLayers []TartDiskLayer`).
- `ociimage.ParseManifest` — insert tart branch between lume and cove.
- `ParsedManifest.Tart TartManifest` — populated when `Format ==
  FormatTart`.
- `tart_pull.go` — `tartPullDisk` and `tartPullSidecar` mirroring
  `lume_pull.go`. Pre-truncate the disk to the manifest's uncompressed
  size annotation, then for each disk layer fetch+decompress+`WriteAt`
  at the cumulative offset. Parallel up to `pullChunkWorkers`.
- `pull.go` — add `case FormatTart:` to both the run dispatch (line ~60)
  and the dry-run printer (line ~448).

### Phase 1a: tart VMConfig → cove vmconfig

Tart's `VMConfig` (`Sources/tart/VMConfig.swift`) maps onto cove's
`vmconfig.Config` as:

| tart field      | cove field        | Notes |
|-----------------|-------------------|-------|
| `cpuCount`      | `CPU`             | direct (Int → uint) |
| `memorySize`    | `MemoryGB`        | UInt64 bytes → uint64 GB (round down, refuse <1 GB) |
| `display.width` | display config    | persist if cove models it |
| `display.height`| display config    | persist if cove models it |
| `os`            | implicit          | drop — cove infers from disk |
| `arch`          | implicit          | drop — cove is arm64-only |
| `cpuCountMin`/`memorySizeMin` | nothing | drop — cove doesn't enforce minimums |
| `macAddress`    | nothing           | drop — cove regenerates |
| `displayRefit`  | nothing           | drop |
| `diskFormat`    | nothing           | drop — cove only handles raw |
| `platform`      | nothing           | drop — Darwin/Linux nested fields |

Original tart config persists alongside as `tart-config.json` (mirrors
lume's `lume-config.json`) for round-trip fidelity.

### Phase 2: push (`--format tart`)

`tart_push.go` mirroring `lume_push.go`:

1. Read cove vmconfig, project to tart VMConfig JSON, push as the config
   layer (mediaType `application/vnd.cirruslabs.tart.config.v1`).
2. Chunk `disk.img` at 512 MiB layers, compress each with the Apple-LZ4
   encoder, push as `application/vnd.cirruslabs.tart.disk.v2` with
   `uncompressed-size` and `uncompressed-content-digest` annotations.
3. Push `nvram.bin` as one `application/vnd.cirruslabs.tart.nvram.v1`
   layer.
4. Build a stub OCIConfig (`{"architecture":"arm64","os":"darwin",
   "config":{"Labels":{"org.cirruslabs.tart.disk.format":"raw"}}}`),
   push as the manifest's `config` blob.
5. Build the tart manifest with the manifest annotations
   (`uncompressed-disk-size`, `upload-time`) and push.

`push.go` adds `--format tart` to the format flag's allowed set and
dispatches to `tartPush` when selected. `--dry-run` lands first; live
push is a separate commit if/when ready.

### Phase 3: integration tests

Mirror `integration_oci_test.go`'s `TestPullDispatch_*` triplet:

- `TestPullDispatch_TartManifest` — serves a tart manifest fixture,
  asserts `plan.Manifest.Format == FormatTart` and `Tart.DiskLayers` is
  populated.
- `TestPullDispatch_TartReverseTrip` — runs `tartPush --dry-run`,
  serves the resulting manifest, re-parses through `ParseManifest`,
  asserts the tart branch consumes its own output.

Three formats × three test cases = 9 integration tests after this lands.

## Why an Apple-LZ4 codec instead of cgo

`Compression.framework` ships as `libcompression.dylib` on every macOS
host, so a cgo binding would be one `import "C"` away. Reasons to write
a pure-Go decoder instead:

1. **`internal/ociimage` has no cgo today.** Adding it would force every
   future test to build with cgo enabled. Pure-Go keeps the package
   trivially cross-compilable.
2. **The framing is small and stable.** Apple has not changed the
   `bv41`/`bv4-`/`bv4$` format since macOS 10.11. The implementation is
   ~150 lines: read 4-byte magic, dispatch on it, delegate the LZ4 raw
   block to `pierrec/lz4.UncompressBlock` (which already exists in
   `go.mod`).
3. **Symmetric encoder is also simple.** Push needs the inverse, and
   doing both in pure Go keeps cove's tart blobs deterministic across
   hosts (cgo would defer compression decisions to whatever
   libcompression version the host has).
4. **Testing is straightforward.** Round-trip random data through
   compress→decompress, plus a fixture from a real tart push to
   guarantee wire compatibility.

## Open questions / deferred

- **`tart://` URL scheme** — registry detection alone is enough for
  ergonomics; the URL prefix would be a separate ~50-line change.
  Defer to v0.3.
- **vetu compat** — same media-type family, but cove is ARM64-only on
  macOS. Vetu (x86_64 Linux) would need cross-arch VM support first.
- **DiskV2's local layer cache** — tart caches pulled blobs at
  `~/.tart/cache` and uses content digests to deduplicate against the
  destination disk. Cove doesn't have this today; pull ignores it. The
  on-disk format is local-only, so skipping it is wire-compatible.
- **Apple's `.lz4` vs LZ4 frame spec interop** — cove's existing
  manifest format uses `pierrec/lz4`'s frame format, which is *not*
  what tart writes. The Apple-LZ4 codec is tart-specific; cove's own
  manifests continue using the frame format. Both live side-by-side in
  `internal/ociimage`.

## Acceptance

- `cove pull ghcr.io/cirruslabs/macos-sequoia-base:latest` succeeds
  end-to-end and produces a runnable cove VM (boots, shows Apple
  logo + login prompt).
- `cove push --format tart --dry-run` produces a manifest whose
  field-by-field shape matches a captured `tart push` output (modulo
  timestamps and digests).
- Reverse trip: `cove push --format tart` then `tart pull` against the
  resulting reference, if a tart binary is available locally.

## Files

| Path                                       | Purpose |
|--------------------------------------------|---------|
| `internal/ociimage/applelz4.go`            | Apple LZ4 framing decoder/encoder |
| `internal/ociimage/applelz4_test.go`       | round-trip + fixture tests |
| `internal/ociimage/tart.go`                | IsTartManifest, ParseTartManifest, TartManifest |
| `internal/ociimage/tart_test.go`           | manifest detection, parse, golden manifest |
| `internal/ociimage/manifest.go`            | dispatch wiring (small) |
| `tart_pull.go`                             | tartPullDisk, tartPullSidecar, tart-config translation |
| `tart_pull_test.go`                        | per-piece unit tests |
| `tart_push.go`                             | tartPush dry-run + (later) live push |
| `tart_push_test.go`                        | manifest shape, projection, layer encode |
| `pull.go`                                  | add `case FormatTart` in run + dry-run dispatch |
| `push.go`                                  | wire `--format tart` |
| `integration_oci_test.go`                  | TestPullDispatch_TartManifest + reverse trip |
| `docs/research/cove-tart-compat.md`        | this brief |
