# Soft Reset Probes

`cove softreset probe` runs the destructive soft-reset residue probe matrix
against a disposable VM label and prints a pass/fail/limit summary.

Use only forked or throwaway VMs. The probes create marker files, inspect host
process and socket tables, and write temporary markers under `/tmp`, `/var/tmp`,
and `/dev/shm` where available.

```bash
cove softreset probe soft-reset-eval-001 --all
cove softreset probe soft-reset-eval-001 --probes filesystem,process,network,memory
```

## Matrix

| Probe | Question | Pass condition | Limit condition |
|---|---|---|---|
| `filesystem` | Are file metadata changes removed by reset? | The sentinel file is absent after reset. | The scratch root is unsafe or cannot be armed. |
| `process` | Are cove-spawned processes gone after reset while system processes remain? | Marker processes disappear and unrelated system processes survive. | The marker process or baseline system process is not observable. |
| `network` | Are listening sockets and ephemeral connections cleared after reset? | Marker sockets disappear and unrelated sockets survive. | The marker socket or baseline system socket is not observable. |
| `memory` | Are `/tmp`, `/var/tmp`, and tmpfs marker writes cleared after reset? | All armed markers are absent after reset. | No marker path can be armed. |

The runner reports `limit` rather than `pass` when a probe cannot observe its
own marker. That keeps the matrix honest on platforms or guest configurations
where a surface is unavailable.
