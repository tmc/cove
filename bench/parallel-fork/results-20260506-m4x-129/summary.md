# parallel-fork benchmark

- Date: 2026-05-06T02:22:28Z
- Cove commit: `fc5385ce8891a39a05c8c88d5bd2b4707fa7e4e5`
- Host metadata: `bench/parallel-fork/results-20260506-m4x-129/host.json`
- Raw results: `bench/parallel-fork/results-20260506-m4x-129/runs.jsonl`

| Level | Status | Total wall | Notes |
|---:|---|---:|---|
| 1 | ok | 207ms | parent `hermes-mlx-go-60g-v10` |
| 2 | ok | 354ms | parent `hermes-mlx-go-60g-v10` |
| 4 | ok | 276ms | parent `hermes-mlx-go-60g-v10` |
| 8 | ok | 403ms | parent `hermes-mlx-go-60g-v10` |
| 16 | ok | 784ms | parent `hermes-mlx-go-60g-v10` |

## child fork duration summary

| n | mean | median | p95 | p99 |
|---:|---:|---:|---:|---:|
| 31 | 496ms | 625ms | 736ms | 740ms |
