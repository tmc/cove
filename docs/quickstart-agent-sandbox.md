---
title: Agent Sandbox Quickstart
---
# Agent Sandbox Quickstart

cove can run a real macOS desktop as a local computer-use sandbox. The model
sees screenshots; cove sends mouse, keyboard, text, OCR, and guest-agent exec
through the VM control socket. For isolated jobs, start each run from a stopped
base VM or local image and throw the child away.

## 5-minute path

Install cove, create a base VM, and verify the control plane:

```bash
go install github.com/tmc/cove@latest
cove up -vm macos-base -user agent
cove ctl -vm macos-base -wait 120s agent-ping
cove ctl -vm macos-base screenshot -o /tmp/macos-base.png
cove ctl -vm macos-base ocr
cove -vm macos-base ctl text "hello from cove"
```

Stop the base before using it as a fork source:

```bash
cove -vm macos-base ctl stop
```

For a clean VM per task:

```bash
cove fork macos-base task-001
cove run -vm task-001 -gui
```

Delete the child when the task is done, or keep it around for inspection. For
image-backed sandboxes, build once and run disposable children:

```bash
cove image build -from macos-base -tag macos-agent:latest
cove run -fork-from macos-agent:latest -ephemeral -gui
```

To run a provider loop with a fresh fork and replay bundle in one command:

```bash
export ANTHROPIC_API_KEY=...
cove agent-sandbox run \
  --provider anthropic \
  --image macos-agent:latest \
  --task "Take a screenshot of the desktop and describe what is visible."
```

See [Agent Sandbox CLI](features/agent-sandbox-cli.md) for Anthropic, Gemini,
Vertex AI, and OpenAI provider examples.

## OpenAI Computer Use

Install the OpenAI Agents SDK adapter from this repository:

```bash
python -m pip install -e adapters/openai-agents-python[agents]
```

Run a fork-backed `ComputerTool` task:

```python
import os

from agents import Agent, ComputerTool, Runner
from cove_sandbox import CoveSandbox

parent = os.environ.get("COVE_PARENT_VM", "macos-base")
child = os.environ.get("COVE_CHILD_VM", "openai-agent-001")

with CoveSandbox.from_fork(parent=parent, name=child) as sandbox:
    sandbox.start(gui=True)
    sandbox.wait_ready(timeout=120)

    agent = Agent(
        name="macOS operator",
        instructions="Use the VM desktop. Keep the final answer concise.",
        tools=[ComputerTool(sandbox.computer())],
    )
    result = Runner.run_sync(agent, "What is visible on the desktop?")
    print(result.final_output)
```

For shell/file workflows, the adapter also implements an Agents SDK
`SandboxRunConfig` backend:

```python
import asyncio

from agents import RunConfig, Runner
from agents.sandbox import SandboxAgent, SandboxRunConfig
from cove_sandbox import CoveSandboxClient, CoveSandboxClientOptions


async def main():
    agent = SandboxAgent(
        name="macOS workspace",
        instructions="Use the cove-backed VM for shell and file work.",
    )
    run_config = RunConfig(
        sandbox=SandboxRunConfig(
            client=CoveSandboxClient(),
            options=CoveSandboxClientOptions(
                parent="macos-base",
                name="openai-workspace-001",
                gui=False,
                delete_on_close=True,
            ),
        )
    )
    result = await Runner.run(agent, "Run sw_vers and summarize it.", run_config=run_config)
    print(result.final_output)


asyncio.run(main())
```

See [OpenAI Agents SDK](examples/openai-agents.md) for the longer walkthrough.

## Anthropic Claude Computer Use

The packaged Anthropic adapter is `cove-claude-sandbox`. It owns the Messages
API loop, executes Claude `tool_use` blocks against cove, and sends screenshots
back as `tool_result` images.

```bash
python3.12 -m venv /tmp/cove-claude-sandbox-venv
/tmp/cove-claude-sandbox-venv/bin/python -m pip install -e adapters/anthropic-sandbox-python
export ANTHROPIC_API_KEY=...
```

Run Claude against a fork:

```python
from cove_claude_sandbox import AnthropicSandbox

sandbox = AnthropicSandbox.from_fork(
    parent="macos-base",
    child="claude-agent-001",
    cove_bin="cove",
)
try:
    result = sandbox.run(
        "Take a screenshot of the desktop and describe what is visible.",
        model="claude-opus-4-7",
        max_iterations=20,
    )
    print(result.final_text)
finally:
    sandbox.stop()
```

For a no-API smoke that only verifies fork, boot, and screenshot capture:

```bash
export COVE_PARENT_VM=macos-base
export COVE_CHILD_VM=claude-first-frame
python3 adapters/anthropic-sandbox-python/examples/computer_use.py --first-frame-only
open /tmp/cove-claude-first-frame.png
```

See [Anthropic Computer Use](examples/anthropic-computer-use.md) for the
substrate bridge and adapter limits.

## Gemini Computer Use

The Gemini bridge is a thin helper for the `computer_use` tool. It drives an
already-running GUI VM through the control socket.

```bash
python -m pip install httpx
export GEMINI_API_KEY=...

cove fork macos-base gemini-agent
cove run -vm gemini-agent -gui
cove ctl -vm gemini-agent -wait 120s agent-ping

python3 adapters/google-bridge/computer_use.py \
  --vm gemini-agent \
  --task "Open Safari and read the visible page title."
```

Gemini currently returns browser-shaped computer-use actions. The bridge maps
clicks, typing, keys, and screenshots onto cove; unsupported browser actions are
reported as no-ops with a refreshed screenshot. See
[Gemini Computer Use](examples/gemini-computer-use.md).

## Fork Isolation Pattern

Use a stopped base VM for repeatable desktop state:

```bash
cove -vm macos-base ctl stop
cove fork macos-base task-debug-001
cove -vm task-debug-001 run -gui
```

Use `cove fork` for short jobs from a stopped VM parent:

```bash
child=task-$(date +%s)
cove fork macos-base "$child"
cove run -vm "$child" -gui
```

Use image-backed `-ephemeral` for sandbox-per-task CI or evals:

```bash
cove image build -from macos-base -tag macos-agent:latest
cove run -fork-from macos-agent:latest -ephemeral -headless
```

VM parents must be stopped before `cove fork` or `cove clone --linked`.
`cove run -fork-from` takes a local image ref such as `macos-agent:latest`;
VM-parent RAM-overlay forks are not implemented. Keep secrets and untrusted
state inside the child; discard the child after each task.

## Per-run Artifacts

`cove run -fork-from` creates a lazy bundle under `~/.vz/runs/<run-id>/` for
short-lived runs:

```text
manifest.json
events.jsonl
stdout.log
stderr.log
screenshots/
```

The event log records control-socket activity such as screenshots, text, keys,
mouse events, and agent calls. Screenshots are copied into `screenshots/` when
the run records them. Future metrics work tracks boot time, agent-ready time,
resource use, and exit status alongside the existing manifest and event stream.

Inspect the last run:

```bash
run=$(ls -td ~/.vz/runs/* | head -1)
jq . "$run/manifest.json"
tail -20 "$run/events.jsonl"
ls "$run/screenshots"
```

## Useful Control Primitives

The adapters all build on the same local VM operations:

```bash
cove -vm macos-base ctl screenshot -o screen.png
cove -vm macos-base ctl ocr
cove -vm macos-base ctl click-text -timeout 30s "Continue"
cove -vm macos-base ctl text "typed by cove"
cove -vm macos-base ctl key 36 down
cove -vm macos-base ctl key 36 up
cove -vm macos-base ctl agent-exec sw_vers
```

These are local Unix-socket and vsock calls. cove does not require SSH for the
agent path, and the VM display does not need to share your host cursor.
