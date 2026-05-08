# Design 035: OpenAI SandboxRunConfig backend for cove

**Status:** Shipped; helper, backend, tests, example, and integration docs all
landed (`36552c2`, `4d61edd`, `27f9e24`)  
**Author:** Travis Cline  
**Date:** 2026-05-08

## Shipped Slices

- Design doc: `36552c2`.
- `cove-sandbox` Python helper plus `CoveSandboxClient` /
  `CoveSandboxClientOptions` / `sandbox_run_config()` re-exports: `4d61edd`.
- Backend wiring (`backend.py`), tests
  (`tests/test_sandbox_run_config.py`), runnable example
  (`examples/sandbox_runner.py`), README, and `docs/integrations/openai-agents.md`
  walkthrough: `27f9e24`.
- ROADMAP row marked `done` against `36552c2`, `4d61edd`, `27f9e24`.

The MVP from the section below is satisfied: a documented helper, a working
local VM-backed backend, a runnable example, and integration docs.

## Problem

Phase 1 of the cove OpenAI Agents SDK adapter covered `ComputerTool`: an agent
could point at a running cove VM and use the control socket as a local
desktop-computer runtime.

Phase 2 adds the shell-and-file backend for `SandboxAgent` workflows. The SDK
already models this as `SandboxRunConfig`, `SandboxAgent`, and a client/session
pair that creates or resumes a sandbox at run time. cove should fit that shape
without inventing a second agent API.

The user-facing goal is simple: keep the normal `Runner.run()` flow, but let the
runner create a cove-backed VM sandbox for the run, execute shell and file
operations inside that sandbox, and clean it up when the run ends.

## Decision Summary

- Keep the public Python adapter local-first and VM-backed.
- Expose a cove `SandboxRunConfig` helper that returns the SDK `RunConfig`
  wiring for a cove client/session backend.
- Treat the cove VM as the sandbox identity. A forked VM name is the session
  identity the SDK can create, resume, and delete.
- Model each run as:
  `Runner.run()` → cove fork or attach → start sandbox session → execute tools
  in the sandbox view → persist workspace state if needed → stop or delete.
- Use `cove run -fork-from <ref> -ephemeral` as the default fresh-session
  primitive for short-lived runs.
- Keep the adapter local. It should not require a hosted sandbox provider or a
  registry.

## Existing Ground

Design 021 and the shipped `cove-sandbox` package already established the
computer-use side of the adapter:

- `CoveSandbox` can attach to an existing VM or fork a disposable child.
- `CoveClient` already speaks the control socket for agent exec, read/write,
  screenshot, key, mouse, and stop.
- The package already has a `SandboxRunConfig` backend surface in the current
  implementation, but it is not yet documented as the supported phase-2 shape.

The OpenAI Agents SDK docs define the sandbox runtime contract as a client +
options + optional live session. That is the shape this design follows. The
runner owns creation and cleanup when a client is supplied; the caller may also
provide a live session or a serialized session state.

## Lifecycle Shape

Fresh session:

1. `Runner.run()` receives a `SandboxRunConfig`.
2. The cove client forks or attaches to a VM.
3. The sandbox starts, waits for `agent-ping`, and exposes a workspace root.
4. The agent runs shell/file actions through the guest agent and control socket.
5. When the run finishes, the adapter stops the VM and optionally deletes the
   child if the options say it owns the session.

Resume session:

1. The SDK hands the client a persisted sandbox session state.
2. cove reattaches to the same VM if it still exists.
3. If the original VM is gone, the client may create a replacement from the
   saved state.
4. The session continues with the same workspace identity.

The important contract is that the agent code does not know whether the sandbox
is backed by an existing VM, a fresh fork, or a resumed session. The backend
handles that decision.

## API Surface

Public Python API:

```python
from cove_sandbox import CoveSandboxClient, CoveSandboxClientOptions
from cove_sandbox import sandbox_run_config
```

Helper shape:

```python
run_config = sandbox_run_config(
    parent="macos-base",
    name="eval-001",
    gui=False,
    delete_on_close=True,
)
```

The helper should return a normal SDK `RunConfig` whose `sandbox=` member is a
`SandboxRunConfig` bound to a cove client instance.

Supported inputs:

- `vm` to attach to an existing VM
- `parent` plus optional `name` to fork a fresh child
- `gui`, `delete_on_close`, `stop_on_close`, `start`, `wait_ready_timeout`
- `socket_path` and `token` for explicit control-socket overrides
- `workspace_root` when the caller wants a stable sandbox root path

The adapter should keep the current `CoveSandbox` convenience wrapper for
computer-use workflows. SandboxAgent workflows should use the sandbox client
and the new helper.

## Workspace Contract

The sandbox workspace is a stable root directory inside the guest. The SDK
filesystem API maps onto cove control-socket read/write and exec operations.

The adapter should:

- treat the workspace root as session-local state
- persist workspace contents on close when the SDK asks it to
- hydrate a resumed session from the stored workspace snapshot if the backend
  VM cannot be reused directly
- keep workspace files inside the sandbox root, not in a repo-global temp dir

The root directory name should remain deterministic and inspectable so users can
debug it from the guest shell.

## Session Identity

The VM name is the stable sandbox identity visible to users. If the caller
passes `parent`, the adapter should fork a new child name and own that child for
the life of the sandbox session.

The returned session state must be serializable by the SDK and contain enough
information to reattach or recreate the sandbox. The state should include:

- VM name
- control socket path or enough information to derive it
- workspace root
- ownership / delete-on-close flags
- any backend-specific snapshot data the SDK needs for resume

## Failure Model

Errors should be plain and local:

- missing VM name or parent: usage error
- failed fork: backend creation error
- guest agent unavailable: start/wait failure
- shell command failure: exec failure with stderr surfaced
- delete failure: cleanup warning unless the caller asked for strict cleanup

The adapter should not hide VM lifecycle failures behind generic SDK failures.
When cove is the source of the error, the message should name cove and the VM.

## File-Level Shape

Expected implementation surface:

- `docs/designs/035-openai-sandbox-run-config.md`
- `adapters/openai-agents-python/src/cove_sandbox/sandbox_run_config.py`
- `adapters/openai-agents-python/src/cove_sandbox/__init__.py`
- `adapters/openai-agents-python/tests/test_sandbox_run_config.py`
- `adapters/openai-agents-python/examples/sandbox_runner.py`
- `adapters/openai-agents-python/README.md`
- `docs/examples/openai-agents.md`
- `docs/integrations/openai-agents.md`
- optional `agent-sandbox` snippet hint if the OpenAI provider path needs a
  copy-paste bridge

## Test Plan

Unit tests should cover:

1. helper returns an SDK `RunConfig` object when the SDK is available
2. helper passes through `vm` and `parent` inputs correctly
3. client options round-trip without the SDK installed
4. session state serializes and deserializes with cove-specific fields
5. example honors `COVE_DRY_RUN=1`
6. docs/examples stay consistent with the actual package exports

## Non-Goals

- No hosted OpenAI Agents transport.
- No new agent loop in cove.
- No shared registry or remote sandbox provider.
- No attempt to make `SandboxRunConfig` a new cove-specific SDK type.
- No change to the existing `ComputerTool` adapter behavior in this slice.

## MVP

The minimum useful slice is:

- a documented `sandbox_run_config()` helper
- a working cove client/session backend for `SandboxAgent`
- a runnable example
- docs that say the backend is local and VM-backed

That is enough for a user to write:

```python
result = await Runner.run(agent, task, run_config=sandbox_run_config(parent="macos-base", name="eval-001"))
```

and get a cove-backed sandbox run without manually constructing the backend
plumbing each time.

## Cross-references

- [`docs/examples/openai-agents.md`](../examples/openai-agents.md) for the
  end-user walkthrough.
- [`docs/integrations/openai-agents.md`](../integrations/openai-agents.md) for
  the shipped integration summary.
- [`adapters/openai-agents-python/README.md`](../../adapters/openai-agents-python/README.md)
  for the package-level README that ships with the adapter.
