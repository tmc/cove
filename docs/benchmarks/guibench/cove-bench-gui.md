# `cove bench gui` command spec

The full subcommand surface for the macOS GUI-agent benchmark (design
[047](../../designs/047-gui-agent-benchmark-harness.md)). This document
specifies every subcommand; a status column marks what is wired into the CLI
today versus what is specified and backed by library code but not yet exposed as
a command. Where this doc and the binary disagree, the binary
(`cove bench gui -h`) is authoritative.

`cove bench gui` is dispatched from `bench_cli.go` (`handleBenchCommand` →
`runBenchGUI` in `bench_gui_cli.go`). VM-free subcommands run anywhere with
plain `go test`/`go run`; subcommands that fork a VM need Apple-Silicon hardware
and the appropriate TCC grants on the base image.

## Status legend

- **wired** — a `case` in `runBenchGUI`, runnable today.
- **library-ready** — the scoring logic exists in `internal/guibench`
  (`Aggregate`, `RenderMarkdown`, `BuildManifest`, `VerifyBundle`, `MaxTier`,
  `StepBudget`, the `Backend` interface), but the CLI subcommand is not yet
  wired. Documented here as the stable surface so a corpus author can rely on
  the shape.

## Subcommands

| Subcommand | Status | Touches a VM | Purpose |
|---|---|---|---|
| `validate` | wired | no | Load and validate a corpus directory. |
| `metrics` | wired | no | List the registered verifier metrics. |
| `manifest` | wired | no | Print the versioned corpus manifest (corpus + verifier version, public/held-out partition). |
| `verify-bundle` | wired | no | Validate a result bundle against a corpus and stamp its tier (self-reported vs. maintainer-verified). |
| `run` | library-ready (CLI stub) | **yes** | Fork per task, drive the provider, getter→metric→score; emit `score.json`. |
| `report` | library-ready | no | Render a `score.json` as the citable Markdown matrix. |
| `selfcheck` | library-ready | **yes** | `--no-agent`: run each task's setup + a known-good scripted solution and assert the verifier passes; assert a no-op scores 0. |
| `examine` | library-ready | **yes** | Manual GUI-action-to-disk-state inspection for one task (OSWorld's `run_manual_examine` shape). |
| `image-check` | library-ready | **yes** | Verify a base image carries the grant tier a corpus needs (`MaxTier`) via the `cove doctor` TCC probe before scoring. |

### `validate` (wired)

```
cove bench gui validate -corpus <dir>
```

Loads every `*.json` in `<dir>`, validates each (unknown metric, missing
conjunction, bad getter spec, empty id), and prints the count and task ids. Fails
loudly on the first bad task. No VM. This is the CI gate for a corpus.

### `metrics` (wired)

```
cove bench gui metrics
```

Prints the registered metric names (`guibench.Metrics()`), one per line, sorted.
Use it to confirm the running binary's metric set against §8 of the task-schema
spec. No VM.

### `manifest` (wired)

```
cove bench gui manifest -corpus <dir>
```

Prints the corpus manifest (`guibench.BuildManifest`): schema version, corpus
version, verifier version, cove commit, task count, and the public/held-out
partition. A citable result references a manifest so a reader knows which corpus
and verifier produced a number. No VM.

### `verify-bundle` (wired)

```
cove bench gui verify-bundle -corpus <dir> [-maintainer] <bundle>
```

Validates a result bundle against the corpus and stamps its tier. Without
`-maintainer` the bundle is stamped self-reported; with `-maintainer` it is
stamped maintainer-verified — the XLANG discipline where only maintainer-executed
runs are citable as verified headline numbers (design 047 §11). No VM (it scores
an already-produced bundle, it does not run the agent).

### `run` (library-ready; CLI prints a slice-2 stub)

Specified surface:

```
cove bench gui run -corpus <dir> -provider <p> [-task-id <id>] [-runs N] \
                   [-subset test_small] [-checkpoint-dir <dir>] [-out score.json]
```

For each selected task: fork a fresh ephemeral RAM-overlay VM from the task's
`image`, run the task's `config` setup, drive the provider's computer-use loop
under the complexity-scaled step budget (`StepBudget`), run the postconfig flush,
read the getter, score the metric, and discard the fork. A crashing task scores 0
(`status:"error"`) and the suite continues. Emits a `score.json`
(`guibench.ScoreReport`) alongside the per-run replay bundle. `--checkpoint-dir`
makes a long run resumable. Requires Apple-Silicon hardware, a provisioned base
image, and provider API keys.

### `report` (library-ready)

```
cove bench gui report <score.json>
```

Renders a `score.json` as the citable Markdown matrix (`guibench.RenderMarkdown`):
the per-provider × per-task cells, per-domain and overall success rates, the
human baseline column, and the flagged-cell count. No VM.

### `selfcheck` (library-ready)

```
cove bench gui selfcheck -corpus <dir> --no-agent
```

The "is the validator correct" discipline (AndroidWorld): run each task's setup
plus a known-good scripted solution and assert the verifier passes, and assert a
no-op scores 0. It is the negative/positive control that keeps the verifier
library trustworthy. Forks a VM but runs no provider (no API keys needed).

### `examine` (library-ready)

```
cove bench gui examine -corpus <dir> -task-id <id>
```

Forks one task, runs its setup, and lets a human inspect the GUI-action-to-disk-
state lag (the §7 flush trap) before trusting the verifier on it. Forks a VM.

### `image-check` (library-ready)

```
cove bench gui image-check -corpus <dir> -image <ref>
```

Computes the corpus's required grant tier (`guibench.MaxTier`) and verifies the
named base image carries it (`cove doctor` TCC/FDA probe), refusing to score on
an under-granted image rather than letting Tier-B/C getters silently read denied
state. Forks/inspects a VM.

## Exit conventions

`cove bench gui` follows the repo's flag-help convention (`flag_help.go`): `-h`,
`-help`, `--help`, and a bare `gui` print usage and exit 0; an unknown
subcommand prints usage and exits nonzero; a flag parse error exits nonzero.

## Gates by hardware requirement

- **No VM** (`validate`, `metrics`, `manifest`, `verify-bundle`, `report`):
  runnable in CI; covered by `go test ./internal/guibench/...`.
- **Needs hardware + grants** (`run`, `selfcheck`, `examine`, `image-check`):
  Apple-Silicon host, a provisioned base image, and (for `run`) provider API
  keys. These are operator gates; their numbers are never inferred.
