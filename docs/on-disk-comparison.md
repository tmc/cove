# On-disk storage comparison: cove vs tart vs lume vs orchard

Scope: how each macOS-VM tool lays out, clones, sizes, distributes, and resets
VMs on disk. Competitor notes are from the synced source snapshot in the
NotebookLM notebook `a2b42c2a-4522-47fd-b8ef-468e6cd7eb79`; cove notes reflect
this repo as of 2026-05-30.

## Tools

| Tool | Lang | Layer | Repo |
|------|------|-------|------|
| **cove** | Go (purego Virtualization.framework) | VM runtime, image store, OCI bridge, build cache, lightweight fleet CLI | github.com/tmc/cove (this repo) |
| **tart** | Swift | single-host VM runtime + OCI dist | github.com/cirruslabs/tart |
| **lume** | Swift | single-host VM runtime + OCI dist | github.com/trycua/cua (libs/lume) |
| **orchard** | Go | fleet orchestrator over tart/vetu | github.com/cirruslabs/orchard |

orchard is not a direct runtime peer: it schedules VMs onto worker hosts and
delegates the disk/runtime work to `tart` or `vetu`.

## On-disk mechanics matrix

| Dimension | cove | tart | lume | orchard |
|-----------|------|------|------|---------|
| **Disk format** | `raw` by default, optional **ASIF** via `-disk-format=asif` (`cmd/cove/utils.go`, `internal/diskimages2/create.go`) | `raw` or **ASIF** via `diskutil image create --format ASIF` | OCI gzip single-layer or legacy LZ4 chunks; active disk is raw `disk.img` | n/a (delegates to runtime) |
| **Clone** | explicit `unix.Clonefile()` CoW, byte-copy fallback (`fork.go`, image materialization) | `FileManager.copyItem` -> implicit APFS CoW | OCI pull + reassemble; local `lume clone` | shells out to runtime clone |
| **Ephemeral reset** | **RAM-overlay** read-only parent with writes in host RAM (`fork_ephemeral.go`) | none; clone, boot, delete | none found | none; clone/delete lifecycle |
| **Layout** | `~/.vz/vms/<name>/`; images under `~/.vz/images/<name>/<tag>/`; OCI blobs/manifests in the content store | `<home>/vms/`; OCI cache under `<cache>/OCIs/<host>/<digest>` | configurable `LumeSettings.vmLocations` | generated worker VM dirs such as `orchard-<name>-<uid>-<restartCount>` |
| **Quota / size cap** | live APFS directory quota with runtime verb probe and Darwin fallback; persisted in `vmDir/quotas.json` (`internal/vmquota`) | no live cap; `tart set --disk-size` grows disk | none found | passes runtime disk-size knobs; no independent live cap |
| **VM-state snapshot** | VM-state snapshots plus disk clone snapshots (`snapshots.go`, `disk-snapshots/`) | suspend state only | none found | none found |
| **Distribution** | OCI push/pull for cove images; compatibility push/pull for tart and lume formats; OCI build-cache import/export; local image tar transfer for fleet (`cmd/cove/push.go`, `pull.go`, `fleet_image.go`) | first-class OCI registry push/pull | first-class OCI registry push/pull | pulls images onto workers through the chosen runtime |
| **Base/delta reuse** | cove-format chunked disks support `--base`, blob mount/reuse, base-manifest annotations, resumable pulls, local base-disk reuse, persistent registry-base materialization shared by builds and pulls, and portable build-cache layers via `--cache-from` / `--cache-to` | local layer cache with digest ranges and APFS share checks | legacy chunk annotations and concurrent reassembly | runtime-dependent |
| **Placement** | `fleet run --policy=least-loaded|image-affinity`; image-affinity can pre-stage a local image to the selected host; cordon/uncordon skips hosts for placement; short local placement leases count as pending load; `fleet health` reports remote reachability/version (`cmd/cove/fleet_run.go`, `cmd/cove/fleet_health.go`) | none | none | full controller/scheduler model |

## Where cove now leads

1. **RAM-overlay ephemeral forks.** cove can boot a child that shares the parent
   disk read-only and stores all writes in host RAM, so reset is shutdown and the
   dirty state disappears. APFS CoW clones are useful, but they still leave dirty
   blocks to clean up; this RAM-backed disposable mode is distinct.

2. **Live quota caps.** cove caps the VM directory with APFS quotas and discovers
   whether the host OS still supports the `diskutil apfs setQuota` verb. tart and
   orchard's tart path expose disk growth, not a live on-disk ceiling.

3. **Registry interop, not just a native format.** cove can publish its own
   chunked OCI format and can also push/pull tart and lume-compatible images.
   That makes it a bridge across the two established registry ecosystems instead
   of another isolated format.

4. **Base-aware distribution.** cove-format pushes can reference a base image,
   skip zero chunks, mount already-present blobs in the destination registry, and
   annotate the result with the base manifest. Pulls can resume interrupted
   downloads, reuse an already-materialized base disk where the manifest proves
   it is the right parent, and cache materialized registry bases for repeated
   builds and child pulls. Builds can also import and export cove build-cache
   artifacts as OCI images, so cache entries and block-delta blobs can move
   between runners through the same private registry path as images.

5. **Image-aware, drainable fleet placement.** cove is not orchard's controller,
   but its fleet CLI now understands image locality, operator drain intent, and
   short local placement leases, and explicit reachability checks: it skips
   cordoned hosts, counts recent local selections as pending load, reports
   remote cove health/version, prefers a reachable host that already has
   `-fork-from`, and if none do, stages the local image to the least-loaded
   reachable host before running the VM there.

## Where competitors still lead

- **orchard has a real control plane.** cove's fleet support is CLI placement and
  transfer with local cordon and short lease metadata; orchard still owns
  reconciliation, durable leases, worker lifecycle, and scheduler/controller
  concerns.
- **tart has the mature public image lane.** cove now speaks tart format, but
  tart still has the established image catalog and local layer-cache machinery.
- **lume's legacy chunk transport is elaborate.** cove can interoperate with
  lume tar-split images, but lume still owns its native concurrent chunk
  reassembly pipeline and ecosystem defaults.

## One-line takeaway

cove's disk story is no longer "local-only raw disk plus clonefile." It is a VM
runtime with RAM-overlay disposability, live quota caps, optional ASIF disks,
multi-format OCI distribution, base/delta reuse, portable OCI build caches,
resumable pulls, and
image-aware fleet placement with cordon drains and local launch leases. tart and
lume still lead in ecosystem maturity, and orchard still leads as a full fleet
controller.
