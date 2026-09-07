# ASIF installation durability

One paired install on host macOS 27 seed 26A5425a reproduced the failure with
CacheEphemeral and avoided it with CacheDurable. Both used the cached macOS
26.6.2 (25G83) IPSW, a fresh 40 GiB ASIF disk, 2 CPUs, and 4 GiB RAM. Both
installers reported completion. The comparison binaries used the P1 source;
the old-policy binary changed only the installer attachment policy through a Go
build overlay outside the checkout.

| Policy | Active directory | Sequence | Table 0 pointer | Read-only layout | First boot |
| --- | --- | --- | --- | --- | --- |
| CacheEphemeral (None) | 0x41400 | 1 | 0 | Empty logical disk | Black capture |
| CacheDurable (Fsync) | 0x200 | 2 | 4 | GPT and APFS volumes | Hello screen |

The old-policy image still had the physical GPT signature at 0x500200 and the
allocation table at chunk 4, but neither directory referred to that table.
The new-policy image retained the older directory at 0x41400 with a zero pointer;
its newer active directory at 0x200 referred to table 4. Checking only the fixed
offset 0x41408 would therefore incorrectly diagnose the healthy image.
Directory selection follows the highest sequence number, as described in the
[ASIF format research](https://github.com/huven/asif-format).

This single controlled pair supports the synchronization-policy diagnosis on
this host. It does not establish a failure rate or a guarantee across host seeds.
The boot screenshots are supporting evidence; diskutil's read-only partition
layout and both directory records establish the disk-readability difference.

## Post-install check

Both GUI and sequential installation now stop the installer VM, wait for the
disk to become available, and attach it read-only without mounting. The check
requires Apple's disk-image stack to expose a GUID partition scheme and an APFS
partition/container. It ejects the device before proceeding, including when
layout validation fails or the parent context is canceled. Stop, availability,
layout, and cleanup failures propagate as installation errors. Installation
Complete is printed only after the check succeeds.

The check prefers diskutil image attach when available and retains hdiutil for
older hosts. It validates logical partition readability, not the integrity of
every APFS file or sealed-system block. It contains no ASIF offset parser.

Tests cover modern and older attach output, absent GPT or APFS, empty logical
disks, malformed metadata, attachment failure, failed ejection, and cleanup after
cancellation. A standalone build of the same preflight code rejected the preserved
original corrupt disk and accepted both the repaired backup and the fresh durable
trial. All three devices were ejected after checking.

`go build ./...` and `go test ./... -skip '^TestPrivateAPI_NameGetSet$'` passed.
The one excluded pre-existing native trap is documented in the P0 report.

## Artifacts

`/tmp/cove-p2-20260906/` contains the policy overlay, binaries, installation logs,
read-only layout plists, result JSON (both directory records), first-boot PNGs,
test logs, and the first 6 MiB of each image for metadata/GPT examination.
The disposable disk payloads were removed after capture to reclaim space;
bundle metadata was retained there. The original corrupt default disk and the
pre-P1 backup remain unchanged. The real default remains running with its agent.
