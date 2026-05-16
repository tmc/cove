---
title: Snapshots
---
# Snapshots

Two kinds of snapshots: VM state snapshots and APFS disk snapshots.

## VM State Snapshots

Save and restore full VM state (CPU, memory, devices). The VM must be running.

```bash
cove snapshot save checkpoint1
cove snapshot list
cove snapshot restore checkpoint1
cove snapshot delete checkpoint1
```

Snapshots are stored in `~/.vz/vms/<name>/snapshots/<name>.vmstate`.

The save command automatically pauses the VM, saves state, and resumes.

### Via Control Socket

```bash
cove ctl -vm default snapshot save checkpoint1
cove ctl -vm default snapshot list
```

Use `cove ctl` for normal automation so token handling stays inside cove. If
you need the raw control-socket protocol, see
[`docs/reference/control-api.md`](../reference/control-api.md).

## Disk Snapshots (APFS COW)

Snapshot the disk image using APFS clonefile (copy-on-write).

> [!TIP]
> Disk snapshots use APFS copy-on-write -- they're nearly free until you diverge from the snapshot.

```bash
cove disk-snapshot save before-update
cove disk-snapshot run before-update
cove disk-snapshot list
cove disk-snapshot restore before-update
cove disk-snapshot delete before-update
```

Disk snapshots work whether the VM is running or stopped. They snapshot the actual disk contents, not the VM state.

For throwaway rollback runs, boot a disposable clone directly from the saved
snapshot:

```bash
cove disk-snapshot run before-update
```

This leaves the base VM untouched and deletes the temporary clone when the run
ends.

## When to Use Which

| | VM State Snapshot | Disk Snapshot |
|---|---|---|
| Captures | CPU + memory + disk | Disk only |
| VM must be | Running | Running or stopped |
| Restore speed | Instant | Instant (APFS clone) |
| Use case | Quick checkpoint mid-session | Before risky disk changes |
