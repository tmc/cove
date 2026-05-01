# cove fork benchmark

- Date: 2026-04-30T14:59:43-07:00
- Host: darwin/arm64, macOS 26.4.1, Apple M4 Max
- Cove: `cove dev (commit 2285aacfffff, built 2026-04-30T21:58:52Z)`
- Runs per parent: 1
- Boot to agent: true
- Note: v0.3 slice 6 boot-to-agent smoke on local cove-test parent

| Parent | Child | Disk logical | Parent stat blocks | Child stat blocks | Inodes differ | Fork wall | Agent reachable | Result |
|---|---|---:|---:|---:|---|---:|---:|---|
| `cove-test` | `fork-bench-cove-test-1777586372-1` | 64 GiB | 20.9 GiB | 20.9 GiB | true | 132ms | 10.788s | ok |
