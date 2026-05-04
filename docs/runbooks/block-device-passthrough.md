# Block Device Passthrough

This runbook is for the real-device smoke test for design 028. It requires a
USB drive or other disposable block device. Do not run it against a disk with
data you need.

## Prepare

Build and install a fresh helper from the same cove binary you will run:

```
go build -o cove .
codesign -s - -f --entitlements internal/autosign/vz.entitlements ./cove
sudo ./cove helper install
```

Find the target disk:

```
diskutil list
diskutil info -plist /dev/diskN
```

Use the raw device path for writable runs:

```
/dev/rdiskN
```

For writable tests, unmount the whole disk first:

```
diskutil unmountDisk /dev/diskN
```

## Read-only Smoke

Boot an existing Linux VM with the device attached read-only:

```
./cove run -linux -vm <vm-name> -block /dev/rdiskN:ro
```

Inside the guest, verify that the extra virtio block device appears:

```
lsblk
sudo fdisk -l /dev/vdb
```

Expected result: the VM starts, the helper opens the raw device, and the guest
sees an extra block device after the root disk.

## Writable Smoke

Writable passthrough is destructive. Use only disposable media.

```
diskutil unmountDisk /dev/diskN
./cove run -linux -vm <vm-name> -block /dev/rdiskN:rw
```

Inside the guest:

```
lsblk
sudo wipefs -n /dev/vdb
```

For explicit no-sync benchmarking:

```
./cove run -linux -vm <vm-name> -block /dev/rdiskN:rw:sync=none
```

## Expected Rejections

These should fail before the VM starts:

```
./cove run -linux -block /tmp/disk.img:rw
./cove run -linux -block rdisk8:rw
./cove run -linux -block /dev/diskN:rw
./cove run -linux -block /dev/rdiskN:rw   # while mounted
```

The actionable helper-refresh error is:

```
block devices require an up-to-date cove-helper; run: sudo cove helper install
```

## TODO

Add a Darwin integration test guarded by an explicit environment variable. The
test should create a temporary raw image, attach it with `hdiutil attach
-nomount`, convert `/dev/diskN` to `/dev/rdiskN`, ask a test helper instance to
open it read-only and read-write, verify that the client receives an fd via
`SCM_RIGHTS`, and detach it in cleanup.
