# Design 028: Block Device Passthrough

Status: Draft
Author: Travis Cline
Date: 2026-05-03

## Problem

Design 027 removes the worst disk-image sync bottleneck by selecting explicit
`VZDiskImageStorageDeviceAttachment` cache and sync modes. The next ceiling is
APFS-backed sparse files themselves: every guest write still flows through a
host file, allocation policy, and filesystem metadata.

For benchmark and appliance workflows, cove should be able to attach a raw
macOS disk device such as `/dev/rdisk8` directly to the guest. The
Virtualization framework exposes this as
`VZDiskBlockDeviceStorageDeviceAttachment`.

## Binding Shape

The public Go binding constructor is:

```go
vz.NewDiskBlockDeviceStorageDeviceAttachmentWithFileHandleReadOnlySynchronizationModeError(
	fileHandle foundation.NSFileHandle,
	readOnly bool,
	synchronizationMode vz.VZDiskSynchronizationMode,
)
```

The Foundation binding has the ownership-aware constructor cove should use for
received descriptors:

```go
foundation.NewFileHandleWithFileDescriptorCloseOnDealloc(fd, true)
```

The returned attachment can be passed to
`vz.NewVirtioBlockDeviceConfigurationWithAttachment` like the existing disk
image attachment.

## Privilege Boundary

Do not run the VM as root. Apple documents the block-device attachment as taking
an open `NSFileHandle`, and raw disk devices normally require root to open. Cove
already has the right boundary: `cove-helper` runs as root, authenticates peers
by UID, and accepts typed requests over `/var/run/cove-helper.sock`.

Add a new helper operation instead of extending `apply_manifest`. The existing
manifest path is JSON-in/JSON-out and is built for finite file operations. Block
device passthrough needs descriptor passing with `SCM_RIGHTS`, so it should be a
separate request and response path.

```go
type helperRequest struct {
	Op              string              `json:"op"`
	Manifest        json.RawMessage     `json:"manifest,omitempty"`
	OpenBlockDevice *blockDeviceRequest `json:"openBlockDevice,omitempty"`
}

type blockDeviceRequest struct {
	Path     string `json:"path"`
	ReadOnly bool   `json:"readOnly"`
}
```

For `op=open_block_device`, the helper validates the request, opens the device,
and replies on the same Unix socket with a short status payload plus one file
descriptor in out-of-band data:

```go
rights := unix.UnixRights(int(f.Fd()))
_, _, err := unixConn.WriteMsgUnix([]byte("ok\n"), rights, nil)
```

The unprivileged cove process reads the descriptor with `ReadMsgUnix`,
parses it with `unix.ParseSocketControlMessage` and `unix.ParseUnixRights`,
then wraps it in `NSFileHandle`.

## Helper Checks

The helper must reject unsafe or surprising requests before opening anything:

- Peer UID must match the installed helper owner, as `helper.go` already does.
- Path must be absolute and under `/dev/`.
- Path base must start with `rdisk` for writable requests. Read-only requests
  may also allow `/dev/diskN` if testing shows Virtualization accepts it.
- `os.Stat` must report a device node.
- Read-only requests open `O_RDONLY`; writable requests open `O_RDWR`.
- Writable requests must reject mounted devices. Use `diskutil info -plist`
  and fail if `Mounted` is true, or if the plist cannot be parsed.
- Writable requests should reject synthesized APFS container members unless a
  future force flag is added. Whole removable media is the initial target.

Errors should be lowercase and actionable:

```text
block device /dev/rdisk8 is mounted
block device /tmp/disk.img is not under /dev
block devices require an up-to-date cove-helper; run: sudo cove helper install
```

## CLI

Add a repeatable `-block` flag to `cove run` first:

```bash
cove run -linux -block /dev/rdisk8:ro
cove run -linux -block /dev/rdisk8:rw
cove run -linux -block /dev/rdisk8:rw:sync=full
cove run -linux -block /dev/rdisk8:rw:sync=none
```

Default sync policy:

- `:ro` -> `VZDiskSynchronizationModeFull` (sync is irrelevant but conservative)
- `:rw` -> `VZDiskSynchronizationModeFull`
- `:rw:sync=none` -> explicit opt-in only

Do not add this to `install` or `up` initially. Raw block device install targets
need separate confirmation and destructive-write UX.

## Implementation Plan

1. Add `blockDeviceSpec` and a `flag.Value` parser next to the existing disk
   and USB flag parsing.
2. Add `openBlockDeviceViaHelper(spec)` in a new `block_device.go`.
3. Extend `helperRequest` and `handleHelperConn` with `open_block_device`.
4. Implement helper validation in small helpers:
   `validateBlockDevicePath`, `validateBlockDeviceNode`, and
   `validateBlockDeviceUnmounted`.
5. Convert the received descriptor with
   `foundation.NewFileHandleWithFileDescriptorCloseOnDealloc(fd, true)`.
6. Construct the VZ attachment and retain it using the same lifetime pattern as
   other runtime attachments.
7. Append the resulting `VZVirtioBlockDeviceConfiguration` to the VM storage
   devices after the root disk.
8. If `helperBinaryFreshness` reports a stale helper, fail before starting the
   VM.

## Tests

Unit tests:

- `-block` parser: path, read-only flag, sync override, malformed specs.
- Helper validation rejects non-`/dev` paths, regular files, relative paths, and
  writable mounted devices.
- Sync mapping table: `ro`, `rw`, `rw:sync=full`, `rw:sync=none`.

Darwin integration test, guarded behind an explicit environment variable:

1. Create a temporary raw image.
2. Attach it with `hdiutil attach -nomount`.
3. Convert `/dev/diskN` to `/dev/rdiskN`.
4. Ask a test helper instance to open it read-only and read-write.
5. Verify the client receives a usable fd over `SCM_RIGHTS`.
6. Detach with `hdiutil detach`.

Do not require a physical USB drive in CI.

## Open Questions

- Whether Virtualization accepts `/dev/diskN` as well as `/dev/rdiskN`; prefer
  `/dev/rdiskN` unless testing proves otherwise.
- Whether APFS synthesized container devices can be safely identified without
  shelling out to `diskutil`.
- Whether a future destructive install path should support `cove install -linux
  -target /dev/rdiskN` or a separate `cove disk write` command.

