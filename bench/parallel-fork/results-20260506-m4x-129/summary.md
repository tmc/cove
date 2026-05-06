# parallel-fork benchmark

- Date: 2026-05-06T02:36:46Z
- Cove commit: `3db9f0f7c6813f84dd384f9bd3ff19c9e26dbf25`
- Host metadata: `bench/parallel-fork/results-20260506-m4x-129/host.json`
- Raw results: `bench/parallel-fork/results-20260506-m4x-129/runs.jsonl`

| Level | Status | Total wall | Notes |
|---:|---|---:|---|
| 1 | ok | 287ms | parent `hermes-mlx-go-60g-v10` |
| 2 | ok | 382ms | parent `hermes-mlx-go-60g-v10` |
| 4 | ok | 454ms | parent `hermes-mlx-go-60g-v10` |
| 8 | ok | 794ms | parent `hermes-mlx-go-60g-v10` |
| 16 | ok | 1163ms | parent `hermes-mlx-go-60g-v10` |

## child fork duration summary

| n | mean | median | p95 | p99 |
|---:|---:|---:|---:|---:|
| 31 | 790ms | 961ms | 1095ms | 1104ms |
