# v0.4 Anthropic adapter v2

**Status**: v2 implementation target. Live API verification pending unless a
running disposable VM and `ANTHROPIC_API_KEY` are available.
**Supersedes**: Design 022 v1 in this file, accepted planning input for the
Python `cove-claude-sandbox` package.
**Source**: [/tmp/cove-v04-audit-a4k2.md](../../tmp/cove-v04-audit-a4k2.md)
(read-only audit of v0.3 GA `main`).
**Roadmap**: v0.4.
**Branch**: planning.

## Goal

Provide an Anthropic adapter that mirrors the existing OpenAI Agents SDK
adapter at [`adapters/openai-agents-python/`](../../adapters/openai-agents-python/).
Both adapters give a hosted model — Claude or GPT — computer-use tooling
backed by a forked cove macOS VM: the model issues `screenshot`, `click`,
`type`, `key`, `scroll`, `bash`, and `text_editor` actions; the adapter
translates them into cove control-socket and guest-agent calls; cove owns
fork-per-run and teardown.

The adapter is shaped so a single user can write
`AnthropicSandbox(parent="macos-base").run(...)` the same way they already
write `CoveSandbox(parent="macos-base")` for OpenAI.

## v2 Goal

Design 022 v2 moves the active Anthropic computer-use bridge into cove's Go
runtime so `cove agent-sandbox run --provider anthropic` does not shell out to
the older Python bridge. The Go adapter owns the Anthropic Messages API loop and
dispatches tool calls through the cove control socket.

The Python `adapters/anthropic-sandbox-python/` package remains useful as a
library surface and reference implementation. The CLI path uses the Go adapter
because it already owns fork-per-run lifecycle, run bundles, metrics, replay
artifacts, and control-socket selection.

## v2 Agent Loop

The loop calls Anthropic Messages with the current computer-use beta enabled:

```go
client.Beta.Messages.New(ctx, anthropic.BetaMessageNewParams{
    Betas: []anthropic.AnthropicBeta{"computer-use-2025-11-24"},
    Tools: []anthropic.BetaToolUnionParam{{
        Type: "computer_20251124",
        Name: "computer",
        DisplayWidthPx: width,
        DisplayHeightPx: height,
    }},
    Messages: messages,
})
```

The exact SDK field names are allowed to vary with `anthropic-sdk-go`; the
wire contract is not:

- beta header: `computer-use-2025-11-24`
- tool type: `computer_20251124`
- tool name: `computer`
- display dimensions: read from the VM through the cove control socket
- response handling: append assistant `tool_use` blocks, execute them, append
  user `tool_result` blocks, repeat until no `tool_use` remains

The adapter returns a transcript entry for every request, response, tool call,
and tool result. Tool execution failures are not fatal to the loop; they are
returned to Claude as `{"type":"tool_result","tool_use_id":id,"is_error":true,
"content":"..."}` so the model can self-correct. API errors, context
cancelation, and max-step exhaustion are fatal.

## v2 Tool Mapping

| Anthropic action | Cove control operation |
|---|---|
| `screenshot` | control socket screenshot, returned as PNG image content |
| `left_click` / `click` | `mouse-click` at scaled host coordinates |
| `type` | text injection |
| `key` / `keypress` | key event with cove key-code mapping |
| `scroll` | mouse-scroll when available, otherwise page-up/page-down key fallback |
| `cursor_position` | mouse position query |

Display dimensions come from the VM screen-size control command. If screen-size
is unavailable, the adapter falls back to 1024x768 and records the limit in the
transcript. Coordinates are scaled back to host pixels before dispatch.

## Why it's feasible

The primitives the adapter needs already exist on `main`:

- **VM fork-per-run**: [design 013](013-vm-fork.md), shipped as
  `cove run -fork-from`.
- **Control socket**: [design 002](002-cove-disks-oci.md) era,
  `~/.vz/vms/<name>/control.sock`, with screenshot, click, type, key,
  scroll, vsock.
- **Guest agent**: `cmd/vz-agent` + connect-go RPCs (`Exec`, `Shell`,
  `Write`, `Read`, dual-port routing for TCC, see project memory note
  `project_agent_routing.md`).
- **Reference adapter**: OpenAI Agents SDK adapter at
  `adapters/openai-agents-python/` (~1,135 LOC across `computer.py`,
  `client.py`, `sandbox.py`, `backend.py` + tests). Shape, packaging,
  README live-smoke instructions are already settled.

## Slice 1 — SDK survey

The OpenAI adapter plugs into a first-class `ComputerTool` interface that
the OpenAI Agents SDK invokes via callback. This slice answers the
question the v0.4 audit flagged: **does Anthropic's SDK have an
equivalent primitive, or does the adapter have to drive the agent loop
itself?**

Slice 1 has no code output. Its deliverable is the table below plus
the shape decision in the next subsection.

### Survey result

| Question | Finding | Source |
|---|---|---|
| Is there an "Anthropic Agents SDK" analogous to `openai-agents`? | Yes: `claude-agent-sdk-python` (`anthropics/claude-agent-sdk-python`). | [GitHub](https://github.com/anthropics/claude-agent-sdk-python) |
| Does it expose a `ComputerTool` / computer-use primitive? | **No.** The SDK provides `query()`, `ClaudeSDKClient`, `@tool` decorator, in-process MCP servers, and `HookMatcher`. Built-in tools are Claude Code's file/bash toolset (Read/Write/Edit/Bash), not a screenshot/mouse/keyboard primitive. | repo README + `@tool` reference |
| Where does computer-use actually live? | Directly in the Messages API: `client.beta.messages.create(...)` with `tools=[{"type": "computer_20251124", ...}, {"type": "bash_20250124", ...}, {"type": "text_editor_20250728", ...}]` and beta header `computer-use-2025-11-24` (or `computer-use-2025-01-24` for older models). The developer runs the agent loop. | [docs](https://platform.claude.com/docs/en/docs/agents-and-tools/computer-use) |
| Reference implementation? | `anthropics/anthropic-quickstarts/computer-use-demo` — Docker container + sampling loop (`loop.py`) + tool implementations. | linked from docs |
| Tool-use return shape | `tool_use` content blocks in the assistant message; reply with a `user` message whose `content` is `[{"type": "tool_result", "tool_use_id": ..., "content": ...}]`. | docs, "How computer use works" |
| Streaming tool results? | Streaming is supported on Messages API but tool execution is still developer-side; no native streaming-tool-result API. | docs |
| Model coverage as of 2026-05 | `computer_20251124`: Opus 4.7 / 4.6 / 4.5, Sonnet 4.6. `computer_20250124`: Sonnet 4.5, Haiku 4.5, Opus 4.1, Sonnet 4, Opus 4, Sonnet 3.7 (deprecated). | docs |

### Shape decision

Because there is **no first-class `ComputerTool` callback** in
`claude-agent-sdk-python`, the cove Anthropic adapter cannot mirror the
OpenAI shape verbatim. Two options:

1. **Plug into `claude-agent-sdk-python` via `@tool`/MCP**. Reject for
   v0.4: the SDK's tool surface is text-shaped, not the Anthropic-schema
   computer-use tool that Claude is trained to use. Plumbing screenshot
   bytes through `@tool` would force us off the documented happy path
   and lose the trained tool-selection behaviour.
2. **Drive the Messages API agent loop directly** (mirror
   `anthropic-quickstarts/computer-use-demo/loop.py`). Adopted. The
   adapter owns the loop: takes a user prompt, appends `tool_use`
   results, calls `client.beta.messages.create(...)` until the model
   returns no more `tool_use` blocks. The "callback" in OpenAI's
   `ComputerTool` becomes our internal action dispatcher.

Concretely: the adapter's public surface is `AnthropicSandbox.run(prompt,
*, model, max_iterations)`, not a `ComputerTool` subclass. Users who
already use `claude-agent-sdk-python` for non-computer-use work keep
that SDK; cove plugs in alongside.

## Slice 2 — adapter package

Mirror the OpenAI adapter directory shape. Same Python 3.10+, same
hatchling-built package, same fork-per-run semantics.

### Package layout

```
adapters/anthropic-agents-python/
├── pyproject.toml                  # name = "cove-claude-sandbox"
├── README.md                       # live-smoke + package-check
├── src/cove_claude_sandbox/
│   ├── __init__.py
│   ├── sandbox.py                  # AnthropicSandbox.run()
│   ├── loop.py                     # agent loop (Messages API)
│   ├── actions.py                  # tool_use → cove control-socket
│   └── client.py                   # cove control-socket client (re-use
│                                   #  shape of openai adapter's client.py)
├── examples/
│   └── computer_use.py             # runnable end-to-end
└── tests/
    └── test_loop.py                # mocked anthropic client
```

The cove control-socket client (`client.py`) duplicates rather than
imports the OpenAI adapter's client because the two are independently
versioned Python packages. A unified base class is an open question
(see below).

### Public surface

```python
from cove_claude_sandbox import AnthropicSandbox

sandbox = AnthropicSandbox.from_fork(
    parent="macos-base",
    child="claude-smoke-001",
    cove_bin="./cove",
)
result = sandbox.run(
    prompt="Open Safari and search for cove documentation",
    model="claude-opus-4-7",
    max_iterations=20,
)
print(result.final_text)
sandbox.close()  # tears down the fork
```

`AnthropicSandbox.run()` internally:

1. Reads `ANTHROPIC_API_KEY` from env (matches OpenAI adapter — no 005
   secrets dependency in Slice 2).
2. Builds the tool block list (`computer_20251124` with the VM's display
   width/height read from cove control socket; `bash_20250124`;
   `text_editor_20250728`).
3. Sends `messages.create(..., betas=["computer-use-2025-11-24"])`.
4. For each `tool_use` block: dispatches to `actions.py`
   (`computer` → control-socket; `bash` → guest-agent `Exec`;
   `text_editor` → guest-agent `Read`/`Write`).
5. Appends `tool_result` user message; loops until no `tool_use`.

### Coordinate scaling

The Anthropic docs require resizing screenshots to fit ≤1568px on the
long edge (≤2576 for Opus 4.7) and scaling click coordinates back up
before dispatch. `actions.py` owns this transform; the cove control
socket already returns raw screen pixels.

### Failure rules

- **Tool-use call fails** (control-socket error, guest-agent exec
  non-zero, screenshot capture failure): return a `tool_result` with
  `is_error: true` and a short message to the model, per the docs'
  error pattern. Do not raise to the caller mid-loop.
- **Fork failure** (parent disk locked, disk full): fail-fast — raise
  before the loop starts; never partially fork.
- **`max_iterations` exceeded**: raise `AnthropicSandboxLimitError` so
  the caller can decide to retry or surface to a human.
- **API rate limit / 5xx**: surface as `AnthropicSandboxAPIError`; do
  not auto-retry in Slice 2 (defer backoff to a follow-up if needed).

## Tests

| Slice | Tests |
|---|---|
| 1 | None — Slice 1 output is this design doc. The survey table is the deliverable. |
| 2 | Unit tests for `actions.py` (each `tool_use` shape → expected control-socket call, mocked socket). One mocked-SDK integration test that drives `AnthropicSandbox.run()` against a `MockAnthropic` client returning a scripted sequence of `tool_use`/text blocks, asserting the loop terminates and dispatches the right calls. |

No live-API test gating CI. The README documents a manual live-smoke
runbook (`COVE_PARENT_VM=…`, `ANTHROPIC_API_KEY=…`,
`python examples/computer_use.py`), matching the OpenAI adapter's
operator pattern.

## Non-goals

- **Streaming `tool_result`**: defer to a Slice 3 if Anthropic ships a
  streaming-tool-result API. As of 2026-05 the loop is request/response.
- **Extended thinking integration**: out of scope; the loop accepts a
  `thinking={"type": "enabled", "budget_tokens": ...}` pass-through but
  does not surface thinking output specially.
- **Batch API**, **prompt caching tuning**, or any non-computer-use
  Anthropic feature.
- **Plugging into `claude-agent-sdk-python`**: rejected in Slice 1
  shape decision; revisit only if Anthropic adds a computer-use
  primitive to that SDK.
- **Unified `cove_sandbox.AgentSandbox` superclass spanning OpenAI +
  Anthropic**: see open questions.

## Acceptance gates

- **Slice 1**: this doc lands on `main` with the survey table populated
  and the shape decision recorded.
- **Slice 2**: `pip install -e adapters/anthropic-agents-python[dev]`
  succeeds; `pytest adapters/anthropic-agents-python/tests` is green;
  `examples/computer_use.py` runs against a fresh fork to first
  screenshot without manual intervention.

## Open questions

1. **Unified superclass.** Do we introduce a `cove_sandbox.base` package
   that `cove-sandbox` (OpenAI) and `cove-claude-sandbox` (Anthropic)
   both depend on, or duplicate? Duplication is cheaper now (each
   adapter <500 LOC) but locks in drift. Decide before a third
   adapter ships.
2. **Tool-schema diffs.** OpenAI's `ComputerTool` exposes `screenshot`,
   `click`, `double_click`, `type`, `keypress`, `wait`, `move`, `drag`,
   `scroll`. Anthropic's `computer_20251124` adds `zoom`, `hold_key`,
   `triple_click`, `left_mouse_down/up`. The adapter must support the
   superset for Anthropic; mapping table belongs in `actions.py`. Open:
   do we expose the extra actions through the cove control socket as
   first-class commands, or compose them from primitives in the
   adapter?
3. **Survey verification.** The Slice 1 table was assembled from the
   public `claude-agent-sdk-python` README and the
   `platform.claude.com/docs/agents-and-tools/computer-use` page on
   2026-05-02. Before Slice 2 starts, confirm: (a) no new
   `ComputerTool` primitive landed in `claude-agent-sdk-python`
   between this doc's date and the implementation start, and (b) the
   beta header / tool-type strings have not rolled forward (they
   change roughly every 6 months — the doc currently lists
   `computer-use-2025-11-24` as the latest header).

## Cross-references

- [`docs/designs/035-openai-sandbox-run-config.md`](035-openai-sandbox-run-config.md)
  for the cove `SandboxRunConfig` backend shape that mirrors the OpenAI
  adapter.
- [`docs/examples/anthropic-computer-use.md`](../examples/anthropic-computer-use.md)
  for the current substrate bridge that this design wraps.
- [`docs/quickstart-agent-sandbox.md`](../quickstart-agent-sandbox.md) for the
  unified agent-sandbox story across providers.
