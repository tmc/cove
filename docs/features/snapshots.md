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
TOKEN=$(cat ~/.vz/vms/default/control.token)
echo '{"type":"snapshot","auth_token":"'$TOKEN'","snapshot":{"action":"save","name":"checkpoint1"}}' | \
  nc -U ~/.vz/vms/default/control.sock
```

## Disk Snapshots (APFS COW)

Snapshot the disk image using APFS clonefile (copy-on-write).

> [!TIP]
> Disk snapshots use APFS copy-on-write -- they're nearly free until you diverge from the snapshot.

```bash
cove disk-snapshot save before-update
cove disk-snapshot list
cove disk-snapshot restore before-update
cove disk-snapshot delete before-update
```

Disk snapshots work whether the VM is running or stopped. They snapshot the actual disk contents, not the VM state.

## When to Use Which

| | VM State Snapshot | Disk Snapshot |
|---|---|---|
| Captures | CPU + memory + disk | Disk only |
| VM must be | Running | Running or stopped |
| Restore speed | Instant | Instant (APFS clone) |
| Use case | Quick checkpoint mid-session | Before risky disk changes |
