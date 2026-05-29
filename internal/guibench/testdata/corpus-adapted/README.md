# guibench adapted corpus (intent-ported third-party subset)

The portable subset of OSWorld / WebArena / cua-bench re-expressed against
native macOS. Each task cites its upstream benchmark and task id in `source`
following the adapter convention, and is parameterized + self-checkable exactly
like the [seed corpus](../corpus-v0/README.md). The full convention, the
getter/metric mapping, and the honest "deliberately NOT ported" log live in
[docs/benchmarks/guibench/adapters.md](../../../../docs/benchmarks/guibench/adapters.md).

## The adapter trap

Porting a foreign-app task verbatim tests that foreign app running on macOS, not
a native-macOS workflow (design 047 §16). Adaptation is therefore limited to two
modes, enforced by `TestCorpusAdaptedNoForeignAppInstall` /
`TestCorpusAdaptedIntentTasksAreNative`:

- **`mode=port`** — the upstream task is genuinely cross-platform (web,
  filesystem/Terminal, generic OS settings); only the verifier is re-pointed at
  the macOS surface.
- **`mode=intent`** — the upstream task targets a foreign app, so only its goal
  is re-expressed against an Apple-native app (Numbers, Mail, Reminders, ...).
  The foreign app is never installed.

## Source convention

```
adapted:<benchmark>:<upstream-id> mode=<port|intent>; <rationale>
```

Parsed by `Task.Provenance()`; `TestCorpusAdaptedEveryTaskCitesUpstream` asserts
every task here carries a valid one.

## Tasks

| Task                            | Mode   | Upstream                              | Domain    | Getter (tier) / metric          |
|---------------------------------|--------|--------------------------------------|-----------|---------------------------------|
| safari-do-not-track             | port   | osworld chrome 030eeff7              | Safari    | defaults (A) / plist_equals     |
| safari-navigate-tab             | port   | webarena task 1                      | Safari    | applescript (C) / url_in        |
| safari-startup-homepage         | port   | osworld chrome 3299584d              | Safari    | defaults (A) / plist_equals     |
| safari-infeasible-offline-route | port   | webarena task 101 (N/A)              | Safari    | exec (A) / infeasible           |
| terminal-append-suffix          | port   | osworld os 5ced85fc                  | Terminal  | exec (A) / exact_match          |
| terminal-chmod-files            | port   | osworld os 4d117223                  | Terminal  | exec (A) / exact_match          |
| terminal-copy-tree              | port   | osworld os 4783cc41                  | Terminal  | exec (A) / exact_match          |
| terminal-count-lines            | port   | osworld os 4127319a                  | Terminal  | file (A) / exact_match          |
| finder-collect-by-ext           | port   | osworld os 23393935                  | Finder    | exec (A) / exact_match          |
| finder-zip-folder               | port   | osworld os 37887e8c                  | Finder    | exec (A) / exact_match          |
| settings-output-volume          | port   | osworld os 28cc3b7e                  | Settings  | applescript (C) / exact_match   |
| numbers-column-total            | intent | osworld libreoffice_calc 0326d92d    | Numbers   | exec (A) result+expected / exact_match |
| mail-plain-signature            | intent | osworld thunderbird 3f28fe4f         | Mail      | applescript (C) / must_include  |
| reminders-create-item           | intent | cua-bench apps/reminders.py          | Reminders | applescript (C) / exact_match   |

One Safari task is `infeasible` (WebArena's unachievable-task / `llm_ua_match`
intent): success is the agent answering `FAIL`; it carries no solution.

## Validation

```
# VM-free load + validate
cove bench gui validate -corpus internal/guibench/testdata/corpus-adapted

# Live self-check on Apple-Silicon hardware
cove bench gui selfcheck -corpus internal/guibench/testdata/corpus-adapted
```

The corpus loads, validates, and self-checks against a stateful fake guest under
`go test ./internal/guibench/...`; the live self-check confirms it on real
hardware.
