---
title: MCP (Model Context Protocol)
---
# MCP (Model Context Protocol)

`cove serve --mcp` speaks the Model Context Protocol over stdio, so AI agents like Claude Code, Cursor, and Cline can list, inspect, screenshot, snapshot, pause, resume, and drive running VMs as tools.

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

Each tool takes a JSON object matching the advertised `inputSchema`. Use `cove dump-docs -type mcp` for the machine-readable source of truth.

| Tool | Purpose | Required input |
|------|---------|----------------|
| `vm_list` | List VMs with reachable control sockets. | none |
| `vm_status` | Report lifecycle state and capabilities. | `name` |
| `vm_pause` | Pause a running VM. | `name` |
| `vm_resume` | Resume a paused VM. | `name` |
| `vm_stop` | Force-stop a running VM. | `name` |
| `vm_request_stop` | Request graceful guest shutdown with an ACPI power button event. | `name` |
| `vm_screenshot` | Capture the VM display as MCP image content. | `name` |
| `vm_type` | Type text into the VM. | `name`, `text` |
| `vm_key` | Send a keyboard event by macOS virtual key code. | `name`, `code` |
| `vm_mouse` | Send a mouse event at normalized coordinates. | `name`, `x`, `y` |
| `vm_agent_exec` | Run a guest command through the vz-agent daemon. | `name`, `cmd` |
| `vm_agent_read` | Read a guest file. | `name`, `path` |
| `vm_agent_write` | Write a guest file. | `name`, `path`, `data` |
| `vm_snapshot_save` | Save a VM state snapshot. | `name`, `snapshot` |
| `vm_snapshot_list` | List VM state snapshots. | `name` |
| `vm_disk_snapshot_list` | List APFS disk snapshots without requiring a running VM. | `name` |
| `vm_pit_snapshot_list` | List point-in-time snapshots without requiring a running VM. | `name` |
| `vm_snapshot_restore` | Restore a VM state snapshot. | `name`, `snapshot` |
| `vm_snapshot_delete` | Delete a VM state snapshot. | `name`, `snapshot` |

Most tools return a text content block containing the typed control response JSON. `vm_screenshot` returns MCP image content instead, with `mimeType` set to `image/png` or `image/jpeg`.

State snapshot tools operate on running VM state. Disk and PIT inventory tools read snapshot metadata from the VM directory and do not mutate the VM.

## Safety notes

- **Stdout is MCP only.** All cove logging, verbose output, and diagnostics go to stderr. This is enforced in MCP mode -- printing to stdout from anywhere in the process would corrupt the framing and detach the client. If you see a client disconnect seconds after startup, check for a rogue log line.
- **Authentication.** MCP runs in-process under whatever account spawned `cove serve --mcp`, so access is implicitly scoped to that user's VMs in `~/.vz/vms/`. Unlike the HTTP gateway, MCP does not carry a separate bearer token -- the transport is the security boundary.
- **Tool permissions.** The default tool set is full-access (including `vm_agent_exec` and `vm_agent_write`). Agents that should only observe can be given a narrower client-side allowlist; cove itself exposes the full set.

## Related

- [Node.js MCP client example](../examples/nodejs-mcp-client.md) -- driving cove from a Node MCP client.
- [HTTP API](../reference/http-api.md) -- the HTTP transport over the same dispatch.
- [VZScript Engine](vzscript.md) -- the recipe format used by CLI automation.
- [Guest Agent](guest-agent.md) -- how `vm_agent_exec`, `vm_agent_read`, and `vm_agent_write` reach the guest.
