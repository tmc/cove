# Cove benchmarks

This directory contains reproducible benchmark protocols and checked-in result
files for cove competitive claims. A result is citable only when it records:

- host OS and hardware;
- cove commit hash;
- benchmark timestamp;
- command or protocol that produced the measurement;
- result status for every attempted cell.

Missing competitor numbers must be recorded as `not measured` or `skipped`.
Do not infer performance from a protocol file.

## Benchmarks

| Benchmark | Purpose |
|---|---|
| [`fork-time`](fork-time/) | Existing stopped-VM clonefile fork latency measurements. |
| [`parallel-fork`](parallel-fork/) | Ephemeral fork fan-out at 1, 2, 4, 8, and 16 workers. |
| [`boot-to-agent`](boot-to-agent/) | Time from VM start/install path to guest agent readiness. |
| [`image-build`](image-build/) | Cache miss, full hit, and partial hit behavior for `cove build`. |
| [`cove-vs-utm`](cove-vs-utm/) | Protocol for the same boot-run-teardown workload on cove and UTM. |
| [`cove-vs-lume`](cove-vs-lume/) | Protocol for the same boot-run-teardown workload on cove and Lume. |
| [`cove-vs-cirrus`](cove-vs-cirrus/) | Protocol for Cirrus/Tart comparison during the shutdown window. |
| [`soft-reset`](soft-reset/) | Empirical matrix showing soft reset is not an isolation primitive. |

## Result names

New scripts write:

```text
bench/<name>/results-YYYYMMDD-<hostid>.jsonl
bench/<name>/results-YYYYMMDD-<hostid>.md
```

The JSONL file is the machine-readable evidence. The Markdown file is the
human summary linked from strategy docs.
