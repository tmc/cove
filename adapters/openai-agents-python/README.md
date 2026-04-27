# cove-sandbox

`cove-sandbox` is the first OpenAI Agents SDK adapter for cove. It lets an
Agents SDK `ComputerTool` drive a local Apple-Silicon macOS VM through cove's
control socket.

This is a local adapter. It does not send VM state, screenshots, files, or
commands to a hosted sandbox provider.

## Install

From this repository:

```bash
python -m pip install -e adapters/openai-agents-python[agents]
```

## ComputerTool

```python
from agents import Agent, ComputerTool, Runner
from cove_sandbox import CoveSandbox

sandbox = CoveSandbox(vm="macos-eval")

agent = Agent(
    name="macOS operator",
    instructions="Use the macOS VM to inspect the app.",
    tools=[ComputerTool(sandbox.computer())],
)

result = Runner.run_sync(agent, "Open Safari and report the visible page title.")
print(result.final_output)
```

The VM must already be running in GUI mode:

```bash
cove -vm macos-eval run -gui
cove ctl -vm macos-eval agent-ping -wait 120s
```

## Fork Per Run

For privacy-sensitive evals, fork a disposable VM and stop it when done:

```python
from cove_sandbox import CoveSandbox

with CoveSandbox.from_fork(parent="macos-base", name="eval-001") as sandbox:
    sandbox.start(gui=True)
    sandbox.wait_ready(timeout=120)
    print(sandbox.exec("sw_vers").stdout)
```

`from_fork` calls `cove fork <parent> <name>`. Cleanup is explicit because VM
deletion is a product decision; the context manager stops the guest but does not
delete the VM bundle.

## Shell Helpers

```python
from cove_sandbox import CoveSandbox

sandbox = CoveSandbox(vm="macos-eval")
run = sandbox.exec(["/bin/zsh", "-lc", "uname -a"])
print(run.stdout)
```

The adapter uses the guest agent for shell execution and the control socket for
GUI actions.
