---
title: Control Socket API
---
# Control Socket API

Running VMs expose a Unix domain socket for control and monitoring.

> [!NOTE]
> The control socket uses per-VM bearer token authentication. Tokens are stored in `~/.vz/vms/<name>/control.token` with owner-read-only permissions (0600).

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

## Python Helper

All Python examples below use this helper:

```python
import socket, json, os

SOCK = os.path.expanduser("~/.vz/vms/default/control.sock")
TOKEN = open(os.path.expanduser("~/.vz/vms/default/control.token")).read().strip()

def send(req):
    req["auth_token"] = TOKEN
    s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    s.connect(SOCK)
    s.sendall(json.dumps(req).encode() + b"\n")
    data = b""
    while b"\n" not in data:
        data += s.recv(65536)
    s.close()
    return json.loads(data)
```

## Go Client

All Go examples use the `ControlClient` from the main package:

```go
import cove "github.com/tmc/vz-macos"

client := cove.NewControlClient(os.ExpandEnv("$HOME/.vz/vms/default/control.sock"))
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

Health check. No parameters.

#### Shell

```bash
echo '{"type":"ping","auth_token":"'$TOKEN'"}' | nc -U ~/.vz/vms/default/control.sock
```

#### Python

```python
resp = send({"type": "ping"})
assert resp["success"]
```

#### Go

```go
err := client.Ping()
```

---

### status

VM state and capabilities. No parameters.

#### Shell

```bash
echo '{"type":"status","auth_token":"'$TOKEN'"}' | nc -U ~/.vz/vms/default/control.sock
```

#### Python

```python
resp = send({"type": "status"})
print(resp["status"]["state"])  # "running"
```

#### Go

```go
st, err := client.Status()
fmt.Println(st.State, st.CanPause, st.CanStop)
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

---

### capabilities

Machine-readable protocol capabilities. No parameters.

#### Shell

```bash
echo '{"type":"capabilities","auth_token":"'$TOKEN'"}' | nc -U ~/.vz/vms/default/control.sock
```

#### Python

```python
resp = send({"type": "capabilities"})
print(resp["data"])
```

#### Go

```go
caps, err := client.Capabilities()
```

Response includes protocol version, encoding, available commands, and feature flags.

---

### screenshot

Capture VM display.

#### Parameters

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `scale` | float | `1.0` | Scale factor (0.0-1.0) |
| `format` | string | `"png"` | Image format: `"png"` or `"jpeg"` |
| `quality` | int | `90` | JPEG quality (1-100), ignored for PNG |

#### Shell

```bash
# Default (base64 PNG in data field)
echo '{"type":"screenshot","auth_token":"'$TOKEN'"}' | nc -U ~/.vz/vms/default/control.sock

# Scaled JPEG
echo '{"type":"screenshot","auth_token":"'$TOKEN'","screenshot":{"scale":0.5,"format":"jpeg","quality":80}}' | nc -U ~/.vz/vms/default/control.sock
```

#### Python

```python
import base64
resp = send({"type": "screenshot", "screenshot": {"scale": 0.5, "format": "jpeg", "quality": 80}})
img_bytes = base64.b64decode(resp["data"])
open("screen.jpg", "wb").write(img_bytes)
```

#### Go

```go
img, err := client.Screenshot()                // full-size PNG as image.Image
img, err = client.ScreenshotScaled(0.5)        // scaled JPEG as image.Image
data, fmt, err := client.ScreenshotData()      // raw bytes + format string
```

---

### key

Send keyboard event.

#### Parameters

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `key_code` | uint32 | (required) | macOS virtual key code |
| `key_down` | bool | `true` | `true` for key down, `false` for key up |
| `modifiers` | uint32 | `0` | Modifier flags (e.g. 256 for Cmd) |
| `use_cg_event` | bool | `false` | Use CGEvent path (needed for app-level shortcuts) |

#### Shell

```bash
# Return key (keycode 36)
echo '{"type":"key","auth_token":"'$TOKEN'","key":{"key_code":36}}' | nc -U ~/.vz/vms/default/control.sock

# Key with modifiers
echo '{"type":"key","auth_token":"'$TOKEN'","key":{"key_code":0,"modifiers":256}}' | nc -U ~/.vz/vms/default/control.sock
```

#### Python

```python
send({"type": "key", "key": {"key_code": 36}})                        # Return
send({"type": "key", "key": {"key_code": 0, "modifiers": 256}})       # Cmd+A
```

#### Go

```go
err := client.KeyPress(36)                          // Return key
err = client.KeyPressWithModifiers(0, 256)           // Cmd+A
```

---

### text

Type a string character by character.

#### Parameters

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `text` | string | (required) | Text to type |

#### Shell

```bash
echo '{"type":"text","auth_token":"'$TOKEN'","text":{"text":"hello world"}}' | nc -U ~/.vz/vms/default/control.sock
```

#### Python

```python
send({"type": "text", "text": {"text": "hello world"}})
```

#### Go

```go
err := client.TypeText("hello world")
```

---

### mouse

Send mouse event.

#### Parameters

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `x` | float | (required) | X coordinate (0.0-1.0 normalized, or absolute pixels) |
| `y` | float | (required) | Y coordinate (0.0-1.0 normalized, or absolute pixels) |
| `action` | string | (required) | `"move"`, `"down"`, `"up"`, or `"click"` |
| `button` | int | `0` | Mouse button (0=left, 1=right, 2=middle) |
| `absolute` | bool | `false` | If true, coordinates are absolute window pixels |

#### Shell

```bash
echo '{"type":"mouse","auth_token":"'$TOKEN'","mouse":{"x":0.5,"y":0.5,"action":"click","button":0}}' | nc -U ~/.vz/vms/default/control.sock
```

#### Python

```python
send({"type": "mouse", "mouse": {"x": 0.5, "y": 0.5, "action": "click", "button": 0}})
```

#### Go

```go
err := client.MouseClick(0.5, 0.5)                  // normalized coordinates
err = client.MouseClickAbsolute(960, 540)            // absolute pixels
```

---

### pause / resume

Pause or resume the VM. No parameters.

#### Shell

```bash
echo '{"type":"pause","auth_token":"'$TOKEN'"}' | nc -U ~/.vz/vms/default/control.sock
echo '{"type":"resume","auth_token":"'$TOKEN'"}' | nc -U ~/.vz/vms/default/control.sock
```

#### Python

```python
send({"type": "pause"})
send({"type": "resume"})
```

#### Go

```go
err := client.Pause()
err = client.Resume()
```

---

### stop

Stop the VM. No parameters.

#### Shell

```bash
echo '{"type":"stop","auth_token":"'$TOKEN'"}' | nc -U ~/.vz/vms/default/control.sock
```

#### Python

```python
send({"type": "stop"})
```

#### Go

```go
err := client.Stop()
```

---

### snapshot

Manage VM state snapshots.

#### Parameters

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `action` | string | (required) | `"save"`, `"restore"`, `"list"`, or `"delete"` |
| `name` | string | (required for save/restore/delete) | Snapshot name |

#### Shell

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

#### Python

```python
send({"type": "snapshot", "snapshot": {"action": "save", "name": "cp1"}})
resp = send({"type": "snapshot", "snapshot": {"action": "list"}})
send({"type": "snapshot", "snapshot": {"action": "restore", "name": "cp1"}})
send({"type": "snapshot", "snapshot": {"action": "delete", "name": "cp1"}})
```

#### Go

```go
msg, err := client.SnapshotSave("cp1")
list, err := client.SnapshotList()
msg, err = client.SnapshotRestore("cp1")
msg, err = client.SnapshotDelete("cp1")
```

---

### memory

Runtime memory balloon control.

#### Parameters

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `action` | string | (required) | `"info"` or `"set"` |
| `size_gb` | float | (required for set) | Target memory size in GB |

#### Shell

```bash
# Info
echo '{"type":"memory","auth_token":"'$TOKEN'","memory":{"action":"info"}}' | nc -U ~/.vz/vms/default/control.sock

# Set target to 8GB
echo '{"type":"memory","auth_token":"'$TOKEN'","memory":{"action":"set","size_gb":8}}' | nc -U ~/.vz/vms/default/control.sock
```

#### Python

```python
resp = send({"type": "memory", "memory": {"action": "info"}})
send({"type": "memory", "memory": {"action": "set", "size_gb": 8}})
```

#### Go

```go
info, err := client.MemoryInfo()
msg, err := client.MemorySet(8.0)
```

---

### ocr

OCR operations on the VM display.

#### Parameters

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `action` | string | (required) | `"all-text"`, `"click"`, `"wait"`, `"gone"`, or `"detect-screen"` |
| `text` | string | (required for click/wait/gone) | Text to find |
| `timeout` | string | `"10s"` | Timeout duration for wait/gone (Go duration format) |

#### Shell

```bash
# Get all text
echo '{"type":"ocr","auth_token":"'$TOKEN'","ocr":{"action":"all-text"}}' | nc -U ~/.vz/vms/default/control.sock

# Click text
echo '{"type":"ocr","auth_token":"'$TOKEN'","ocr":{"action":"click","text":"Continue"}}' | nc -U ~/.vz/vms/default/control.sock

# Wait for text to appear
echo '{"type":"ocr","auth_token":"'$TOKEN'","ocr":{"action":"wait","text":"Desktop","timeout":"30s"}}' | nc -U ~/.vz/vms/default/control.sock

# Wait for text to disappear
echo '{"type":"ocr","auth_token":"'$TOKEN'","ocr":{"action":"gone","text":"Loading","timeout":"60s"}}' | nc -U ~/.vz/vms/default/control.sock

# Detect screen state
echo '{"type":"ocr","auth_token":"'$TOKEN'","ocr":{"action":"detect-screen"}}' | nc -U ~/.vz/vms/default/control.sock
```

#### Python

```python
resp = send({"type": "ocr", "ocr": {"action": "all-text"}})
print(resp["data"])

send({"type": "ocr", "ocr": {"action": "click", "text": "Continue"}})
send({"type": "ocr", "ocr": {"action": "wait", "text": "Desktop", "timeout": "30s"}})
send({"type": "ocr", "ocr": {"action": "gone", "text": "Loading", "timeout": "60s"}})
```

#### Go

```go
text, err := client.OCRAllText()
err = client.OCRClickText("Continue", 10*time.Second)
```

---

### port_forward

Manage host TCP to guest vsock forwarding.

#### Parameters

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `action` | string | (required) | `"start"`, `"stop"`, or `"list"` |
| `host_port` | int | (required for start/stop) | Host TCP port |
| `guest_port` | int | (required for start) | Guest vsock port |

#### Shell

```bash
# Start
echo '{"type":"port_forward","auth_token":"'$TOKEN'","port_forward":{"action":"start","host_port":8080,"guest_port":80}}' | nc -U ~/.vz/vms/default/control.sock

# List
echo '{"type":"port_forward","auth_token":"'$TOKEN'","port_forward":{"action":"list"}}' | nc -U ~/.vz/vms/default/control.sock

# Stop
echo '{"type":"port_forward","auth_token":"'$TOKEN'","port_forward":{"action":"stop","host_port":8080}}' | nc -U ~/.vz/vms/default/control.sock
```

#### Python

```python
send({"type": "port_forward", "port_forward": {"action": "start", "host_port": 8080, "guest_port": 80}})
resp = send({"type": "port_forward", "port_forward": {"action": "list"}})
send({"type": "port_forward", "port_forward": {"action": "stop", "host_port": 8080}})
```

#### Go

Port forwarding is managed through the raw request interface. See the `sendRequest` method for custom commands.

---

### Agent Commands

Guest agent operations require the vz-agent daemon running inside the VM.

#### agent-exec

Run a command in the guest.

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `args` | string[] | (required) | Command and arguments |
| `env` | object | `{}` | Environment variables |
| `working_dir` | string | `""` | Working directory |

##### Shell

```bash
echo '{"type":"agent-exec","auth_token":"'$TOKEN'","agent_exec":{"args":["ls","/tmp"]}}' | nc -U ~/.vz/vms/default/control.sock
```

##### Python

```python
resp = send({"type": "agent-exec", "agent_exec": {"args": ["ls", "/tmp"]}})
print(resp["data"])
```

##### Go

```go
result, err := client.AgentExecTyped([]string{"ls", "/tmp"}, nil, "")
fmt.Println(string(result.Stdout))
```

#### agent-exec-attach

Open a long-lived attach to a guest exec session with PTY allocation. The server streams JSON-line frames (`{"type":"attached"}`, `{"type":"stdout","data":"<base64>"}`, `{"type":"stderr","data":"<base64>"}`, `{"type":"done","exit":N}`). Stdin frames (`{"type":"stdin","data":"<base64>"}`) are decoded and discarded in this slice; bidirectional stdin is the v0.3 proto bump (see [design 023](../designs/023-cove-shell-exec-ux.md) Slice 3).

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `args` | string[] | (required) | Command and arguments |
| `env` | object | `{}` | Environment variables |
| `working_dir` | string | `""` | Working directory |
| `tty` | bool | `false` | Allocate a PTY in the guest |

The `cove shell <vm>` client (see [`cove shell`](cli.md#shell)) is the canonical consumer.

##### Shell

```bash
echo '{"type":"agent-exec-attach","auth_token":"'$TOKEN'","agent_exec":{"args":["bash","-l"],"tty":true}}' \
  | nc -U ~/.vz/vms/default/control.sock
```

#### agent-exec-resize

Resize the PTY of an active exec session.

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `exec_id` | string | (required) | Exec session ID returned by `agent-exec-attach` |
| `cols` | int | (required) | New column count |
| `rows` | int | (required) | New row count |

##### Shell

```bash
echo '{"type":"agent-exec-resize","auth_token":"'$TOKEN'","exec_id":"'$EID'","cols":120,"rows":40}' \
  | nc -U ~/.vz/vms/default/control.sock
```

#### agent-exec-signal

Send a signal to an active exec session's process group.

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `exec_id` | string | (required) | Exec session ID returned by `agent-exec-attach` |
| `signal` | int | (required) | Unix signal number (e.g. `2` for SIGINT, `15` for SIGTERM) |

##### Shell

```bash
echo '{"type":"agent-exec-signal","auth_token":"'$TOKEN'","exec_id":"'$EID'","signal":2}' \
  | nc -U ~/.vz/vms/default/control.sock
```

#### agent-read

Read a file from the guest.

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `path` | string | (required) | Guest file path |

##### Shell

```bash
echo '{"type":"agent-read","auth_token":"'$TOKEN'","agent_read":{"path":"/etc/hosts"}}' | nc -U ~/.vz/vms/default/control.sock
```

##### Python

```python
resp = send({"type": "agent-read", "agent_read": {"path": "/etc/hosts"}})
```

##### Go

```go
data, err := client.AgentReadFile("/etc/hosts")
```

#### agent-write

Write a file to the guest.

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `path` | string | (required) | Guest file path |
| `data` | string | (required) | File contents |
| `mode` | int | `420` | File mode (420 = 0644) |

##### Shell

```bash
echo '{"type":"agent-write","auth_token":"'$TOKEN'","agent_write":{"path":"/tmp/test","data":"hello","mode":420}}' | nc -U ~/.vz/vms/default/control.sock
```

##### Python

```python
send({"type": "agent-write", "agent_write": {"path": "/tmp/test", "data": "hello", "mode": 420}})
```

##### Go

Agent file write is available through the raw request interface.

#### agent-cp

Copy files between host and guest.

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `host_path` | string | (required) | Host file path |
| `guest_path` | string | (required) | Guest file path |
| `to_guest` | bool | (required) | `true` to copy host-to-guest, `false` for guest-to-host |

##### Shell

```bash
echo '{"type":"agent-cp","auth_token":"'$TOKEN'","agent_cp":{"host_path":"/local/file","guest_path":"/tmp/file","to_guest":true}}' | nc -U ~/.vz/vms/default/control.sock
```

##### Python

```python
send({"type": "agent-cp", "agent_cp": {"host_path": "/local/file", "guest_path": "/tmp/file", "to_guest": True}})
```

##### Go

Agent copy is available through the raw request interface.

#### agent-ping

Check if the guest agent is alive.

##### Shell

```bash
echo '{"type":"agent-ping","auth_token":"'$TOKEN'"}' | nc -U ~/.vz/vms/default/control.sock
```

##### Python

```python
resp = send({"type": "agent-ping"})
```

##### Go

```go
version, err := client.AgentPingTyped()
```

#### agent-info

Get guest system information.

##### Shell

```bash
echo '{"type":"agent-info","auth_token":"'$TOKEN'"}' | nc -U ~/.vz/vms/default/control.sock
```

##### Python

```python
resp = send({"type": "agent-info"})
```

##### Go

```go
info, err := client.AgentInfo()
```

#### agent-shutdown

Shut down the guest OS.

##### Shell

```bash
echo '{"type":"agent-shutdown","auth_token":"'$TOKEN'","agent_shutdown":{}}' | nc -U ~/.vz/vms/default/control.sock
```

##### Python

```python
send({"type": "agent-shutdown", "agent_shutdown": {}})
```

##### Go

Agent shutdown is available through the raw request interface.

#### agent-sshd

Manage SSH daemon in the guest.

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `action` | string | (required) | `"on"` or `"off"` |

##### Shell

```bash
echo '{"type":"agent-sshd","auth_token":"'$TOKEN'","agent_sshd":{"action":"on"}}' | nc -U ~/.vz/vms/default/control.sock
```

##### Python

```python
send({"type": "agent-sshd", "agent_sshd": {"action": "on"}})
```

##### Go

Agent SSHD management is available through the raw request interface.

---

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
