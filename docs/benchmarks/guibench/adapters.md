# guibench third-party adapters

This doc defines how a guibench [`Task`](../../../internal/guibench/task.go) records
that it was adapted from a foreign computer-use benchmark, how a foreign
verifier's intent maps onto cove getters and metrics, and — most importantly —
which foreign task categories are **deliberately not ported**, and why.

The adapter capability exists because OSWorld, WebArena, AndroidWorld, and
WindowsAgentArena have already done the hard work of writing thousands of
verifiable agent tasks. A subset of that work is genuinely portable to a
native-macOS benchmark. Most of it is not, and copying it anyway would quietly
turn a macOS benchmark into a "Linux/Windows app running on macOS" benchmark,
which measures the wrong thing.

## The adapter trap

Porting a foreign-app task verbatim tests *that foreign app running on macOS*,
not a native-macOS workflow. An OSWorld "fill the blank cells in B1:E30 in
LibreOffice Calc" task, ported literally, would require installing LibreOffice
on macOS and would score the agent on driving LibreOffice's Linux-flavored UI.
That is a distraction from the wedge guibench occupies: **verifiable tasks on
the apps a Mac user actually has** (Finder, Safari, Numbers, Mail, Notes,
Reminders, System Settings, Terminal) (design 047 §16).

So adaptation is allowed in exactly two modes, and the corpus test
(`TestCorpusAdaptedNoForeignAppInstall`,
`TestCorpusAdaptedIntentTasksAreNative`) enforces it.

### Mode `port` — genuinely cross-platform

The upstream task is already platform-agnostic, so the same goal *is* a native
macOS workflow. No re-expression is needed; only the verifier is re-pointed at
the macOS surface. Three families qualify:

| Upstream family            | macOS surface        | cove getter (tier)            |
|----------------------------|----------------------|-------------------------------|
| OSWorld `chrome/*`, WebArena web | Safari / Settings | `applescript` (C), `defaults` (A) |
| OSWorld `os/*` file & text | Finder / Terminal    | `exec`, `file` (A)            |
| Generic OS settings        | System Settings      | `defaults` (A), `applescript` (C) |

A `port` task may keep a cross-platform domain (`Terminal`, `Finder`, `Safari`,
`Settings`). Example: OSWorld Chrome "enable Do Not Track" is a generic
browser-privacy toggle, so `safari-do-not-track` reads `com.apple.Safari
SendDoNotTrackHTTPHeader` through the Tier-A `defaults` getter — no Chrome, no
re-interpretation.

### Mode `intent` — foreign-app intent re-expressed natively

The upstream task targets a foreign app with no honest cross-platform reading,
so **only its goal is carried over**, re-expressed against an Apple-native app.
The foreign app is never installed. An `intent` task must have an Apple-native
`domain` (`Numbers`, `Pages`, `Mail`, `Reminders`, `Notes`, `Calendar`,
`Keynote`, `Preview`). Examples:

- OSWorld LibreOffice Calc "calculate the total sales in a row called Total" →
  `numbers-column-total`: the *intent* (sum a column, append a labeled total) is
  re-expressed against Apple Numbers, verified on the exported CSV.
- OSWorld Thunderbird "set up a plain text signature" → `mail-plain-signature`:
  re-expressed against Apple Mail, signature content read via AppleScript.
- AndroidWorld-style "create a task/reminder" (using trycua cua-bench's
  native Apple Reminders getter as the macOS analogue) → `reminders-create-item`.

## The Source convention

An adapted task encodes its provenance in the `source` field, which
[`Task.Provenance`](../../../internal/guibench/adapter.go) parses:

```
adapted:<benchmark>:<upstream-id> mode=<port|intent>; <free-text rationale>
```

- `<benchmark>` — the upstream benchmark, lowercased: `osworld`, `webarena`,
  `androidworld`, `winarena`, `cua-bench`.
- `<upstream-id>` — the foreign task's own identifier, **copied verbatim** so the
  lineage is auditable (an OSWorld UUID, a WebArena `task_id`, a source path).
- `mode=` — `port` or `intent`, as above.
- the rationale states what the upstream verifier checked and which cove
  getter/metric reproduces that check.

A cove-original task carries no `adapted:` tag (`Task.IsAdapted()` returns
false) and has no provenance. The whole `testdata/corpus-adapted/` corpus is
adapted-only; `TestCorpusAdaptedEveryTaskCitesUpstream` asserts every task
parses to a valid `Provenance`.

## How a foreign verifier maps to cove getter + metric

guibench mirrors OSWorld's getter/metric split, so most foreign checks have a
direct analogue. The adaptation is a re-pointing, not a reimplementation.

| Foreign verifier check                                  | cove getter      | cove metric            |
|---------------------------------------------------------|------------------|------------------------|
| OSWorld `active_tab_url_parse` / WebArena `url_match`    | `applescript` (active-tab URL) | `url_in`, `url_match_normalized` |
| OSWorld `check_include_exclude` on file/terminal output  | `exec`, `file`   | `must_include`, `exact_match` |
| OSWorld plist / preference compare                       | `defaults`       | `plist_equals`         |
| WebArena `string_match` reference answer                 | `exec` / getter  | `exact_match`, `fuzzy_match` |
| WebArena `llm_ua_match` (unachievable → decline)         | n/a (terminal answer) | `infeasible`      |
| AndroidWorld sqlite row / integrity                      | `sqlite`         | `sqlite_row_matches`, `rows_added_integrity` |
| OSWorld accessibility-tree XPath/CSS                     | `accessibility`  | `accessibility_match`  |

Every adapted task still obeys the corpus invariants: parameterized schema with
a seeded gold answer, a known-good `solution` for the verifier self-check (or
`infeasible: true` with no solution), and a config baseline derived to differ
from the goal so a no-op scores 0 (design 047 §9, §10).

## Deliberately NOT ported (the honest log)

These upstream categories were reviewed and **excluded**, because porting them
would require installing or driving a foreign app on macOS — the adapter trap.
They are recorded here so a future contributor does not "complete the port" and
silently break the wedge.

| Upstream category           | Why excluded                                                            |
|-----------------------------|-------------------------------------------------------------------------|
| OSWorld `gimp/*`            | GIMP is not a Mac-native app; intent (image edits) is covered natively by Preview + `image_similar`, not by installing GIMP. |
| OSWorld `libreoffice_*` (verbatim) | LibreOffice Calc/Writer/Impress are foreign apps. Only the *intent* of select calc tasks is re-expressed against Numbers; the rest are out of scope until Pages/Numbers/Keynote getters grow. |
| OSWorld `vlc/*`             | VLC is a foreign app; the native analogue (QuickTime) has no verifiable end-state we read today. |
| OSWorld `thunderbird/*` (verbatim) | Thunderbird is a foreign app. One signature task's *intent* is re-expressed against Apple Mail; account/IMAP setup is excluded (network + foreign-protocol surface). |
| OSWorld `vs_code/*`         | VS Code is a foreign (Electron) app; not a default-Mac competency. |
| OSWorld `multi_apps/*` that include any of the above | Inherit the exclusion of their foreign component. |
| WAA (WindowsAgentArena) Windows-shell / Office tasks | Windows-specific apps and shell; no native-macOS analogue worth re-expressing. |
| Any task installing software (e.g. OSWorld "install Spotify") | Installation is a package-manager workflow, not a GUI competency, and pollutes the RAM-overlay fork. |

When a foreign task does not cleanly fit `port` or `intent`, **prefer a
cove-original native task** over a strained adaptation.

## Running the adapted corpus

The adapted corpus is just another `-corpus` directory; every existing
`cove bench gui` subcommand operates on it.

```
# VM-free: load + validate (this is the CI gate, no Apple Silicon needed)
cove bench gui validate -corpus internal/guibench/testdata/corpus-adapted

# Versioned manifest (records the corpus + verifier versions of a result)
cove bench gui manifest -corpus internal/guibench/testdata/corpus-adapted

# Live (needs Apple-Silicon hardware + the pre-granted base image):
# confirm every gold solution scores 1.0 and every no-op scores 0.0
cove bench gui selfcheck -corpus internal/guibench/testdata/corpus-adapted

# Live: inspect GUI-action-to-disk-state lag for one adapted task
cove bench gui examine -corpus internal/guibench/testdata/corpus-adapted \
  -task-id mail-plain-signature
```

The Go-level VM-free gate is `go test ./internal/guibench/...`, which loads,
validates, parses the provenance of, and self-checks (against a stateful fake
guest) the adapted corpus. The live `selfcheck` is the operator's confirmation
on real hardware.
