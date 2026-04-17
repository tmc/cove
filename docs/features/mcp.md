---
title: MCP (Model Context Protocol)
---
# MCP (Model Context Protocol)

`cove serve --mcp` speaks the Model Context Protocol over stdio, so AI agents like Claude Code, Cursor, and Cline can list, boot, screenshot, and drive VMs as tools.

```bash
cove serve --mcp
```

The command reads MCP framing from stdin and writes MCP framing to stdout. All logs go to stderr -- stdout is reserved for the protocol, and any byte written there that isn't MCP framing breaks the client.

## What MCP is

MCP is a stdio-based JSON-RPC protocol that lets an LLM agent call tools a host program exposes, safely and with typed arguments. The agent sees a tool list with JSON schemas; each turn it can invoke a tool, receive structured output, and continue. Cove exposes VM control as an MCP tool surface, so an agent can work on a macOS or Linux VM without the user having to copy commands between chat and terminal.

The same underlying dispatch that powers the [HTTP API](../reference/http-api.md) backs MCP -- the two transports are thin shells over one command handler.

## Configuring Claude Code

Add cove to your Claude Code `settings.json`:

```json
{
  "mcpServers": {
    "cove": {
      "command": "cove",
      "args": ["serve", "--mcp"]
    }
  }
}
```

Cursor and Cline follow the same pattern with their own settings files; any MCP client that launches a subprocess and speaks MCP over stdio works.

## Tools

Each tool takes a JSON object matching the `input` schema and returns a JSON object matching the `output` schema.

### list_vms

List all VMs and their current states.

- **Input**: `{}`
- **Output**: `{"vms": [{"name": string, "state": string, "cpu": int, "memory_gb": number}]}`

### run_vm

Boot a VM. Returns when the VM is running (or fails).

- **Input**: `{"name": string, "options": {"headless": bool, "gui": bool, "no_resume": bool}}`
- **Output**: `{"name": string, "state": "running"}`

### stop_vm

Request graceful shutdown. If the agent is available the guest is asked to shut down; otherwise a stop is requested via the framework.

- **Input**: `{"name": string}`
- **Output**: `{"name": string, "state": "stopped"}`

### screenshot

Capture the VM display. Returned as a base64 PNG so the agent can include it in its context.

- **Input**: `{"name": string, "scale": number}`
- **Output**: `{"name": string, "format": "png", "data": "<base64>"}`

### exec

Run a command in the guest via the guest agent.

- **Input**: `{"name": string, "cmd": string, "args": [string], "as_user": bool}`
- **Output**: `{"exit_code": int, "stdout": string, "stderr": string}`

`as_user: false` (default) routes through the root daemon agent on port 1024; `as_user: true` routes through the user session agent, which has TCC/FDA access from a logged-in GUI session.

### agent_read

Read a file from the guest.

- **Input**: `{"name": string, "path": string}`
- **Output**: `{"path": string, "data": "<base64>"}`

### agent_write

Write a file to the guest.

- **Input**: `{"name": string, "path": string, "data": "<base64>", "mode": int}`
- **Output**: `{"path": string, "bytes_written": int}`

### status

VM state and capabilities.

- **Input**: `{"name": string}`
- **Output**: `{"state": string, "can_pause": bool, "can_resume": bool, "can_stop": bool}`

### vzscript_run

Run a built-in or custom [vzscript](vzscript.md) recipe inside the VM. Blocks until the recipe finishes; returns the recipe's aggregated output.

- **Input**: `{"name": string, "recipe": string}`
- **Output**: `{"recipe": string, "exit_code": int, "log": string}`

## Safety notes

- **Stdout is MCP only.** All cove logging, verbose output, and diagnostics go to stderr. This is enforced in MCP mode -- printing to stdout from anywhere in the process would corrupt the framing and detach the client. If you see a client disconnect seconds after startup, check for a rogue log line.
- **Authentication.** MCP runs in-process under whatever account spawned `cove serve --mcp`, so access is implicitly scoped to that user's VMs in `~/.vz/vms/`. Unlike the HTTP gateway, MCP does not carry a separate bearer token -- the transport is the security boundary.
- **Tool permissions.** The default tool set is full-access (including `exec` and `agent_write`). Agents that should only observe can be given a narrower client-side allowlist; cove itself exposes the full set.

## Related

- [Node.js MCP client example](../examples/nodejs-mcp-client.md) -- driving cove from a Node MCP client.
- [HTTP API](../reference/http-api.md) -- the HTTP transport over the same dispatch.
- [VZScript Engine](vzscript.md) -- the recipe format used by `vzscript_run`.
- [Guest Agent](guest-agent.md) -- how `exec`, `agent_read`, and `agent_write` reach the guest.
