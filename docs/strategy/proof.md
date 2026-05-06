# Competitive proof

Status: initial reproducible benchmark suite, 2026-05-06.

This page is the citable table for cove performance and comparison claims.
Every row names the checked-in result file and the commit that captured it.
Missing competitor numbers are recorded as `not measured`; they are not
estimated.

## Result table

| Workload | Cove | Lume | UTM | Cirrus | Methodology |
|---|---:|---:|---:|---:|---|
| Parallel fork fan-out from stopped 60 GiB macOS VM | 1 fork: 207ms; 2 forks: 354ms; 4 forks: 276ms; 8 forks: 403ms; 16 forks: 784ms. Source: [`bench/parallel-fork/results-20260506-m4x-129/summary.md`](../../bench/parallel-fork/results-20260506-m4x-129/summary.md), raw rows in [`runs.jsonl`](../../bench/parallel-fork/results-20260506-m4x-129/runs.jsonl), commit `72b3eb0`. | not measured | not measured | not measured | [`bench/parallel-fork/run.sh`](../../bench/parallel-fork/run.sh), commit `3b09ad6`; cleanup fix in commit `4cf421f`. |
| Boot-to-agent from local image `test:latest` | 13,082ms to `ctl ready --require agent-ping`. Source: [`bench/boot-to-agent/results-20260506-m4x-129/summary.md`](../../bench/boot-to-agent/results-20260506-m4x-129/summary.md), raw row in [`runs.jsonl`](../../bench/boot-to-agent/results-20260506-m4x-129/runs.jsonl), commit `72b3eb0`. | not measured | not measured | not measured | [`bench/boot-to-agent/run.sh`](../../bench/boot-to-agent/run.sh), commit `5f9ae58`; cleanup fix in commit `4cf421f`. |
| Image snapshot build from stopped VM `cove-test` | 33,231ms for `cove image build -from cove-test -tag bench-cove-test:20260506-r41`. Source: [`bench/image-build/results-20260506-m4x-129/summary.md`](../../bench/image-build/results-20260506-m4x-129/summary.md), inspect JSON in [`image-inspect-1.json`](../../bench/image-build/results-20260506-m4x-129/image-inspect-1.json), commit `72b3eb0`. | not measured | not measured | not measured | [`bench/image-build/run.sh`](../../bench/image-build/run.sh), commit `c27154c`; inspect flag fix in commit `72b3eb0`. |
| Same host Lume comparison | not measured | `lume` CLI not found on PATH. Source: [`bench/cove-vs-lume/results-20260506-m4x-129/runs.jsonl`](../../bench/cove-vs-lume/results-20260506-m4x-129/runs.jsonl), commit `72b3eb0`. | not measured | not measured | [`bench/cove-vs-lume/run.sh`](../../bench/cove-vs-lume/run.sh), commit `fc5385c`. |
| Same host UTM comparison | not measured | not measured | `utmctl` CLI not found on PATH. Source: [`bench/cove-vs-utm/results-20260506-m4x-129/runs.jsonl`](../../bench/cove-vs-utm/results-20260506-m4x-129/runs.jsonl), commit `72b3eb0`. | not measured | [`bench/cove-vs-utm/run.sh`](../../bench/cove-vs-utm/run.sh), commit `fc5385c`. |
| Same host Cirrus comparison | not measured | not measured | not measured | `cirrus` CLI not found on PATH. Source: [`bench/cove-vs-cirrus/results-20260506-m4x-129/runs.jsonl`](../../bench/cove-vs-cirrus/results-20260506-m4x-129/runs.jsonl), commit `72b3eb0`. | [`bench/cove-vs-cirrus/run.sh`](../../bench/cove-vs-cirrus/run.sh), commit `fc5385c`; Cirrus shutdown context is linked from the migration page. |

## Host

The 2026-05-06 result set was captured on host `m4x-129`. Each benchmark result
directory includes a `host.json` file with OS, kernel, CPU, memory, disk-free,
cove version, `HEAD`, and `origin/main` at run time. Example:
[`bench/parallel-fork/results-20260506-m4x-129/host.json`](../../bench/parallel-fork/results-20260506-m4x-129/host.json).

## Rules for using this page

- A performance number can be quoted only with the result file and commit hash
  in the same sentence or table cell.
- A missing competitor number must remain `not measured` until the corresponding
  runner writes an `ok` row for that tool.
- Protocol files are not evidence by themselves. The checked-in `runs.jsonl`
  files are the raw evidence.
- The Cirrus migration page may use these rows for operational comparison, but
  it must not imply a hosted Cirrus result that was not measured.
