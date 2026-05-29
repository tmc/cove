# guibench task-schema spec (v1)

Stable specification of the `guibench` task record, metrics, getters, privilege
tiers, parameterization, and run protocol. This document is **versioned**: the
machine-readable counterpart is `guibench.SchemaVersion` (currently `"v1"`),
which a scored result records so an old `score.json` is never silently compared
against a newer verifier (design [047](../../designs/047-gui-agent-benchmark-harness.md)
§7, "corpus and verifier are versioned together").

The spec is the contract a third party authors a macOS corpus against. It
mirrors the implemented Go surface in `internal/guibench`; where this doc and the
code disagree, the code is authoritative and the drift is a bug in this doc.

- Schema version: `v1` (`guibench.SchemaVersion`)
- Verifier version: a digest of the scoring surface, `guibench.VerifierVersion()`
  (format `v1:<12-hex>`). Recorded on every result.
- Corpus version: a digest of task identities + scoring shape over a loaded
  corpus, `guibench.CorpusVersion(tasks)`. Two runs over the same corpus version
  are directly comparable.

## 1. The task record

A task is one JSON file. A corpus is a directory of `*.json` files; the loader
(`guibench.Load`) reads every `*.json`, validates each, and returns them sorted
by `id`. Decoding rejects unknown fields (`DisallowUnknownFields`), so a typo in
a field name fails loudly rather than being ignored.

```json
{
  "id": "finder-create-folder",
  "image": "macos-base:v1",
  "domain": "Finder",
  "instruction": "Using Finder, create a new folder named {FOLDER} on the Desktop.",
  "source": "provenance URL or note",
  "complexity": 2,
  "infeasible": false,
  "subset": ["test_small"],
  "network_allow": [],
  "config": [
    {"args": ["sh", "-c", "rm -rf \"$HOME/Desktop/{FOLDER}\""], "env": {}, "work_dir": ""}
  ],
  "schema": [
    {"name": "FOLDER", "pool": ["Project", "Invoices", "Photos", "Drafts"]}
  ],
  "evaluator": {
    "func": "file_exists",
    "conj": "",
    "result": {"kind": "exec", "args": ["sh", "-c", "test -d \"$HOME/Desktop/{FOLDER}\" && echo true || echo false"]},
    "expected": null,
    "options": {}
  }
}
```

### Top-level fields

| Field | JSON | Type | Required | Meaning |
|---|---|---|---|---|
| ID | `id` | string | yes | Unique task id. Used to sort the corpus and to record participation in a result. |
| Image | `image` | string | yes (to run) | The substrate base to fork from. For the reference VZ-fork backend this is a `cove image` ref. |
| Domain | `domain` | string | no | App/area for per-domain aggregation (Finder, Safari, Settings, Notes, Preview, Terminal). Empty aggregates into the `(none)` bucket. |
| Instruction | `instruction` | string | yes | Natural-language goal given to the agent. May contain `{PARAM}` placeholders. |
| Source | `source` | string | no | Provenance URL/note. |
| Complexity | `complexity` | int | no | Maps to the agent step budget via `guibench.StepBudget` (§5). 0 or negative falls back to the floor. |
| Config | `config` | `[]SetupStep` | no | Ordered setup steps run after fork, before the agent acts. |
| Evaluator | `evaluator` | `Evaluator` | yes | How the end-state is read and scored (§2). |
| Infeasible | `infeasible` | bool | no | If true, success = the agent's terminal answer is `FAIL` (the `infeasible` metric). |
| Subset | `subset` | `[]string` | no | Named subsets this task joins (`test_small` for CI, `held_out` for maintainer-only). See §6. |
| Schema | `schema` | `[]Param` | no | Typed parameter pool for anti-memorization (§4). |
| NetworkAllow | `network_allow` | `[]string` | no | Per-task egress allowlist. Empty = fully offline during scoring (§7). |

### SetupStep

A setup step is a guest command run after fork, before the agent acts. `args`
entries may contain `{PARAM}` placeholders.

| Field | JSON | Type | Meaning |
|---|---|---|---|
| Args | `args` | `[]string` | argv to run in the guest. |
| Env | `env` | `map[string]string` | environment for the command. |
| WorkDir | `work_dir` | string | working directory. |

### Validation (no VM)

`Task.Validate` rejects: an empty `id`; an empty evaluator `func`; an unknown
metric name; a list `func` with no `conj`; an invalid `conj`; an invalid getter
spec (unknown kind, missing required field); a schema param with an empty name.
Validation never touches a VM, so a corpus is checked in CI with
`cove bench gui validate -corpus <dir>`.

## 2. Evaluator

The evaluator is the getter/metric split (OSWorld's design): a **getter** pulls
one value off the live guest; a **metric** is a pure function scoring it.

| Field | JSON | Type | Meaning |
|---|---|---|---|
| Func | `func` | string or `[]string` | One metric name, or a list. |
| Conj | `conj` | `"and"` \| `"or"` | Required only when `func` is a list. `and` ⇒ mean of sub-scores; `or` ⇒ max. |
| Result | `result` | `GetterSpec` | Reads the live end-state value (§3). |
| Expected | `expected` | `GetterSpec` (nullable) | Optional reference value, also read off the guest. |
| Options | `options` | `map[string]any` | Metric knobs. An `"expected": "<literal>"` option supplies a gold value computed from params and overrides a getter-read expected. |

A per-task score is a float in `[0,1]`: binary for most tasks, fractional only
when an `and` conjunction averages sub-metrics.

## 3. Getters (by privilege tier)

A getter is declared by `kind`; the remaining `GetterSpec` fields are
kind-specific and may contain `{PARAM}` placeholders. Every getter is classified
by the TCC grant it needs on a fresh fork. **The base image carries exactly the
grants its corpus needs, verified by `cove doctor` before save** (design 047 §5,
§12). `guibench.MaxTier(tasks)` reports the grant level a corpus requires; the
`cove bench gui image-check` flow refuses to run a corpus on an under-granted
image rather than letting a getter silently read denied state.

| Tier | Grant (`Tier.Grant()`) | Getter kinds |
|---|---|---|
| **A** | none | `exec`, `file`, `defaults`, `screen_ocr` |
| **B** | Full Disk Access | `sqlite`, `protected_file`, `tccdb` |
| **C** | Apple Events + Accessibility | `applescript`, `accessibility` |

### GetterSpec fields

| Field | JSON | Used by | Meaning |
|---|---|---|---|
| Kind | `kind` | all | Getter kind (table above). |
| Args | `args` | `exec` | Command argv (required for `exec`). |
| Env | `env` | `exec` | Environment. |
| WorkDir | `work_dir` | `exec` | Working directory. |
| Field | `field` | `exec` | `"stdout"` (default) or `"exit"` (the exit code as a string). |
| Path | `path` | `file`, `protected_file`, `sqlite`, `tccdb` | Guest path / db path. |
| Domain | `domain` | `defaults` | Preference domain (required). |
| Key | `key` | `defaults` | Preference key (required). |
| Query | `query` | `sqlite`, `tccdb` | SQL returning a single scalar (required). |
| Script | `script` | `applescript` | AppleScript/JXA source (required). |
| JXA | `jxa` | `applescript` | Run the script as JavaScript for Automation. |
| App | `app` | `accessibility` | Target application name (required). |
| Element | `element` | `accessibility` | AX element selector. |
| Attr | `attr` | `accessibility` | AX attribute to read (required). |

### Pre-read flush discipline (the most likely false-negative source)

macOS preferences go through `cfprefsd` and app SQLite stores use a
write-ahead log, so a getter that reads a backing file too early sees stale data
(design 047 §7). The getters handle this:

- `defaults` reads through `defaults read <domain> <key>`, which returns the
  live `cfprefsd` value rather than a stale on-disk plist.
- `sqlite` checkpoints the WAL (`PRAGMA wal_checkpoint(FULL)`) before querying.
- For tasks that read state a different way, `guibench.Flush(probe, kind, path)`
  runs an explicit flush (`cfprefsd` kill, or WAL checkpoint).

## 4. Parameterization (anti-memorization)

A static corpus is memorizable, so each task is a template (design 047 §10). The
`schema` declares typed parameters; `Task.Params(seed)` materializes one
deterministic, self-consistent variation; `guibench.Materialize` substitutes
`{NAME}` placeholders in the instruction, setup args, and getter spec.

| Param field | JSON | Meaning |
|---|---|---|
| Name | `name` | Placeholder name, used as `{Name}`. Required. |
| Pool | `pool` | Candidate values; an independent param picks one by seed. |
| ExpectedFrom | `expected_from` | Names another param whose chosen value derives this one. |
| Derive | `derive` | Lookup table: the source param's value → this param's value. |

The verifier's gold value is computed from the **same** params (typically via the
`options.expected` literal), so it can never go stale relative to the
instruction. The same seed always yields the same values (`math/rand/v2` over a
seeded source mixed with the task id — never the global rand or `crypto/rand`).

## 5. Run protocol

Following the OpenAI CUA convention so numbers are comparable in spirit:

- **pass@1**, scored over **≥ 3 runs** per (provider, task); a cell whose score
  spread (`max−min`) exceeds **0.20** (`guibench.VarianceFlagThreshold`) is
  flagged for manual inspection, not re-rolled.
- **Temperature 0.6** (the CUA convention).
- **Complexity-scaled step budget**, not a fixed 200: `StepBudget(complexity) =
  15 + complexity*15`, with a floor of 15 (macOS tasks are mostly short).
- **One fresh RAM-overlay fork per task, always.** Never reused, never soft
  reset (design 015 closed soft reset).
- **Crash = score 0**, suite continues (AndroidWorld's try/except discipline). A
  crashed run is an `Outcome` with `status: "error"`, counted as 0 — never a
  dropped row.

### Scoring outputs

A scored matrix produces a `score.json` (`guibench.ScoreReport`): the citable
metadata (`generated_at`, `cove_commit`, `host_hardware`, `corpus_version`,
`verifier_hash`, `schema_version`, `runs`, `task_ids`, `domains`), per-cell
results (`Cell`: mean, spread, flagged, errors), per-provider rollups
(`ProviderScore`: overall + per-domain), and an optional operator-supplied
`human_baseline` reported alongside the agents (never derived from agent runs).

## 6. Subsets and the held-out partition

A task joins a named subset by listing it in `subset`. Two names are canonical:

- `test_small` (`guibench.SubsetTestSmall`): a representative slice a CI run
  scores without paying for the full matrix.
- `held_out` (`guibench.SubsetHeldOut`): tasks reserved for maintainer-run
  verified scoring, never published in the public split, so an external
  submitter cannot tune against them.

`guibench.SelectSubset(tasks, name)` returns a subset (and errors loudly if the
name matches no task). The corpus manifest (`guibench.BuildManifest`) records the
public/held-out partition and the corpus + verifier versions.

## 7. Egress lockdown (contamination defense)

By default a task runs **fully offline** during scoring so the agent cannot
`wget` a gold reference into the path the verifier checks — the Berkeley RDI
contamination exploit that reached a 73% bogus score on OSWorld (design 047 §8).
**Gold references never live in the task config; they stay host-side in the
verifier.** A task that genuinely needs the network lists exactly the domains it
may reach in `network_allow`; `guibench.TaskEgress(task)` derives the lockdown
(`offline` by default, a `task-allow` policy with the explicit allowlist
otherwise), and `EgressLockdown.CheckPolicy` lets the runner confirm the applied
cove network policy matches before the agent runs.

## 8. Reference: enumerated values

- **Metric names** (`guibench.Metrics()`): `exact_match`, `must_include`,
  `fuzzy_match`, `file_exists`, `hash_equals`, `plist_equals`,
  `sqlite_row_matches`, `url_in`, `infeasible`. (`fuzzy_match` is a string
  normalizer, never an LLM judge; a `vlm_judge` metric, when added, is flagged
  non-deterministic and excluded from the headline reproducible score.)
- **Getter kinds**: `exec`, `file`, `defaults`, `screen_ocr` (Tier A);
  `sqlite`, `protected_file`, `tccdb` (Tier B); `applescript`, `accessibility`
  (Tier C).
- **Conjunctions**: `and` (mean), `or` (max).
- **Variance flag threshold**: `0.20`.
- **Schema version**: `v1`.

Live `cove bench gui metrics` prints the registered metric names so the corpus
author can confirm the running binary's set.
