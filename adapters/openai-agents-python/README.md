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
provenance. Pass `required_labels` for operator-defined selectors and
`required_capabilities` to restrict hosted placement to workers that advertise
runtime traits such as `ram-overlay`. The direct fleet client can also queue
image preparation before placement or warm-pool replenishment, push image
GC/lifecycle/storage maintenance work, read retained placement-plan history and
the retained controller-run timeline, plan or apply controller reconciliation,
inspect or verify the hash-chained audit feed, manage scoped service-account
tokens and OIDC/SAML identity bindings, and drill into worker- or
assignment-scoped events, reports, metering, and hosted sandbox lists.

For direct fleet-client code, `CoveFleetClient` covers hosted sandbox and
warm-pool lifecycles plus maintenance controls:

```python
from cove_sandbox import CoveFleetClient

prepare = CoveFleetClient.prepare_image(
    fleet_url="https://fleet.internal.example",
    api_key="cove_...",
    namespace="team-a",
    image_ref="macos-base:latest",
    manifest_bundle="manifests",
    image_platform="darwin/arm64",
    required_labels={"zone": "desk"},
    required_capabilities=("ram-overlay",),
    dry_run=True,
)
print(len(prepare["assignments"]), len(prepare["skipped"]))

prune = CoveFleetClient.push_storage_prune(
    fleet_url="https://fleet.internal.example",
    api_key="cove_...",
    namespace="team-a",
    required_labels={"zone": "desk"},
    required_capabilities=("ram-overlay",),
    category="build-scratch",
    older_than="168h",
    apply=True,
    dry_run=True,
)
print(len(prune["assignments"]), len(prune["skipped"]))

runs = CoveFleetClient.list_controller_runs(
    fleet_url="https://fleet.internal.example",
    api_key="cove_...",
    namespace="team-a",
    kind="storage.prune",
    target_type="storage",
    limit=20,
)
print(runs["count"])

plan = CoveFleetClient.plan_sandbox(
    fleet_url="https://fleet.internal.example",
    api_key="cove_...",
    namespace="team-a",
    image_ref="macos-base:latest",
    manifest_bundle="manifests",
    image_platform="darwin/arm64",
    required_labels={"zone": "desk"},
    required_capabilities=("ram-overlay",),
    limit=5,
)
print(len(plan["candidates"]), len(plan["skipped"]))

plans = CoveFleetClient.list_placement_plans(
    fleet_url="https://fleet.internal.example",
    api_key="cove_...",
    namespace="team-a",
    policy="image-affinity",
    image_ref="macos-base:latest",
    limit=20,
)
print(plans["count"])

plan_history = CoveFleetClient.get_placement_plan(
    fleet_url="https://fleet.internal.example",
    api_key="cove_...",
    plan_id="placement-plan-123",
)
print(len(plan_history["candidates"]), len(plan_history["skipped"]))

pool = CoveFleetClient.ensure_warm_pool(
    fleet_url="https://fleet.internal.example",
    api_key="cove_...",
    namespace="team-a",
    name="runner-14",
    image_ref="macos-base:latest",
    manifest_bundle="manifests",
    image_platform="darwin/arm64",
    size=3,
    required_labels={"zone": "desk"},
    required_capabilities=("ram-overlay",),
    resources={"vms": 1},
)
print(pool["pool"]["ready"], pool["pool"]["active"])
summary = CoveFleetClient.get_operations_summary(
    fleet_url="https://fleet.internal.example",
    api_key="cove_...",
    namespace="team-a",
)
print(summary["workers"]["ready"], summary["sandboxes"]["active"])
reconcile_plan = CoveFleetClient.plan_reconcile(
    fleet_url="https://fleet.internal.example",
    api_key="cove_...",
)
reconciled = CoveFleetClient.reconcile(
    fleet_url="https://fleet.internal.example",
    api_key="cove_...",
)
print(len(reconcile_plan["requeued_assignments"]), len(reconciled["warm_pool_cleanup"]))
audit = CoveFleetClient.list_audit_events(
    fleet_url="https://fleet.internal.example",
    api_key="cove_...",
    namespace="team-a",
    action="assignment.create",
    limit=20,
)
verify = CoveFleetClient.verify_audit_log(
    fleet_url="https://fleet.internal.example",
    api_key="cove_...",
)
print(audit["count"], verify["ok"])
accounts = CoveFleetClient.list_service_accounts(
    fleet_url="https://fleet.internal.example",
    api_key="cove_...",
    namespace="team-a",
)
rotated = CoveFleetClient.upsert_service_account(
    fleet_url="https://fleet.internal.example",
    api_key="cove_...",
    namespace="team-a",
    name="ci",
    role="operator",
    token="cove_ci_...",
)
print(accounts["count"], rotated["service_account"]["name"])
oidc = CoveFleetClient.upsert_oidc_binding(
    fleet_url="https://fleet.internal.example",
    api_key="cove_...",
    namespace="team-a",
    name="github-main",
    issuer="https://token.actions.githubusercontent.com",
    subject="repo:tmc/cove:ref:refs/heads/main",
    audience="cove-fleet",
    role="operator",
    jwks_url="https://token.actions.githubusercontent.com/.well-known/jwks",
)
saml_login = CoveFleetClient.saml_binding_login(
    fleet_url="https://fleet.internal.example",
    api_key="cove_...",
    name="okta",
    relay_state="cli",
)
saml_metadata = CoveFleetClient.get_saml_metadata(
    fleet_url="https://fleet.internal.example",
    api_key="cove_...",
    name="okta",
)
print(oidc["binding"]["name"], saml_login["redirect_url"], len(saml_metadata))
workers = CoveFleetClient.list_workers(
    fleet_url="https://fleet.internal.example",
    api_key="cove_...",
    status="ready",
    image_ref="macos-base:latest",
    source_manifest_digest="sha256:...",
    labels={"zone": "desk"},
    capabilities=("ram-overlay",),
    limit=20,
)
print(workers["count"])
assignments = CoveFleetClient.list_assignments(
    fleet_url="https://fleet.internal.example",
    api_key="cove_...",
    namespace="team-a",
    status="running",
    worker_id="mini-1",
    limit=20,
)
print(assignments["count"])
worker_events = CoveFleetClient.list_worker_events(
    fleet_url="https://fleet.internal.example",
    api_key="cove_...",
    worker_id="mini-1",
    limit=20,
)
assignment_reports = CoveFleetClient.list_assignment_reports(
    fleet_url="https://fleet.internal.example",
    api_key="cove_...",
    assignment_id="assignment-123",
    limit=20,
)
print(worker_events["count"], assignment_reports["count"])
retry = CoveFleetClient.retry_assignment(
    fleet_url="https://fleet.internal.example",
    api_key="cove_...",
    assignment_id="assignment-123",
    reason="transient host failure",
    replan=True,
)
print(retry["assignment"]["id"], retry["assignment"].get("worker_id"))
evacuation = CoveFleetClient.evacuate_worker(
    fleet_url="https://fleet.internal.example",
    api_key="cove_...",
    worker_id="mini-1",
    reason="maintenance",
)
print(len(evacuation["assignments"]), len(evacuation["blocked"]))
drain = CoveFleetClient.drain_worker(
    fleet_url="https://fleet.internal.example",
    api_key="cove_...",
    worker_id="mini-1",
    reason="maintenance",
)
print(len(drain["sandboxes"]), len(drain["skipped"]))
claim = CoveFleetClient.claim_warm_pool(
    fleet_url="https://fleet.internal.example",
    api_key="cove_...",
    namespace="team-a",
    name="runner-14",
    command=("/bin/sh", "-lc", "make test"),
)
print(claim["vm_name"], claim["assignment"]["worker_id"])

client = CoveFleetClient.create_sandbox(
    fleet_url="https://fleet.internal.example",
    api_key="cove_...",
    namespace="team-a",
    image_ref="macos-base:latest",
    manifest_bundle="manifests",
    image_platform="darwin/arm64",
    required_labels={"zone": "desk"},
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

Maintenance helpers include `push_image_gc`, `push_lifecycle_policy`,
`push_storage_budget`, `push_storage_prune`, the matching `list_*_runs` /
`get_*_run` methods, `plan_sandbox`, `list_placement_plans`,
`get_placement_plan`, `get_operations_summary`, `plan_reconcile`, `reconcile`,
`list_workers`, `get_worker`,
`list_assignments`, `get_assignment`, `cancel_assignment`, `retry_assignment`,
worker lifecycle helpers such as `cordon_worker`, `evacuate_worker`,
`drain_worker`, and `decommission_worker`, `list_audit_events`,
`verify_audit_log`, `list_service_accounts`, `upsert_service_account`,
`delete_service_account`, `list_oidc_bindings`, `upsert_oidc_binding`,
`delete_oidc_binding`, `list_saml_bindings`, `upsert_saml_binding`,
`refresh_saml_binding`, `get_saml_metadata`, `saml_binding_login`,
`create_saml_session`, and `delete_saml_binding`, scoped observability helpers such as
`list_worker_sandboxes`, `list_worker_events`, `list_worker_reports`,
`get_worker_metering`, `list_assignment_events`, `list_assignment_reports`,
and `get_assignment_metering`, and `list_controller_runs` for the aggregate
operations dashboard, inventory, maintenance controls, audit chain, and
timeline.
Pass `dry_run=True` to maintenance pushes to inspect planned
assignments and structured skipped-worker diagnostics without mutating the
controller.

For a copy-paste helper that returns the SDK `RunConfig` wrapper directly,
import `sandbox_run_config` from `cove_sandbox`.
