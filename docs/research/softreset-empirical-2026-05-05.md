# Soft-reset Orchestrator Empirical Run

Date: 2026-05-05

Command:

```
cove softreset run-all ubuntu-gh-runner-headed --json --timeout=60s
```

VM:

- `ubuntu-gh-runner-headed`
- Linux
- stopped before the run

Result:

- Isolation score: 75
- Pass: filesystem attributes, memory markers
- Limit: process table, network sockets
- Fail: none

The process and network probes reached `limit` because no cove-spawned marker
process or socket was observable at arm time. That means this run validates
the orchestrator, ordering, JSON report, and local destructive probe
correlation, but it does not yet prove guest reset isolation for process or
network state.

Raw report:

```json
{
  "vm": "ubuntu-gh-runner-headed",
  "probes": [
    {
      "name": "filesystem-attributes",
      "status": "pass",
      "runtime_s": 0.005,
      "evidence": [
        "sentinel=attribute-residue",
        "mode=0600",
        "mtime=2000-01-01T00:00:00Z",
        "xattr=set",
        "sentinel=absent-after-reset"
      ]
    },
    {
      "name": "process-table",
      "status": "limit",
      "runtime_s": 0.618,
      "evidence": [
        "marker=cove-softreset-probe",
        "before-total=3062",
        "before-cove=0",
        "before-system=3062",
        "cove-spawned=not-observed"
      ]
    },
    {
      "name": "network",
      "status": "limit",
      "runtime_s": 0.233,
      "evidence": [
        "marker=cove-softreset-probe",
        "before-total=207",
        "before-cove=0",
        "before-system=207",
        "cove-sockets=not-observed"
      ]
    },
    {
      "name": "memory",
      "status": "pass",
      "runtime_s": 0.001,
      "evidence": [
        "/tmp=armed",
        "/var/tmp=armed",
        "/dev/shm=limit:mkdir /dev/shm: operation not permitted",
        "/tmp/cove-softreset-memory-marker=absent-after-reset",
        "/var/tmp/cove-softreset-memory-marker=absent-after-reset",
        "markers=absent-after-reset"
      ]
    }
  ],
  "runtime_s": 0.856,
  "isolation_score": 75
}
```

Design meaning:

- The Phase D orchestrator is usable and produces the required correlation
  report.
- The empirical isolation claim remains conservative: score below 100 means
  this run cannot be used to claim full soft-reset isolation.
- A future guest-active run should arm process and network markers inside the
  VM before reset and should fail the claim if either marker survives across a
  fork-from or reset boundary.
