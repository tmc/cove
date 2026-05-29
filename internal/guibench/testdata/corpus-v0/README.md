# guibench seed corpus v0

The first cove-original native-macOS task corpus (design 047 §9 slice 4). Each
file is one parameterized [Task](../../task.go): a typed parameter schema, an
instruction templated over those params, ordered setup steps, a known-good
`solution` (the gold steps a self-check runs to confirm the verifier), and an
evaluator whose getter reads live end-state off a forked guest and whose metric
scores it. The gold answer is derived from the same seeded params, so it never
goes stale and the corpus resists memorization (§10).

## Scope (v1 constraints)

- **No iCloud / Keychain / Apple-ID tasks.** Cloned RAM-overlay siblings share
  one SEP/iCloud identity unless `-recover-identity` runs per fork; those task
  classes are excluded in v1 (design 047 §6). Enforced by
  `TestCorpusV0NoAppleIDTasks`.
- **Tier-A first, AX/AppleScript (Tier C) next, Tier-B sqlite only where
  necessary** (§5). The base image must carry the grants the corpus's getters
  need; the highest is reported by `guibench.MaxTier`.
- **One fresh RAM-overlay fork per task, always** (§6, §12). The runner and the
  self-check both fork per run; nothing is reused or soft-reset.
- **Persisted-state getters flush before reading** (§7): `defaults`/plist reads
  go through `cfprefsd` (and tasks `killall cfprefsd` before reading); the
  `sqlite` getter runs `PRAGMA wal_checkpoint(FULL)`.

## Domains and getter tiers

| Domain   | Tasks | Getter kinds (tier)                       |
|----------|-------|-------------------------------------------|
| Finder   | 5     | exec (A)                                  |
| Terminal | 4     | exec (A), sqlite (B)                      |
| Settings | 3     | defaults / exec (A)                       |
| Safari   | 3     | applescript (C), defaults (A), exec (A)   |
| Notes    | 2     | applescript (C)                           |
| Preview  | 2     | exec (A)                                  |

One Safari task is `infeasible`: there is no such feature, so success is the
agent answering `FAIL` (§7). It carries no `solution`; its self-check uses the
terminal answer.

## Versioning

The corpus is versioned with the verifier (design 047 §7): a citable result
records both `guibench.CorpusVersion(tasks)` and `guibench.VerifierVersion()`.
A scoring-relevant edit (a new getter, a changed metric set, a new/removed task)
changes the version, so two numbers are only comparable when their versions
match.

## Self-check and manual examine

```
# Confirm every gold solution scores 1.0 and every no-op scores 0.0
# (needs Apple-Silicon hardware + the pre-granted base image):
cove bench gui selfcheck -corpus internal/guibench/testdata/corpus-v0

# Inspect GUI-action-to-disk-state lag for one task: runs setup, pauses for you
# to act on the guest, then prints exactly what the getter reads:
cove bench gui examine -corpus internal/guibench/testdata/corpus-v0 \
  -task-id settings-appearance
```

The corpus loads and validates without a VM (`cove bench gui validate`), and the
self-check logic is unit-tested against a fake guest; the live self-check is the
operator's confirmation on real hardware.
