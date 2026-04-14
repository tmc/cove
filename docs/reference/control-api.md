---
title: Control Socket API
---
# Control Socket API

Running VMs expose a Unix domain socket for control and monitoring.

## Connection

```
~/.vz/vms/<name>/control.sock   # socket path
~/.vz/vms/<name>/control.token  # auth token (owner-read only, 0600)
```

Commands are sent as single-line JSON (protobuf JSON mapping of `ControlRequest`). Responses are single-line JSON (`ControlResponse`).

## Authentication

Every request must include `auth_token`:

```bash
TOKEN=$(cat ~/.vz/vms/default/control.token)
echo '{"type":"ping","auth_token":"'$TOKEN'"}' | nc -U ~/.vz/vms/default/control.sock
```

## Request Format

```json
{
  "type": "<command-type>",
  "auth_token": "<token>",
  "<command-type>": { ... }
}
```

The `type` field selects the handler. The matching field name carries the payload.

## Response Format

```json
{
  "success": true,
  "error": "",
  "data": "...",
  "status": { ... }
}
```

The `data` field carries legacy string responses (base64 images, JSON, plain text). New clients should use the typed `result` oneof fields.

## Commands

### ping

Health check.

```bash
echo '{"type":"ping","auth_token":"'$TOKEN'"}' | nc -U ~/.vz/vms/default/control.sock
```

### status

VM state and capabilities.

```bash
echo '{"type":"status","auth_token":"'$TOKEN'"}' | nc -U ~/.vz/vms/default/control.sock
```

Response:
```json
{
  "success": true,
  "status": {
    "state": "running",
    "can_pause": true,
    "can_resume": false,
    "can_stop": true,
    "can_request_stop": true
  }
}
```

### capabilities

Machine-readable protocol capabilities.

```bash
echo '{"type":"capabilities","auth_token":"'$TOKEN'"}' | nc -U ~/.vz/vms/default/control.sock
```

Response includes protocol version, encoding, available commands, and feature flags.

### screenshot

Capture VM display.

```bash
# Default (base64 PNG in data field)
echo '{"type":"screenshot","auth_token":"'$TOKEN'"}' | nc -U ~/.vz/vms/default/control.sock

# Scaled down
echo '{"type":"screenshot","auth_token":"'$TOKEN'","screenshot":{"scale":0.5}}' | nc -U ~/.vz/vms/default/control.sock

# JPEG format with quality
echo '{"type":"screenshot","auth_token":"'$TOKEN'","screenshot":{"format":"jpeg","quality":80}}' | nc -U ~/.vz/vms/default/control.sock
```

### key

Send keyboard event.

```bash
# Return key (keycode 36)
echo '{"type":"key","auth_token":"'$TOKEN'","key":{"key_code":36}}' | nc -U ~/.vz/vms/default/control.sock

# Key with modifiers
echo '{"type":"key","auth_token":"'$TOKEN'","key":{"key_code":0,"modifiers":256}}' | nc -U ~/.vz/vms/default/control.sock
```

### text

Type a string character by character.

```bash
echo '{"type":"text","auth_token":"'$TOKEN'","text":{"text":"hello world"}}' | nc -U ~/.vz/vms/default/control.sock
```

### mouse

Send mouse event.

```bash
echo '{"type":"mouse","auth_token":"'$TOKEN'","mouse":{"x":0.5,"y":0.5,"action":"click","button":0}}' | nc -U ~/.vz/vms/default/control.sock
```

Coordinates are normalized (0-1). Actions: `move`, `down`, `up`, `click`.

### pause / resume

```bash
echo '{"type":"pause","auth_token":"'$TOKEN'"}' | nc -U ~/.vz/vms/default/control.sock
echo '{"type":"resume","auth_token":"'$TOKEN'"}' | nc -U ~/.vz/vms/default/control.sock
```

### snapshot

Manage VM state snapshots.

```bash
# Save
echo '{"type":"snapshot","auth_token":"'$TOKEN'","snapshot":{"action":"save","name":"cp1"}}' | nc -U ~/.vz/vms/default/control.sock

# List
echo '{"type":"snapshot","auth_token":"'$TOKEN'","snapshot":{"action":"list"}}' | nc -U ~/.vz/vms/default/control.sock

# Restore
echo '{"type":"snapshot","auth_token":"'$TOKEN'","snapshot":{"action":"restore","name":"cp1"}}' | nc -U ~/.vz/vms/default/control.sock

# Delete
echo '{"type":"snapshot","auth_token":"'$TOKEN'","snapshot":{"action":"delete","name":"cp1"}}' | nc -U ~/.vz/vms/default/control.sock
```

### memory

Runtime memory balloon control.

```bash
# Info
echo '{"type":"memory","auth_token":"'$TOKEN'","memory":{"action":"info"}}' | nc -U ~/.vz/vms/default/control.sock

# Set target to 8GB
echo '{"type":"memory","auth_token":"'$TOKEN'","memory":{"action":"set","size_gb":8}}' | nc -U ~/.vz/vms/default/control.sock
```

### ocr

OCR operations on the VM display.

```bash
# Get all text
echo '{"type":"ocr","auth_token":"'$TOKEN'","ocr":{"action":"all-text"}}' | nc -U ~/.vz/vms/default/control.sock

# Click text
echo '{"type":"ocr","auth_token":"'$TOKEN'","ocr":{"action":"click","text":"Continue"}}' | nc -U ~/.vz/vms/default/control.sock

# Wait for text
echo '{"type":"ocr","auth_token":"'$TOKEN'","ocr":{"action":"wait","text":"Desktop","timeout":"30s"}}' | nc -U ~/.vz/vms/default/control.sock

# Wait for text to disappear
echo '{"type":"ocr","auth_token":"'$TOKEN'","ocr":{"action":"gone","text":"Loading","timeout":"60s"}}' | nc -U ~/.vz/vms/default/control.sock

# Detect screen state
echo '{"type":"ocr","auth_token":"'$TOKEN'","ocr":{"action":"detect-screen"}}' | nc -U ~/.vz/vms/default/control.sock
```

### port_forward

Manage host TCP to guest vsock forwarding.

```bash
# Start
echo '{"type":"port_forward","auth_token":"'$TOKEN'","port_forward":{"action":"start","host_port":8080,"guest_port":80}}' | nc -U ~/.vz/vms/default/control.sock

# List
echo '{"type":"port_forward","auth_token":"'$TOKEN'","port_forward":{"action":"list"}}' | nc -U ~/.vz/vms/default/control.sock

# Stop
echo '{"type":"port_forward","auth_token":"'$TOKEN'","port_forward":{"action":"stop","host_port":8080}}' | nc -U ~/.vz/vms/default/control.sock
```

### Agent Commands

```bash
# Exec
echo '{"type":"agent-exec","auth_token":"'$TOKEN'","agent_exec":{"args":["ls","/tmp"]}}' | nc -U ~/.vz/vms/default/control.sock

# Read file
echo '{"type":"agent-read","auth_token":"'$TOKEN'","agent_read":{"path":"/etc/hosts"}}' | nc -U ~/.vz/vms/default/control.sock

# Write file
echo '{"type":"agent-write","auth_token":"'$TOKEN'","agent_write":{"path":"/tmp/test","data":"hello","mode":420}}' | nc -U ~/.vz/vms/default/control.sock

# Copy host to guest
echo '{"type":"agent-cp","auth_token":"'$TOKEN'","agent_cp":{"host_path":"/local/file","guest_path":"/tmp/file","to_guest":true}}' | nc -U ~/.vz/vms/default/control.sock

# Shutdown
echo '{"type":"agent-shutdown","auth_token":"'$TOKEN'","agent_shutdown":{}}' | nc -U ~/.vz/vms/default/control.sock

# SSHD management
echo '{"type":"agent-sshd","auth_token":"'$TOKEN'","agent_sshd":{"action":"on"}}' | nc -U ~/.vz/vms/default/control.sock
```

## Protobuf Schema

The full schema is defined in `proto/control.proto`. Key message types:

- `ControlRequest` -- request envelope with `type`, `auth_token`, and command oneof
- `ControlResponse` -- response envelope with `success`, `error`, `data`, and result oneof
- `KeyCommand`, `MouseCommand`, `TextCommand` -- input events
- `ScreenshotCommand` -- display capture with scale/format/quality options
- `SnapshotCommand` -- save/restore/list/delete snapshots
- `MemoryCommand` -- memory balloon info/set
- `OCRCommand` -- OCR click/wait/gone/all-text/detect operations
- `PortForwardCommand` -- TCP-to-vsock forwarding
- `AgentExecCommand`, `AgentFileReadCommand`, `AgentFileWriteCommand` -- guest agent operations

## Swift Client

A Swift package for the control socket is available at `swift/VZControl/` in the repository.
