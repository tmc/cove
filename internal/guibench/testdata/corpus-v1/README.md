# guibench corpus v1 — parity-floor expansion

The second cove-original native-macOS task corpus (design 047 §9), scaling the
seed corpus toward the 116-154 parity floor and deliberately exercising the
verifier fidelity layer the v0 corpus did not. Each file is one parameterized
[Task](../../task.go) with the same discipline as v0: a typed parameter schema,
an instruction templated over those params, ordered setup steps, a known-good
`solution` the self-check runs to confirm the verifier, and an evaluator whose
getter reads live end-state and whose metric scores it. The gold value derives
from the same seeded params, so it never goes stale (anti-memorization, §10).

## What this corpus adds over v0

- **AX-tree verification** (`accessibility_match`): System Settings toggles
  (Bluetooth, Night Shift, True Tone) verified from the live AX switch value
  rather than an async-flushed plist, plus a scalar AX read of the Calculator
  display. The macOS analogue of OSWorld `check_accessibility_tree`.
- **Before/after SQLite integrity** (`rows_added_integrity` /
  `rows_removed_integrity`): Reminders/Notes/Calendar/Contacts and a
  Terminal-driven sqlite db, ported from AndroidWorld
  `validate_rows_addition_integrity` / `validate_rows_removal_integrity`. A
  whole-table snapshot is frozen during Config; the metric scores 1 only when
  the target row was added/removed AND every other row is intact — so deleting
  every note (then re-creating the target) scores 0, the collateral-damage
  false positive a single-point read misses.
- **Image fidelity** (`image_similar`): Preview rotate/flip/grayscale verified
  with a thresholded MSE that tolerates the re-encode + EXIF rewrite breaking
  `hash_equals`. Source images are synthesized in Config (no committed binaries).
- **URL normalization** (`url_match_normalized`): Safari/Maps navigations
  verified after stripping tracking/session query noise, with `keep_query` for
  the load-bearing search term.
- **PDF text** (`pdf_contains`) and **AND/OR composition** (a `conj` over a
  metric list) for lenient acceptance.

## New app coverage

Mail (intent-port of Thunderbird compose), Calendar, Reminders, Contacts, Maps,
TextEdit (intent-port of a Writer doc task), and Calculator — first-party apps
the v0 corpus lacked. No GIMP/LibreOffice/VLC/Office is required: foreign tasks
are re-expressed against the Apple-native app, never ported verbatim (§3 adapter
rule). iCloud/Keychain/Apple-ID surfaces remain excluded (§6 shared-SEP hazard,
enforced by `TestCorpusV1NoAppleIDTasks`).

## Getter tiers

Tier-A (exec/file/defaults), Tier-B (sqlite), Tier-C (applescript /
accessibility) are all present; each task declares its tier, and the base image
must carry exactly the grants the corpus's getters need (the highest is reported
by `guibench.MaxTier`). `NetworkAllow` is empty on every task (offline scoring,
§8) — none needs the network.

## Self-check

The corpus loads and validates without a VM (`TestCorpusV1*`). The host-runnable
tasks (sqlite/defaults/file/sips driven) are self-checked end-to-end against a
host-shell fake guest across seeds 1-4 by `TestCorpusV1HostSeedInvariant`,
proving good->1 / no-op->0 for every materialized value. The AppleScript/AX
tasks need live first-party apps, so their self-check is the operator's
confirmation on Apple-Silicon hardware:

```
cove bench gui selfcheck -corpus internal/guibench/testdata/corpus-v1
```
