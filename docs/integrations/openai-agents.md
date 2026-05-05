# OpenAI Agents SDK Integration

**Status:** Phase 2 shipped
**Date:** 2026-05-05

`cove-sandbox` is the local OpenAI Agents SDK adapter for cove VMs.

Phase 1 covers `ComputerTool` against an already-running VM. Phase 2 adds the
`SandboxRunConfig` backend so `SandboxAgent` runs can create or resume a cove
sandbox at run time, run shell and file operations inside it, and clean it up
after the run.

The adapter is local and VM-backed:

- fresh sessions fork a cove VM
- resumed sessions reattach when the VM still exists
- workspaces live inside the sandbox root
- cleanup stops or deletes the child VM according to the options

Use the Python package directly:

```python
from cove_sandbox import sandbox_run_config
```

The package README and the example under
`adapters/openai-agents-python/examples/sandbox_runner.py` show the full run
shape.

The supported public surface is intentionally small:

- `CoveSandbox` for `ComputerTool`
- `sandbox_run_config(...)` for `SandboxAgent`
- `CoveSandboxClient` and `CoveSandboxClientOptions` for callers that want the
  lower-level client/session API

The adapter does not introduce a hosted sandbox provider or a registry-backed
transport.
