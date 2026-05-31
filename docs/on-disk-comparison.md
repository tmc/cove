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
| **Distribution** | OCI push/pull for cove images; compatibility push/pull for tart and lume formats; Lume tar-split pulls prefetch parts concurrently, preserve part order, and verify descriptor size/digest; OCI build-cache import/export; local image tar transfer for fleet (`cmd/cove/push.go`, `pull.go`, `lume_pull.go`, `fleet_image.go`) | first-class OCI registry push/pull | first-class OCI registry push/pull | pulls images onto workers through the chosen runtime |
| **Base/delta reuse** | cove-format chunked disks support `--base`, blob mount/reuse, base-manifest annotations, resumable pulls, local base-disk reuse, persistent registry-base materialization shared by builds and pulls, and portable build-cache layers via `--cache-from` / `--cache-to` | local layer cache with digest ranges and APFS share checks | legacy chunk annotations and concurrent reassembly | runtime-dependent |
| **Placement** | `fleet run --policy=least-loaded|image-affinity`; image-affinity can pre-stage a local image to the selected host; `fleet run --all` fans out concurrently to every non-cordoned host and pre-stages `-fork-from` only to cold targets; cordon/uncordon skips hosts for placement; short local placement leases count as pending load; `fleet health` reports remote reachability/version; repeated SSH operations reuse OpenSSH ControlMaster sockets; initial `cove-fleetd` control-plane boundary now accepts `coved -fleet-url` worker register/heartbeat/report dial-ins, tracks worker image refs, persists leased controller assignments, places assignments with least-loaded, image-affinity, or bin-pack policy plus anti-affinity hints, returns retained top-k placement plans, reconciles stale workers and expired leases, supports controller-side worker cordon/uncordon, queues fleet image-preparation pulls, image-GC assignments, lifecycle-policy assignments, and storage budget/prune assignments onto matching workers, reconciles fork warm-pool quotas into `cove run -fork-from` assignments, probes warm slots with `cove shell` before marking them ready, claims ready warm slots into same-worker guest exec assignments, stops claimed warm VMs after the guest command returns, downsizes warm pools by canceling pending surplus slots or queueing same-worker stop assignments for started surplus slots, exposes named warm-pool get/delete lifecycle operations with graceful claimed-slot deferral, persists controller audit events, binds service-account bearer tokens to audit actors, scopes assignment/warm-pool/service-account/audit resources by service-account namespace, enforces `viewer`/`operator`/`admin` service-account roles, renews active `cove` assignments, and executes leased `cove` assignments asynchronously on workers (`cmd/cove/fleet_run.go`, `cmd/cove/fleet_health.go`, `cmd/cove-fleetd`, `cmd/coved`, `internal/fleetcontrol`, `internal/coved`) | none | none | full controller/scheduler model |

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
   of another isolated format. Lume tar-split imports now fetch parts
   concurrently, write them back in manifest order, and verify each fetched part
   against the OCI descriptor before extraction.

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
   short local placement leases, concurrent fan-out, reused SSH transports, and
   explicit reachability checks: it skips cordoned hosts, counts recent local
   selections as pending load, can start the same run across all non-cordoned
   hosts, pre-stages `-fork-from` to cold fan-out targets, reports remote cove
   health/version, prefers a reachable host that already has `-fork-from`, and
   if none do, stages the local image to the least-loaded reachable host before
   running the VM there.

## Where competitors still lead

- **orchard has the more mature control plane.** cove's fleet support is CLI
  placement, fan-out, transfer, local cordon, short lease metadata, and an
  initial `cove-fleetd`/`coved -fleet-url` host-inventory, assignment-lease,
  reconciliation, worker cordon, fleet image preparation, fleet image-GC push,
  lifecycle-policy push, storage budget/prune push, and worker execution
  boundary with least-loaded, image-affinity, and slot-capped bin-pack
  placement plus anti-affinity hints,
  retained placement plans, and first warm-pool quota replenishment plus
  agent-ready slot claim into guest `Exec` with stop, downsize, and delete
  cleanup, plus a persisted controller audit feed, service-account actor
  binding, namespace-scoped controller resources, and basic service-account
  roles; orchard still owns richer SSO and controller operations.
- **tart has the mature public image lane.** cove now speaks tart format, but
  tart still has the established image catalog and local layer-cache machinery.
- **lume has native ecosystem defaults.** cove can interoperate with Lume
  tar-split images and now matches its concurrent part-fetch shape on import,
  but Lume still owns its native image defaults and user-facing ecosystem.

## One-line takeaway

cove's disk story is no longer "local-only raw disk plus clonefile." It is a VM
runtime with RAM-overlay disposability, live quota caps, optional ASIF disks,
multi-format OCI distribution including concurrent verified Lume tar-split
imports, base/delta reuse, portable OCI build caches, resumable pulls, and
image-aware fleet placement with cordon drains, local launch leases, and
concurrent multi-host run fan-out with cold-target image staging and reused SSH
transports. The first `cove-fleetd` plus `coved -fleet-url` control-plane
boundary is present with host inventory, assignment leases, stale-worker and
expired-lease reconciliation, controller-side worker cordon, fleet image
preparation, fleet image-GC push, lifecycle-policy push, storage budget/prune
push, and worker-side `cove` assignment execution plus
least-loaded/image-affinity/bin-pack placement with anti-affinity hints and
retained placement plans, warm-pool quota replenishment, and agent-ready slot
claim into same-worker guest `Exec` with stop, downsize, and delete cleanup,
plus a persisted audit feed with service-account actor binding, namespace
filters, and basic service-account roles, but tart and lume still lead in
ecosystem maturity, and orchard still leads as a full fleet controller.
