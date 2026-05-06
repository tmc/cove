# parallel-fork benchmark

Measures how long cove takes to materialize multiple ephemeral forks from the
same image ref. Fan-out levels are 1, 2, 4, 8, and 16 by default.

Run:

```bash
bench/parallel-fork/run.sh --image macos-runner:latest --levels 1,2,4,8,16
```

Use a runner image that is safe to boot repeatedly and tear down. The script
records skipped cells when the image is missing.
