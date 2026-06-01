# On-disk storage comparison: cove vs tart vs lume vs orchard

Scope: how each macOS-VM tool lays out, clones, sizes, distributes, and resets
VMs on disk. Competitor notes are from the synced source snapshot in the
NotebookLM notebook `a2b42c2a-4522-47fd-b8ef-468e6cd7eb79`; cove notes reflect
this repo as of 2026-05-31.

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
| **Disk format** | `raw` by default, optional **ASIF** via `-disk-format=asif`; local image manifests record `disk_format`, and cove-native OCI manifests record `org.tmc.cove.disk-format`, so ASIF snapshots remain inspectable/verifiable locally and in registry metadata (`cmd/cove/utils.go`, `internal/diskimages2/create.go`, `cmd/cove/image.go`, `internal/ociimage/annotations.go`) | `raw` or **ASIF** via `diskutil image create --format ASIF` | OCI gzip single-layer or legacy LZ4 chunks; active disk is raw `disk.img` | n/a (delegates to runtime) |
| **Clone** | explicit `unix.Clonefile()` CoW, byte-copy fallback (`fork.go`, image materialization) | `FileManager.copyItem` -> implicit APFS CoW | OCI pull + reassemble; local `lume clone` | shells out to runtime clone |
| **Ephemeral reset** | **RAM-overlay** read-only parent with writes in host RAM (`fork_ephemeral.go`) | none; clone, boot, delete | none found | none; clone/delete lifecycle |
| **Layout** | `~/.vz/vms/<name>/`; images under `~/.vz/images/<name>/<tag>/`; OCI blobs/manifests in the content store | `<home>/vms/`; OCI cache under `<cache>/OCIs/<host>/<digest>` | configurable `LumeSettings.vmLocations` | generated worker VM dirs such as `orchard-<name>-<uid>-<restartCount>` |
| **Quota / size cap** | live APFS directory quota with runtime verb probe and Darwin fallback; persisted in `vmDir/quotas.json` (`internal/vmquota`) | no live cap; `tart set --disk-size` grows disk | none found | passes runtime disk-size knobs; no independent live cap |
| **VM-state snapshot** | VM-state snapshots plus disk clone snapshots (`snapshots.go`, `disk-snapshots/`) | suspend state only | none found | none found |
| **Distribution** | OCI push/pull for cove images with OCI index / Docker manifest-list resolution, explicit `--platform` child selection, pull dry-run index-selection reports, and registry-visible disk format annotations; compatibility push/pull for tart and lume formats; remote registry inspect for cove/Tart/Lume/cove-image artifacts before pull; Lume tar-split pulls prefetch parts concurrently, preserve part order, and verify descriptor size/digest; OCI build-cache import/export; local image tar transfer for fleet (`internal/ociimage/registry.go`, `cmd/cove/push.go`, `pull.go`, `image_remote_inspect.go`, `lume_pull.go`, `fleet_image.go`) | first-class OCI registry push/pull | first-class OCI registry push/pull | pulls images onto workers through the chosen runtime |
| **Base/delta reuse** | cove-format chunked disks support `--base`, blob mount/reuse, base-manifest annotations, resumable pulls, raw/ASIF-aware local base-disk reuse, persistent registry-base materialization shared by builds and pulls, and portable build-cache layers via `--cache-from` / `--cache-to` | local layer cache with digest ranges and APFS share checks | legacy chunk annotations and concurrent reassembly | runtime-dependent |
| **Placement** | `fleet run --policy=least-loaded|image-affinity`; image-affinity can pre-stage a local image to the selected host; `fleet run --all` fans out concurrently to every non-cordoned host and pre-stages `-fork-from` only to cold targets; cordon/uncordon skips hosts for placement; short local placement leases count as pending load; `fleet health` reports remote reachability/version; repeated SSH operations reuse OpenSSH ControlMaster sockets; initial `cove-fleetd` control-plane boundary now accepts `coved -fleet-url` worker register/heartbeat/report dial-ins, tracks worker image refs, persists leased controller assignments, places assignments with least-loaded, image-affinity, or bin-pack policy plus anti-affinity hints, returns retained top-k placement plans, reconciles stale workers and expired leases, supports controller-side worker cordon/uncordon/drain and operations-summary rollups, queues fleet image-preparation pulls, image-GC assignments, lifecycle-policy assignments, and storage budget/prune assignments onto matching workers, reconciles fork warm-pool quotas into `cove run -fork-from` assignments, probes warm slots with `cove shell` before marking them ready, claims ready warm slots into same-worker guest exec assignments, stops claimed warm VMs after the guest command returns, downsizes warm pools by canceling pending surplus slots or queueing same-worker stop assignments for started surplus slots, exposes named warm-pool get/delete lifecycle operations with graceful claimed-slot deferral and per-pool lifecycle status counts, exposes hosted-style `/v1/sandboxes` create/list/filter/page/get/delete/start/restart/stop/wait/lease/exec/control/events/reports/metering handles over fork-run assignments with ready probes, terminal waits, stop cleanup, retained-VM starts, restarts, enforced TTL-based exclusive modify leases, same-worker shell exec, same-worker screenshot/key/text/mouse control proxying, and per-resource active-interval metering records, persists filterable/paginated hash-chained controller audit events with global verification, binds service-account bearer tokens to audit actors, scopes assignment/warm-pool/sandbox/service-account/audit/metering resources by service-account namespace, enforces `viewer`/`operator`/`admin` service-account roles, ships OpenAI Agents Python `SandboxRunConfig` and public Go `agentsandbox` local/cloud clients with hosted lifecycle, paginated inventory, sandbox event/report history, metering, and GUI event support, renews active `cove` assignments, and executes leased `cove` assignments asynchronously on workers (`cmd/cove/fleet_run.go`, `cmd/cove-fleetd`, `cmd/coved`, `internal/fleetcontrol`, `internal/coved`, `agentsandbox`, `adapters/openai-agents-python`) | none | none | full controller/scheduler model |

## Where cove now leads

1. **RAM-overlay ephemeral forks.** cove can boot a child that shares the parent
   disk read-only and stores all writes in host RAM, so reset is shutdown and the
   dirty state disappears. APFS CoW clones are useful, but they still leave dirty
   blocks to clean up; this RAM-backed disposable mode is distinct. Run evidence
   now records fork source, optional registry source manifest digest, child,
   materialization mode, disk reuse, cleanup intent, verification, and
   limitations in `fork_created` metrics and derived `runs show` / GitHub
   summaries.

2. **Live quota caps.** cove caps the VM directory with APFS quotas and discovers
   whether the host OS still supports the `diskutil apfs setQuota` verb. tart and
   orchard's tart path expose disk growth, not a live on-disk ceiling.

3. **Registry interop, not just a native format.** cove can publish its own
   chunked OCI format and can also push/pull tart and lume-compatible images.
   That makes it a bridge across the two established registry ecosystems instead
   of another isolated format. Registry tags can resolve through OCI image
   indexes or Docker manifest lists before cove parses the child image manifest.
   Lume tar-split imports now fetch parts concurrently, write them back in
   manifest order, and verify each fetched part against the OCI descriptor
   before extraction. `cove image inspect -remote` fetches only registry
   metadata and identifies cove-native, Tart, Lume, and cove image-store
   artifacts before a disk pull, including index/list resolution details,
   selectable child manifests, selected platform, explicit platform child
   selection, optional all-platform child-manifest classification, disk format
   for cove-native/image-store artifacts, pull plan, cove base-chain
   disk-format/size/chunk compatibility for selected and all-platform cove
   child manifests, and
   verification posture; `-verify-blobs` HEAD-audits remote config/layer
   descriptors without downloading disks, including per-child audits when
   combined with `-all-platforms`, and multiple refs can be inspected as one
   batch for private catalog audits.

4. **ASIF-aware image metadata.** cove can create ASIF VM disks with
   DiskImages2, local image manifests record whether a captured disk is `raw`
   or `asif`, and cove-native OCI manifests carry the same disk-format fact.
   `image inspect`, `image verify`, `push --dry-run`, `pull --dry-run`, and
   `image inspect -remote` surface the format, so an ASIF-backed baseline stays
   auditable after it is snapshot into the local fork image store or published
   to a private registry.

5. **Base-aware distribution.** cove-format pushes can reference a base image,
   skip zero chunks, mount already-present blobs in the destination registry, and
   annotate the result with the base manifest. Pulls can resume interrupted
   downloads, reuse an already-materialized base disk where the manifest and
   detected disk format prove it is the right parent, and cache materialized
   registry bases with disk-format metadata for repeated builds and child pulls.
   Pull dry-runs can remain network-free, read local manifest JSON, or fetch
   only the registry manifest, then preflight compatible local or cached base
   disks and summarize local content-store coverage versus registry fetch work,
   including zero chunks and metadata blobs; the same pull plan is available as
   JSON for CI or fleet placement automation. Manifest-backed dry-runs report
   OCI index/list child candidates and selection, can force
   `--platform os/arch[/variant]`, then HEAD-audit the registry blobs this host
   would need for cove-native, Tart, or Lume pulls without downloading them.
   Remote inspect can apply the same base-chain compatibility and HEAD-only blob
   audits to every child of a mixed-platform index before choosing which disk to
   pull.
   Pull completion reports the actual reused chunk count, bytes, disk format,
   and base disk path when the base clone succeeds. Local
   images built from pulled VMs preserve the
   source registry manifest digest, image forks restore it to child
   `disk.provenance`, and store GC treats those image manifests as roots.
   Remote inspect walks declared base-manifest chains by digest for the selected
   manifest and, with `-all-platforms`, for each cove-native index child,
   reporting missing parents, raw/ASIF or size incompatibilities, reusable chunk
   counts, and reusable bytes before disk download. Builds can also import and
   export cove build-cache artifacts as OCI images, so cache entries and
   block-delta blobs can move between runners through the same private registry
   path as images.

6. **Image-aware, drainable fleet placement.** cove is not orchard's controller,
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
  agent-ready slot claim into guest `Exec` with stop, downsize, delete
  cleanup, and per-pool lifecycle status counts, hosted-style sandbox create/list/filter/page/get/delete/start/restart/stop/wait/lease/exec/control/events/reports/metering handles
  with enforced modify leases
  over fork-run assignments, OpenAI Agents Python and Go agentsandbox local/cloud
  provider switches with hosted lifecycle, list filters, pagination, sandbox event/report history, metering, and GUI events, plus persisted
  per-resource sandbox usage records, a
  filterable/paginated hash-chained controller audit feed, service-account actor binding,
  namespace-scoped controller resources, basic service-account roles, RS256 OIDC
  bearer bindings with issuer discovery/JWKS refresh, and fail-closed SAML IdP
  binding records with validated X.509 signing certificates, plus worker drain
  for hosted sandbox maintenance and a reconciled operations summary; orchard
  still owns complete SAML assertion authentication and broader production
  controller operations.
- **tart has the mature public image lane.** cove now speaks tart format, but
  tart still has the established image catalog and local layer-cache machinery.
- **lume has native ecosystem defaults.** cove can interoperate with Lume
  tar-split images and now matches its concurrent part-fetch shape on import,
  but Lume still owns its native image defaults and user-facing ecosystem.

## One-line takeaway

cove's disk story is no longer "local-only raw disk plus clonefile." It is a VM
runtime with RAM-overlay disposability, live quota caps, optional ASIF disks
whose format survives local and cove-native OCI metadata, multi-format OCI
distribution including explicit OCI index platform selection, concurrent
verified Lume tar-split imports, base/delta reuse, portable OCI build caches,
resumable pulls, and
image-aware fleet placement with cordon and controller worker drains, local launch leases, and
concurrent multi-host run fan-out with cold-target image staging and reused SSH
transports. The first `cove-fleetd` plus `coved -fleet-url` control-plane
boundary is present with host inventory, assignment leases, stale-worker and
expired-lease reconciliation, controller-side worker cordon, operations
summary, fleet image preparation, hosted sandbox worker drain, fleet image-GC push, lifecycle-policy push, storage budget/prune
push, and worker-side `cove` assignment execution plus
least-loaded/image-affinity/bin-pack placement with anti-affinity hints and
retained placement plans, warm-pool quota replenishment, and agent-ready slot
claim into same-worker guest `Exec` with stop, downsize, delete cleanup, and
per-pool lifecycle status counts,
hosted-style sandbox create/list/filter/page/get/delete/start/restart/stop/wait/lease/exec/control/events/reports/metering
handles over fork-run assignments with enforced modify leases, OpenAI Agents Python and Go agentsandbox local/cloud provider
switches with hosted lifecycle, list filters, pagination, sandbox event/report history, metering, and GUI events, persisted per-resource sandbox usage
records, plus a filterable/paginated hash-chained audit feed with service-account actor binding,
namespace filters, basic service-account roles, RS256 OIDC bearer bindings with
issuer discovery/JWKS refresh, and fail-closed SAML IdP binding records, but
tart and lume still lead in
ecosystem maturity, and orchard still leads as a full fleet controller.
