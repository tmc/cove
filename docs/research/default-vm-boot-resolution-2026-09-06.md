# Default VM boot restored

The fresh macOS 26.6.2 installation now cold-boots to Setup Assistant and renders
correctly. No reinstall, replacement guest, or host security changes were needed.
The original 8 CPU / 32 GiB configuration is restored. The VM is left running.

This supersedes the suspected kernel-handoff regression in
[the handoff](default-vm-boot-handoff-2026-09-06.md).

## Confirmed fault and repair

The ASIF disk contained the installed data, but its active directory had no pointer
to the allocation table covering the guest disk. Apple's `diskutil image info`
reported all 40 GiB empty. Read-only attachment exposed a disk with no partitions;
logical sector zero read as zeroes. Merely recognizing the file as ASIF did not
establish that its contents were accessible.

The primary GPT signature was present at physical file offset `0x500200`. A populated
allocation table at physical chunk 4 mapped logical chunk zero to physical chunk 5.
The active directory at `0x41400` had sequence number 1, but its first table pointer
at `0x41408` was zero. On an APFS clone, changing that eight-byte big-endian pointer
to `4` restored the GPT and all APFS containers. Apple's tools recognized the sealed
system volume, Preboot, Data, and recovery volumes. This is a repair for this specific
image, not a general-purpose ASIF repair recipe.

The directory/table layout was checked against the
[ASIF format description](https://github.com/huven/asif-format). Both directory
selection and the recovered table were checked before changing the copy.

The repaired copy replaced `default.covevm/disk.img`. The original remains at:

```
~/.vz/vms/default.covevm/disk.asif-before-directory-repair
```

The files initially share their data through APFS cloning; retaining the backup does
not initially duplicate the entire 22 GiB image.

## Boot diagnosis

- Cove already attached a virtio serial port. Separate captures stayed empty, even
  with guest NVRAM `boot-args=-v serial=3`.
- PL011 configuration validated but VM startup rejected it.
- The working `mlx-lm` control reached its desktop and reconnected its agent in
  roughly 16 seconds.
- LLDB attached to the default VM's XPC service. At `hv_vcpu_run`, reading guest
  registers on the owning CPU thread returned PC `0x70097f20`, LR `0x700980a0`.
  Another sample returned PC `0x70097ef8`, LR `0x7007fa34`.
- Guest memory contained the decompressed auxiliary-store LLB. These addresses
  belonged to firmware polling the USB boot-device interface at `0x30100000`,
  including its readiness register at offset `0x10`. This was not evidence of
  kernel execution. Removing keyboard, pointing device, and USB controller did
  not resolve it.
- Guest NVRAM contained `boot-command=recover-system`,
  `recovery-boot-mode=iboot`, and `iboot-failure-reason=0x65`.

After the disk repair, the guest rendered Startup Disk. Setting the guest's
`boot-command=fsboot` allowed normal startup to the hello screen. Then both
`boot-command` and temporary `boot-args` were removed. An ordinary `cove -vm default
run`, with no recovery or resume override, again reached the hello screen.

The private auxiliary-storage NVRAM methods assert without their entitlement.
For this diagnosis, LLDB changed only the helper process's in-memory entitlement
cache, after `VzCore::VirtualizationEntitlements::from_current_process()` returned.
No host NVRAM, SIP, AMFI, or on-disk framework code was changed. The address used
(`0x27fa34ec8`) is specific to host build 26A5425a; it is not a portable interface.

## Prevention and validation

The macOS installer used `CacheEphemeral`, which selects synchronization mode
`None`. It now uses `CacheDurable`, selecting `Fsync`, so installation does not
explicitly suppress guest disk synchronization. Loss of the directory pointer is
confirmed; attributing it specifically to synchronization mode still requires an
independent reproduction. Do not treat the policy change as a proven complete fix
for every ASIF durability issue.

- `go build ./...`: passed.
- Fresh cove binary built and signed with the virtualization entitlement.
- Live validation: repaired guest rendered recovery, then Setup Assistant, then
  Setup Assistant again after removing temporary NVRAM settings and cold-starting.
- `go test ./...`: failed to compile the existing test suite at
  `cmd/cove/private_api_display_test.go:458`: undefined
  `privvz.NewMacGraphicsDisplayWithConfigurationError`. Other reported packages
  passed or had no tests.

Artifacts, diagnostic helpers, NVRAM backups, logs, and screenshots:

```
/tmp/cove-boot-debug-20260906/
```

The running signed binary is `cove` in that directory. `final.jpg` shows the final
ordinary launch. Guest account creation remains at Setup Assistant. No binary
artifacts are staged.
