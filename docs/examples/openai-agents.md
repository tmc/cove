---
title: OpenAI Agents SDK
---
# OpenAI Agents SDK

Use `cove-sandbox` when an Agents SDK run needs a real local macOS guest instead
of a hosted or Linux sandbox. The adapter exposes cove as a local `ComputerTool`
runtime and keeps control on the host you own.

## Install

```bash
python -m pip install -e adapters/openai-agents-python[agents]
```

## Run

Start a GUI VM:

```bash
cove -vm macos-eval run -gui
cove ctl -vm macos-eval agent-ping -wait 120s
```

Drive it from an Agents SDK computer tool:

```python
from agents import Agent, ComputerTool, Runner
from cove_sandbox import CoveSandbox

sandbox = CoveSandbox(vm="macos-eval")

agent = Agent(
    name="macOS operator",
    instructions="Use the macOS VM and report concise observations.",
    tools=[ComputerTool(sandbox.computer())],
)

result = Runner.run_sync(agent, "What is visible on the VM desktop?")
print(result.final_output)
```

For privacy-sensitive evals, fork a disposable VM per run:

```python
from cove_sandbox import CoveSandbox

with CoveSandbox.from_fork(parent="macos-base", name="eval-001") as sandbox:
    sandbox.start(gui=True)
    sandbox.wait_ready(timeout=120)
    print(sandbox.exec("sw_vers").stdout)
```

The context manager stops the guest. It does not delete the VM bundle.
