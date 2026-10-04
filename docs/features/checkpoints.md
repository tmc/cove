# Cold checkpoints

`cove checkpoint` captures and restores a declared set of stopped-VM files.
It is separate from disk-only snapshots, PIT experiments and identity-changing
forks. This first implementation supports macOS and EFI-booted Linux bundles.
Live checkpoints and paired guest memory are unavailable.

```sh
cove checkpoint plan -json muse-lab
cove checkpoint save -disk data.img muse-lab baseline
cove checkpoint inspect muse-lab baseline
cove checkpoint restore muse-lab baseline
cove checkpoint recover muse-lab
```

Flags precede the VM name. `-disk` explicitly declares another regular disk file
inside the bundle; there is no persisted runtime attachment inventory to infer
additional devices from. Review `plan` against the devices used by the last
run. Runtime-only attachments, external files, raw devices, USB/hotplug devices
and host shared-folder contents are outside this cold checkpoint contract.
Stop and detach unsupported devices before capturing a checkpoint. A plan
lists file coverage; it does not certify that the VM is stopped.

macOS captures the primary disk, auxiliary storage, hardware model and machine
identity. Linux captures its primary disk, machine identity and present EFI
stores. Present saved configuration and known boot files are included. Every
source must be a regular bundle-local file without symlink components. Direct
kernel Linux, unsupported platforms, saved `suspend.vmstate` and opaque
`framework-config.vzcfg` are refused before checkpoint data is created. An old
suspend file is not evidence of a memory/disk pair.

Capture and restore hold the VM runtime lock and check control sockets,
matching VM processes and every target's open-file owners. The manifest records
file roles, sizes, SHA-256 hashes and a guest-identity compatibility fingerprint.
Restore requires the same guest identity; it does not silently adopt fork
identity. These checks require cooperative local ownership, rather than hostile
writers bypassing the locks.

Checkpoint publication is atomic. Restore first clones and verifies both the
replacement files and original backups, durably publishes a restore journal,
then installs files. Until installation finishes, the VM remains unavailable.
An interruption leaves `.checkpoint-restore`; running the VM, cloning it and
`cove push` refuse this marker. `checkpoint recover` rolls an uncommitted
transaction back or verifies and cleans an already committed transaction.
Recovery can itself be interrupted and repeated. Unexpected outside writes
cause recovery to stop while preserving its journal and backups.

The lock order is runtime `run.lock` first when required, then
`.checkpoint.operations.lock`. Capture, restore and recovery take an exclusive,
nonblocking operation lock. `CloneVM` and the complete `cove push` command hold
shared operation locks, so neither can begin or continue through restore
publication. `CloneVM` does not reacquire `run.lock`: snapshot-seeded fork
callers already hold it. The operation lock does not change ordinary cloning's
existing behavior for active sources.

This reader protection covers those clone and push entry points only. Other
export/image/PIT/disk-snapshot tools and arbitrary host readers are not covered;
do not run them concurrently with checkpoint operations. Runtime locks prevent
cooperating VM launch and stopped-disk operations while a checkpoint runs.
Direct internal push-plan helpers check pending recovery but do not hold a lock
through subsequent upload.

Tests exercise exact multi-file restoration, interrupted restore and rollback
phases, corrupted files, identity mismatch, outside writes, reader exclusion
and fork-style lock reuse. They model interruptions at transaction boundaries.
Physical guest boot/task restoration, paired memory, SDK device inventory,
power-loss testing and generic reader isolation remain unqualified.
