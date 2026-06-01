# cove-sandbox

`cove-sandbox` is the OpenAI Agents SDK adapter for cove. It lets an Agents SDK
`ComputerTool` drive Apple-Silicon macOS VMs through cove's local control socket
or a private `cove-fleetd` control plane, and it exposes a `SandboxRunConfig`
helper for `SandboxAgent` workflows.

`SandboxRunConfig` can run locally through the VM control socket or remotely
through a control-plane sandbox with `provider="cloud"`.

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

## Live Smoke

Run unit coverage first:

```bash
python -m pytest adapters/openai-agents-python/tests
```

With Python 3.10 or newer, check packaging without publishing:

```bash
tmp=$(mktemp -d)
python -m pip wheel adapters/openai-agents-python -w "$tmp"
ls "$tmp"/cove_sandbox-*.whl
```

Then run a live fork-backed smoke against a stopped parent VM:

```bash
python -m pip install -e adapters/openai-agents-python[agents]
go build -o cove ./cmd/cove
codesign -s - -f --entitlements internal/autosign/vz.entitlements ./cove
COVE_PARENT_VM=macos-base \
COVE_CHILD_VM=openai-agents-smoke-$(date +%s) \
COVE_BIN=./cove \
python adapters/openai-agents-python/examples/computer_tool.py
```

The smoke uses `CoveSandbox.from_fork`, boots the child in GUI mode, waits for
the guest agent, and then runs an Agents SDK `ComputerTool` request. Delete the
child VM after inspecting logs or screenshots you want to keep.

## Shell Helpers

```python
from cove_sandbox import CoveSandbox

sandbox = CoveSandbox(vm="macos-eval")
run = sandbox.exec(["/bin/zsh", "-lc", "uname -a"])
print(run.stdout)
```

The adapter uses the guest agent for shell execution and the control socket for
GUI actions.

## SandboxRunConfig

The v2 backend implements the Agents SDK sandbox client/session interfaces, so
`SandboxAgent` can create or resume a cove-backed sandbox through
`RunConfig(sandbox=SandboxRunConfig(...))`.

```python
from agents import RunConfig, Runner
from agents.sandbox import SandboxAgent, SandboxRunConfig
from cove_sandbox import CoveSandboxClient, CoveSandboxClientOptions

agent = SandboxAgent(
    name="macOS workspace",
    instructions="Inspect the workspace and report concise results.",
)

run_config = RunConfig(
    sandbox=SandboxRunConfig(
        client=CoveSandboxClient(),
        options=CoveSandboxClientOptions(
            parent="macos-base",
            name="eval-001",
            gui=False,
            delete_on_close=True,
        ),
    )
)

result = await Runner.run(agent, "Run sw_vers in the sandbox.", run_config=run_config)
```

`parent` forks a fresh VM with `cove fork`. Use `vm=` instead when you want to
attach to an existing VM. The backend maps SDK `exec`, `read`, `write`, and
workspace persistence calls onto the cove control socket and guest agent.

For a hosted/control-plane sandbox, switch only the provider options:

```python
run_config = RunConfig(
    sandbox=SandboxRunConfig(
        client=CoveSandboxClient(),
        options=CoveSandboxClientOptions(
            provider="cloud",
            fleet_url="https://fleet.internal.example",
            api_key="cove_...",
            image_ref="macos-base:latest",
            manifest_bundle="manifests",
            image_platform="darwin/arm64",
            required_capabilities=("ram-overlay",),
            name="eval-001",
            delete_on_close=True,
        ),
    )
)
```

The cloud provider creates `POST /v1/sandboxes` handles, polls until the sandbox
is `ready`, maps SDK `exec`/workspace file calls onto
`POST /v1/sandboxes/{id}/exec`, maps `ComputerTool`
screenshot/key/text/mouse calls onto `POST /v1/sandboxes/{id}/control`, and
exposes sandbox audit history through `GET /v1/sandboxes/{id}/events` and
worker reports through `GET /v1/sandboxes/{id}/reports`. It deletes the sandbox
handle on close when `delete_on_close=True`. Set
`COVE_FLEET_URL` and `COVE_API_KEY` (or `COVE_FLEET_TOKEN`) instead of passing
`fleet_url` and `api_key` directly. For registry-audited hosted sandboxes, pass
`manifest_bundle` plus optional `image_platform`; the controller verifies the
offline bundle and admits the sandbox only on workers with matching image
provenance. Pass `required_capabilities` to restrict hosted placement to
workers that advertise runtime traits such as `ram-overlay`.

For direct fleet-client code, `CoveFleetClient` covers the hosted lifecycle:

```python
from cove_sandbox import CoveFleetClient

client = CoveFleetClient.create_sandbox(
    fleet_url="https://fleet.internal.example",
    api_key="cove_...",
    namespace="team-a",
    image_ref="macos-base:latest",
    manifest_bundle="manifests",
    image_platform="darwin/arm64",
    required_capabilities=("ram-overlay",),
    sandbox_id="eval-001",
)
ready = client.list_page(status="ready", image_ref="macos-base:latest", offset=0, limit=10)
print(ready.get("next_offset"))
lease = client.lease(holder="runner-42", ttl=30)
client.wait_ready(timeout=120)
print(client.exec("sw_vers").stdout)
print(client.metering()["summary"]["records"])
print(client.events(action="sandbox.exec", limit=20)["count"])
print(client.reports(role="exec", limit=20)["count"])
client.release_lease(holder=lease["lease"]["holder"])
client.delete_vm()
```

For a copy-paste helper that returns the SDK `RunConfig` wrapper directly,
import `sandbox_run_config` from `cove_sandbox`.
