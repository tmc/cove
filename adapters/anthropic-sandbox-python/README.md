# cove-claude-sandbox

`cove-claude-sandbox` connects Anthropic computer use to a forked cove macOS VM.
It mirrors the shape of `adapters/openai-agents-python`, but the Anthropic
adapter owns the Messages API agent loop.

Anthropic computer use is not a `ComputerTool` callback like the OpenAI Agents
SDK. The adapter calls `client.beta.messages.create(...)` with the
`computer-use-2025-11-24` beta header, executes returned `tool_use` blocks
against the cove control socket and guest agent, appends `tool_result` blocks,
and repeats until Claude returns no more tool calls.

## Install

```sh
python3.12 -m venv /tmp/cove-claude-sandbox-venv
/tmp/cove-claude-sandbox-venv/bin/python -m pip install -e adapters/anthropic-sandbox-python[dev]
/tmp/cove-claude-sandbox-venv/bin/python -m pytest adapters/anthropic-sandbox-python/tests
```

## First-frame smoke

This smoke does not call the Anthropic API. It verifies fork/restore, the cove
control socket, and screenshot capture against a fresh child VM.

```sh
export COVE_PARENT_VM=macos-base
export COVE_CHILD_VM=claude-sandbox-smoke
export COVE_BIN=./cove

python3 adapters/anthropic-sandbox-python/examples/computer_use.py --first-frame-only
```

The command writes `/tmp/cove-claude-first-frame.png` and then stops the child
VM.

## Computer-use run

```python
from cove_claude_sandbox import AnthropicSandbox

sandbox = AnthropicSandbox.from_fork(
    parent="macos-base",
    child="claude-run-001",
    cove_bin="./cove",
)
try:
    result = sandbox.run(
        "Open Safari and take a screenshot of the cove documentation.",
        model="claude-opus-4-7",
        max_iterations=20,
    )
    print(result.final_text)
finally:
    sandbox.stop()
```

The loop exposes Anthropic-defined tools:

- `computer_20251124` named `computer`
- `bash_20250124` named `bash`
- `text_editor_20250728` named `str_replace_based_edit_tool`

Tool failures are returned to Claude as `tool_result` blocks with
`is_error: true`. Fork failures and max-iteration exhaustion are raised to the
caller.
