# Competitive Benchmark Methodology

This page defines the Phase 2 method for reproducible competitive benchmarks.
It is a method, not a results page. Do not publish numbers from this file alone.

## Evidence Rules

Every competitive result must be backed by an artifact produced during the same
benchmark run or by a cited external source. A valid cove run records:

- host model, CPU, memory, macOS version, and available disk space;
- cove version, commit, build flags, and entitlement signing status;
- guest image name, guest OS version, CPU count, memory, disk size, and network
  mode;
- command line, start time, end time, exit status, and captured stdout/stderr;
- per-run artifact directory from `cove run`, including logs and metrics.

If a measurement cannot be reproduced from the artifact directory and the JSON
summary, leave it out. Do not average across runs with missing logs. Do not mix
numbers from different hosts, guest images, or tool versions in one comparison
table.

## Workloads

The competitive suite should cover workloads that map to cove's operator-facing
use cases:

- fresh VM start to ready agent;
- forked task VM start to ready agent;
- guest command execution with stdout/stderr capture;
- artifact-producing `cove run` command;
- image pull or local image restore when the compared tool supports it;
- teardown latency and host disk cleanup.

Run each workload at least three times on a quiet host. Prefer five runs for any
published claim. Report median, minimum, maximum, and failure count. Treat a
tool that fails a workload as a failed cell, not as a slow successful run.

## cove Run Artifacts

Competitive benchmarks should integrate with `cove run` artifacts instead of
copying values by hand. The benchmark harness writes one machine-readable
summary and one generated Markdown report:

```sh
cove bench competitive \
  --out docs/benchmarks/results-2026-05-cove.json \
  --markdown docs/benchmarks/competitive-2026-05.md
```

The JSON file is the source of truth for tables. The Markdown file is generated
from the JSON plus the artifact manifest. If the Markdown and JSON disagree,
fix the generator or rerun the benchmark; do not patch the Markdown manually.

Each result row should link or name the backing artifact path. Private host
paths are acceptable in internal reports, but redact secrets, tokens, account
names, and customer identifiers before sharing outside the repo.

## Competitor Honesty

Only include competitors that were actually run under this methodology or have
a clearly cited source for the exact measurement.

- Include Lume only when the benchmark run contains Lume artifacts from the
  same host and workload.
- Include Docker Desktop for Mac only when the benchmark run contains Docker
  artifacts from the same host and workload.
- Do not include Cirrus benchmark numbers unless sourced from a cited Cirrus
  artifact or public source. If Cirrus numbers are unavailable, say unavailable.

Do not use product names to imply compatibility, certification, endorsement, or
trademark status. Describe the command that was run and the measured behavior.

## Reporting

The generated report should separate facts from interpretation:

- facts: exact commands, versions, artifact paths, exit status, and measured
  times;
- interpretation: where cove is faster, slower, missing a feature, or blocked
  by unavailable competitor data;
- exclusions: workloads skipped, tools not run, missing artifacts, and any
  source limitations.

Use plain language. Prefer "not run" or "unavailable" over speculative
comparisons. A benchmark is ready to cite only when another operator can rerun
the command, inspect the artifacts, and reach the same result table.
