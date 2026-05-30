# 048 — Guest-side AX snapshot over the control socket

Status: design (unscheduled, `maybe`). Deepens [047](047-gui-agent-benchmark-harness.md)
Slice 3 (Tier-C AX-tree getters) and resolves the §13 open question:

> AX-tree getter: a clean `ctl agent-exec` AX helper (osascript/Swift one-shot)

This doc decides *what* that helper should emit and *how* it crosses the
host/guest boundary, grounded in a deep pass over
[`github.com/tmc/axmcp`](https://github.com/tmc/axmcp) — a mature macOS
accessibility-automation toolkit that already builds the structured AX snapshot
047 wants, on the same `github.com/tmc/apple` (`apple/x/axuiautomation`)
dependency cove already pins.

## 1. The problem this resolves

047 §5 names the AX tree "the reliable synchronous GUI-state probe" — the
verifier of first resort, because pixels (OCR) are noisy and SQLite/file reads
(Tier B) only see persisted state, not live UI. The corpus's
`accessibility_match` metric selects over an AX-tree document.

The shipped getter (`internal/guibench/getter.go`, `getAccessibility` →
`axDumpScript`) realizes this as a **one-shot JXA program** run through
`osascript` over the existing exec transport (the 047 §13 "no guest-agent RPC,
no proto bump" path). It walks `System Events`' `uiElements()` of the front
window and emits XML:

```xml
<ax app="Notes">
  <node role="AXWindow" title="Notes" identifier="" value="">
    <node role="AXTextArea" title="" identifier="" value="Buy milk"/>
  </node>
</ax>
```

That was the right first move — it shipped the metric end-to-end with zero new
guest surface. But it carries exactly four attributes per element
(`role`, `title`, `subrole`→`identifier`, `value`) and has three structural
limits that bound how precise an `accessibility_match` assertion can be:

1. **No geometry.** No `x/y/w/h`. A task cannot assert "the Done button is
   visible in the toolbar" or disambiguate two same-titled elements by position.
2. **No state flags.** No `enabled`, no `settable`, no available actions. A task
   cannot assert "Save is enabled" or "the checkbox is checked-and-toggleable".
3. **No stable element identity.** Elements are positional in nested XML; there
   is no index a metric (or a trajectory step) can reference across two
   snapshots to say "element 7 changed from unchecked to checked".

There is also a robustness cost: System Events' AppleScript/JXA bridge is slow
(one Apple-Event round-trip per attribute per element) and brittle on deep
hierarchies (the dumper already hard-caps depth at 20 to avoid runaway walks).

## 2. The load-bearing boundary fact

`apple/x/axuiautomation` is a **local-OS binding**: `NewApplication(bundleID)` /
`NewApplicationFromPID(pid)` call `AXUIElementCreateApplication` against a PID in
*the same OS as the calling process*, and the AX API "must be called from the
main thread" (per the package doc). axmcp is built entirely on this — every one
of its four MCP servers (`axmcp`, `computer-use-mcp`, `xcmcp`,
`iphonemirror-mcp`) reads the AX tree of the OS it runs in, over stdio MCP, with
no network/RPC transport of its own.

`VZVirtualMachineView` (cove's host-side framebuffer NSView) is **pixels only**.
The guest's AX tree lives in the guest's WindowServer; the host process cannot
`AXUIElementCreateApplication` into it. This is the same boundary established for
the OCR getter (047 §5): vision and accessibility of the guest must be computed
**in the guest** and returned over the control socket.

So "expose axmcp-semantics in cove via proxying" has exactly one productive
reading: **run the AX-snapshot logic inside the guest and surface its result
over the control socket.** Re-exposing a *host*-run axmcp would automate the
developer's own desktop, not the VM — wrong target for both the benchmark and
cove's hermetic-sandbox purpose. (See §7 for that rejected option.)

## 3. What axmcp already solves

axmcp's `computer-use-mcp` implements the OpenAI/Codex Computer Use 9-tool
contract (`list_apps`, `get_app_state`, `click`, `set_value`, `scroll`, `drag`,
`press_key`, `type_text`, `perform_secondary_action`) on top of the AX tree,
plus trajectory record/replay. Its `internal/computeruse/appstate` builder
(`Build(ctx, selector, windowTitle, instructions) → Snapshot`) BFS-walks
`axuiautomation.Application` into a structured snapshot:

- `AppState{ App, Window, Tree []ElementNode, ScreenshotPNGBase64, ... }`
- `ElementNode{ Index, ParentIndex, Role, Title, Value, Description,
  Identifier, X, Y, Width, Height, Enabled, Settable, SecondaryActions }`

That is precisely the superset the cove getter is missing: a flat, **indexed**,
parent-linked node list with geometry, enabled/settable flags, and per-element
actions — the same `Index` model a stateful `get_app_state → element_index → act`
loop and a two-snapshot diff both need. axmcp has already paid for the hard
parts (the BFS walk, settable/action probing, element indexing, the Codex
contract shape, and trajectory record/replay).

## 4. Decision

**Adopt the structured AX snapshot as a first-class guest probe, surfaced over
the control socket, reusing axmcp's `computeruse/appstate` snapshot semantics
rather than reinventing them.** Do *not* import axmcp as a dependency or run it
on the host; vendor/borrow the snapshot logic into the guest agent (§5, §6).

This is the AX-tree-fidelity deepening of 047 Slice 3, and it realizes the
roadmap's "path B" AX-tree validation (an in-guest structured AX dumper) with a
mature reference implementation as the source of truth for the shape.

## 5. Transport: two shapes, 2b preferred

The guest probe surface today is `Probe` (`getter.go`):

```go
type Probe interface {
    Exec(args []string, env map[string]string, workDir string) (exitCode int, stdout, stderr string, err error)
    ReadFile(path string) ([]byte, error)
    OCRAllText() (string, error)
}
```

`OCRAllText` is the precedent to mirror: a *structured* guest computation
(Vision OCR) returned over a dedicated `controlpb` RPC (`Client.OCRAllText`,
control.go), not squeezed through `Exec`. `AX snapshot` is the same kind of
thing — a structured, in-guest computation that doesn't fit a stdout string
cleanly.

**Option 2a — in-guest MCP endpoint.** Bake `computer-use-mcp` (or a slim
`get_app_state` subset) into the guest base image; broker its stdio MCP over the
control socket to a host-side `cove`-exposed MCP endpoint. Maximal fidelity (the
full Codex contract, record/replay) but heavyweight: a long-lived guest MCP
process, a stdio↔vsock bridge, and MCP framing cove doesn't otherwise speak.

**Option 2b — single `AXSnapshot` RPC on the existing agent (preferred).** Add
one control RPC, `AXSnapshot(app, window) → AppState`, implemented in the guest
agent by calling the borrowed `appstate` builder against the named guest app.
Surface it as:

```go
// New Probe seam (additive; FakeProbe returns canned snapshots, like OCRAllText).
type AXSnapshotter interface {
    AXSnapshot(app, window string) (*AppState, error)
}
```

`ClientProbe.AXSnapshot` calls a new `Client.AXSnapshot` (a `controlpb` request
type alongside the existing `AgentExec*`/OCR ones); the unit-test `FakeProbe`
returns a canned `AppState` so `accessibility_match` stays unit-testable without
a VM (047 §5 discipline). This mirrors the `Screenshotter` optional-seam pattern
already in `trajectory_oracle.go` (an optional interface a backend may satisfy,
kept *off* the minimal `Probe` so backends that can't do it still compile).

**2b is preferred:** it matches cove's request/response RPC style, adds one
method instead of an MCP subsystem, has no long-lived guest process, and reuses
the agent's existing exec/permission plumbing. 2a is the fallback only if we
later want the *full* Codex contract (acting, not just reading) brokered into
cove — and even then it can layer on top of 2b's snapshot.

## 6. Reuse strategy and version skew

axmcp is `module github.com/tmc/axmcp`, **go 1.26**, `tmc/apple v0.6.9`,
`modelcontextprotocol/go-sdk v1.4.0`. cove is **go 1.25.5**, `tmc/apple v0.6.11`
(carried by main's bump). Two consequences:

- **Don't import axmcp wholesale.** The go-sdk dependency, the go 1.26 floor, and
  the v0.6.9 apple pin all conflict with cove's tree. Importing it drags in MCP
  machinery the 2b RPC path doesn't need.
- **Borrow the snapshot logic.** `internal/computeruse/appstate` depends only on
  `apple/x/axuiautomation` — the *same* module cove already uses (cove is even
  *ahead* at v0.6.11). Vendoring the `AppState`/`ElementNode` types and the
  `buildState`/`snapshotNode` walk into cove's guest agent is a clean, MCP-free
  lift. Confirm the `axuiautomation` surface the walk uses
  (`NewApplication`/`NewApplicationFromPID`, `AXUIElementCopyAttributeValue`,
  `AXUIElementIsAttributeSettable`, `AXUIElementCopyActionNames`) is present and
  unchanged at v0.6.11 before lifting; the API listing shows all of these exist.

Credit axmcp in the borrowed file header (same author, `github.com/tmc`), and
keep the vendored copy small — the snapshot builder, not the MCP servers.

## 7. Rejected: proxy host-run axmcp into cove

cove *could* spawn `axmcp`/`computer-use-mcp` as a host subprocess and re-expose
its stdio MCP through a `cove serve` MCP endpoint. Rejected: host-axmcp drives
the host Mac's apps, not the guest VM (§2). It serves neither the benchmark
(which scores guest state) nor cove's reason to exist (hermetic guest
automation). The only legitimate host target is the cove GUI window itself —
niche, and not worth an MCP subsystem.

## 8. Prerequisites and caveats

- **Tier-C grant.** The guest base image must carry the Accessibility grant
  (independent TCC service from Apple Events / FDA — 047 §5, and the cove memory
  note that FDA pre-grant does *not* cover Apple Events or AX). `cove doctor`
  must verify it before `image save`, same as the existing Tier-C getters. A
  denial surfaces as an RPC error, mirroring the current nonzero-exit behavior.
- **Main-thread + run-loop discipline.** `axuiautomation` must run on the guest
  agent's main thread with `runtime.LockOSThread` (per its package doc); the
  guest agent already has a main-thread story for input/capture to slot into.
- **Backward compatibility.** The XML `axDumpScript` path stays as the
  zero-dependency fallback (and for substrates without the borrowed builder).
  `accessibility_match` should accept both the XML document and the structured
  `AppState` (e.g. select by `role`/`title`/`identifier` against either), so
  existing corpus tasks keep scoring unchanged while new tasks can assert on
  geometry/enabled/settable/actions.
- **No public surface.** Privacy gate: nothing here is published; the snapshot is
  an internal verifier/probe, not an exported registry artifact.

## 9. Tie-in to the benchmark

- **`accessibility_match` fidelity (047 §5).** Geometry + state flags + stable
  indices let the metric make assertions the XML dumper can't, raising verifier
  precision — the "verifier rigor as brand" play (047 §16, ROADMAP Slice 6b).
- **Path-B AX-tree validation.** A structured in-guest snapshot is exactly the
  golden-fixture source for validating the metric against real guest UI
  (the open Phase-10 AX-tree validation item).
- **Trajectory export (ROADMAP Slice 4b).** axmcp's record/replay and the
  indexed `ElementNode` model align with cove's HF-schema trajectory export: an
  `AppState` per step is a richer UI-grounding observation than OCR text, and the
  element `Index` gives actions a stable referent — strengthening the
  "native-macOS UI-grounding dataset no competitor has" claim.
- **Reference agent backend (047 §9 Slice 7).** The same Codex 9-tool contract
  axmcp implements is what cove's agent-sandbox providers target; 2a (if pursued
  later) could expose a guest-side reference backend *and* a verifier source from
  one in-guest component.

## 10. Open questions

- Borrow-and-vendor vs. a thin `tmc/apple`-level shared package both repos
  import? Vendoring is faster now; a shared package avoids drift if axmcp and
  cove both keep evolving the snapshot shape. Lean vendor-now, revisit if the
  shapes co-evolve.
- Does `accessibility_match` gain structured selectors (by index, by geometry,
  by enabled/settable) or stay string/XML-shaped with the `AppState` serialized
  to the same XML? Serializing to the existing XML is the smallest step and keeps
  one metric path; structured selectors are a follow-on.
- Snapshot scope: front window only (matching today's dumper) vs. whole-app vs.
  selectable subtree? Start front-window to match the shipped metric; widen if a
  task needs cross-window state.
```
