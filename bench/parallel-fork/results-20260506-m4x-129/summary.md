# parallel-fork benchmark

- Date: 2026-05-06T02:33:08Z
- Cove commit: `8cd1903417795e0ef972ea96c8a1f86b24d4144c`
- Host metadata: `bench/parallel-fork/results-20260506-m4x-129/host.json`
- Raw results: `bench/parallel-fork/results-20260506-m4x-129/runs.jsonl`

| Level | Status | Total wall | Notes |
|---:|---|---:|---|
| 1 | ok | 188ms | parent `hermes-mlx-go-60g-v10` |
| 2 | ok | 198ms | parent `hermes-mlx-go-60g-v10` |
| 4 | ok | 206ms | parent `hermes-mlx-go-60g-v10` |
| 8 | ok | 334ms | parent `hermes-mlx-go-60g-v10` |
| 16 | ok | 885ms | parent `hermes-mlx-go-60g-v10` |

## child fork duration summary

| n | mean | median | p95 | p99 |
|---:|---:|---:|---:|---:|
| 31 | 537ms | 805ms | 834ms | 834ms |
