# cove fork-time benchmark

This harness measures the F1 roadmap claim tracked in `docs/designs/ROADMAP.md`:
how long `cove fork` takes, and optionally how long a forked child takes to
become reachable via `agent-ping`.

The harness is black-box on purpose. It invokes the built `cove` binary instead of importing VM internals, so measurements include the same CLI path users run.

## Build

```bash
go build -o cove .
codesign -s - -f --entitlements internal/autosign/vz.entitlements ./cove
go run ./cmd/fork-bench -h
```

## Fork-only Measurement

Run this against one or more existing stopped parent VMs:

```bash
go run ./cmd/fork-bench \
  -cove ./cove \
  -parents macos-10g,macos-60g,macos-200g \
  -runs 5 \
  -max-fork 250ms \
  -out bench/fork-time/results-$(date +%Y%m%d).md
```

The `-max-fork 250ms` threshold is the regression guard for the v0.1.1 smoke result: a 60 GB VM forked in about 0.14s on the M4 smoke host. Adjust only with a new measured baseline and note the host hardware in the result file.

## Boot-to-Agent Measurement

To measure the roadmap ship-gate predicate, add `-boot`:

```bash
go run ./cmd/fork-bench \
  -cove ./cove \
  -parents macos-30g-base \
  -runs 3 \
  -boot \
  -timeout 3m \
  -out bench/fork-time/results-agent-$(date +%Y%m%d).md
```

`-boot` starts each child with `cove -vm <child> run -headless -no-resume`, polls `cove -vm <child> ctl agent-ping`, then stops and deletes the child when `-cleanup=true`.

## Output

The output is a markdown table with:

- fork wall time,
- parent and child logical disk size,
- parent and child `stat` block counts,
- whether parent and child have distinct inodes,
- optional boot-to-agent time,
- command errors, if any.

APFS reports stat block counts for clonefile children; those counts are not a unique physical-block ownership report. Treat low fork wall time plus distinct inodes as the CoW smoke signal, and use child-write divergence tests for correctness. Publish the result file as-is. If the number is slower than expected, update the product claim instead of hiding the run.
