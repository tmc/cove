---
title: HTTP API Reference
---
# HTTP API Reference

`cove serve` exposes VM operations over HTTP, with an optional stdio MCP transport for AI agents. It's a subcommand on the same `cove` binary -- no daemon, no new service, no separate install.

```bash
cove serve -http 127.0.0.1:7777 &
curl http://127.0.0.1:7777/healthz
# {"status":"ok"}
```

The server reads `~/.vz/vms/` at startup, proxies each VM's Unix control socket under `/v1/vms/<name>/...`, and hot-adds routes when new VMs come up.

## Authentication

Every authenticated request carries `Authorization: Bearer <token>`.

By default the master token lives in the macOS keychain (service `cove-gateway`, account `$USER`). On first launch cove generates a 32-byte random token and stores it with the description "cove gateway -- grants full access to all local VMs", which appears on every keychain access prompt.

Retrieve it for scripting:

```bash
TOKEN=$(security find-generic-password -s cove-gateway -a $USER -w)
curl -H "Authorization: Bearer $TOKEN" http://127.0.0.1:7777/v1/vms
```

File-backed token (for CI where the login keychain isn't unlocked):

```bash
cove serve -token-file ~/.cove/api.token
```

For multi-user hosts or when you want to hand one token to one automation, use strict per-VM auth:

```bash
cove serve -per-vm-auth
```

In strict mode, `/v1/vms/<name>/...` requires that VM's own per-VM bearer token. See [Control Socket API — Authentication](control-api.md#authentication) for token location, permissions, and rotation.

> [!WARNING]
> The master token grants full `agent_exec` in every running VM. Treat it like an SSH private key. On shared Mac minis or any host where more than one UID can read your keychain, run `cove serve -per-vm-auth`.

Cove binds `localhost` by default. There's no TLS -- the assumption is localhost or trusted-network use; remote access goes through an SSH tunnel.

## Lifecycle

```
GET    /healthz                              # no auth; {"status":"ok"}
GET    /v1/vms                               # list VMs + states
GET    /v1/vms/:name/status                  # state + capabilities
POST   /v1/vms/:name/pause
POST   /v1/vms/:name/resume
POST   /v1/vms/:name/stop                    # body: {"force": false}
GET    /v1/vms/:name/screenshot              # image/png
POST   /v1/vms/:name/type                    # body: {"text": "..."}
POST   /v1/vms/:name/key                     # body: {"code": N, "modifiers": [...]}
POST   /v1/vms/:name/mouse                   # body: {"x": .., "y": .., "click": true}
```

### Example: list and status

```bash
curl -H "Authorization: Bearer $TOKEN" http://127.0.0.1:7777/v1/vms
```

```json
{
  "vms": [
    {"name": "default",   "state": "running",  "cpu": 4, "memory_gb": 8},
    {"name": "ci-runner", "state": "stopped",  "cpu": 4, "memory_gb": 8}
  ]
}
```

```bash
curl -H "Authorization: Bearer $TOKEN" \
  http://127.0.0.1:7777/v1/vms/default/status
```

```json
{
  "state": "running",
  "can_pause": true,
  "can_resume": false,
  "can_stop": true,
  "can_request_stop": true
}
```

### Example: pause, screenshot, resume

```bash
curl -X POST -H "Authorization: Bearer $TOKEN" \
  http://127.0.0.1:7777/v1/vms/default/pause

curl -H "Authorization: Bearer $TOKEN" \
  http://127.0.0.1:7777/v1/vms/default/screenshot \
  -o screen.png

curl -X POST -H "Authorization: Bearer $TOKEN" \
  http://127.0.0.1:7777/v1/vms/default/resume
```

### Example: send input

```bash
# Type text
curl -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"text":"hello world"}' \
  http://127.0.0.1:7777/v1/vms/default/type

# Press Return (key code 36)
curl -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"code":36}' \
  http://127.0.0.1:7777/v1/vms/default/key

# Click at normalized coordinates
curl -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"x":0.5,"y":0.5,"click":true}' \
  http://127.0.0.1:7777/v1/vms/default/mouse
```

## Guest agent

```
POST   /v1/vms/:name/agent/exec              # body: {"cmd": "...", "args": [...], "as_user": false}
GET    /v1/vms/:name/agent/read?path=/foo    # returns file body
POST   /v1/vms/:name/agent/write             # body: {"path": "...", "data": "<base64>"}
POST   /v1/vms/:name/agent/cp                # body: {"src": "host", "dst": "guest", "to_guest": true}
```

### Example: exec a command

```bash
curl -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"cmd":"sw_vers","args":[],"as_user":false}' \
  http://127.0.0.1:7777/v1/vms/default/agent/exec
```

```json
{
  "exit_code": 0,
  "stdout": "ProductName:\t\tmacOS\nProductVersion:\t\t15.2\n...",
  "stderr": ""
}
```

Pass `"as_user": true` to route through the user session agent (port 1024 is the daemon, user session has TCC/FDA access).

### Example: read and write files

```bash
curl -H "Authorization: Bearer $TOKEN" \
  "http://127.0.0.1:7777/v1/vms/default/agent/read?path=/etc/hosts"

# Write base64-encoded content
curl -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"path":"/tmp/hello","data":"aGVsbG8K"}' \
  http://127.0.0.1:7777/v1/vms/default/agent/write
```

## VM creation (long-running)

VM creation takes minutes (IPSW download, install, first boot). The API returns `202 Accepted` immediately with a Location header pointing at an operation record.

```
POST   /v1/vms                               # async create; returns 202 + Location
```

```bash
curl -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "my-macos",
    "installer": {"kind": "ipsw", "source": "latest"},
    "cpu": 4,
    "memory_gb": 8,
    "disk_gb": 64
  }' \
  http://127.0.0.1:7777/v1/vms
```

Response:

```
HTTP/1.1 202 Accepted
Location: /v1/operations/op_8f2a1c
Content-Type: application/json

{
  "operation_id": "op_8f2a1c",
  "resource": "vms/my-macos",
  "status": "pending",
  "created_at": "2026-04-16T17:30:00Z"
}
```

Then poll or stream:

```bash
curl -H "Authorization: Bearer $TOKEN" \
  http://127.0.0.1:7777/v1/operations/op_8f2a1c
```

```json
{
  "operation_id": "op_8f2a1c",
  "resource": "vms/my-macos",
  "status": "running",
  "progress": {"phase": "download_ipsw", "percent": 42},
  "created_at": "2026-04-16T17:30:00Z"
}
```

On success `status` flips to `succeeded` and a `result` field appears; on failure `status` is `failed` with an `error` field.

## Operations

```
GET    /v1/operations                        # list recent operations
GET    /v1/operations/:id                    # current state snapshot
GET    /v1/operations/:id/events             # SSE stream of progress updates
```

Operations are written to `~/.vz/operations/<op_id>.json` with write-temp-then-rename on every state change and `fsync` of the parent directory. This means an operation record survives `cove serve` restart -- if `brew upgrade cove` restarts the gateway mid-install, clients can keep polling the same operation ID and see the correct state. Terminal operations are retained for 1 hour then garbage-collected.

## Snapshots

```
POST   /v1/vms/:name/snapshot                # body: {"name": "checkpoint1"}
GET    /v1/vms/:name/snapshots
POST   /v1/vms/:name/snapshots/:snap/restore
DELETE /v1/vms/:name/snapshots/:snap
```

```bash
curl -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"checkpoint1"}' \
  http://127.0.0.1:7777/v1/vms/default/snapshot

curl -H "Authorization: Bearer $TOKEN" \
  http://127.0.0.1:7777/v1/vms/default/snapshots
```

## Events (SSE)

```
GET    /v1/vms/:name/events                  # SSE stream of VM state changes
GET    /v1/operations/:id/events             # SSE stream of operation progress
```

Server-Sent Events, `Content-Type: text/event-stream`. Each event is a JSON object; an empty line terminates the record.

```bash
curl -N -H "Authorization: Bearer $TOKEN" \
  http://127.0.0.1:7777/v1/vms/default/events
```

```
event: state
data: {"state":"paused","at":"2026-04-16T17:31:02Z"}

event: state
data: {"state":"running","at":"2026-04-16T17:31:05Z"}
```

Operation event streams emit `{phase, percent, message}` records until the operation reaches a terminal state, then the stream closes.

## Python client

```python
import requests, os

TOKEN = os.environ["COVE_TOKEN"]
BASE = "http://127.0.0.1:7777"
headers = {"Authorization": f"Bearer {TOKEN}"}

# List VMs
r = requests.get(f"{BASE}/v1/vms", headers=headers)
r.raise_for_status()
for vm in r.json()["vms"]:
    print(vm["name"], vm["state"])

# Screenshot
r = requests.get(f"{BASE}/v1/vms/default/screenshot", headers=headers)
open("screen.png", "wb").write(r.content)

# Exec in guest
r = requests.post(
    f"{BASE}/v1/vms/default/agent/exec",
    headers=headers,
    json={"cmd": "uname", "args": ["-a"], "as_user": False},
)
print(r.json()["stdout"])

# Create VM (long-running)
r = requests.post(
    f"{BASE}/v1/vms",
    headers=headers,
    json={"name": "my-macos", "installer": {"kind": "ipsw", "source": "latest"},
          "cpu": 4, "memory_gb": 8, "disk_gb": 64},
)
assert r.status_code == 202
op_url = BASE + r.headers["Location"]

# Poll until terminal
import time
while True:
    st = requests.get(op_url, headers=headers).json()
    print(st["status"], st.get("progress"))
    if st["status"] in ("succeeded", "failed"):
        break
    time.sleep(5)
```

## Related

- [Node.js MCP client example](../examples/nodejs-mcp-client.md) -- driving cove from an MCP client in Node.
- [MCP transport](../features/mcp.md) -- stdio MCP for AI agents.
- [Push & Pull](../getting-started/push-pull.md) -- image distribution over OCI registries.
- [Control Socket API](control-api.md) -- the underlying Unix socket the HTTP API proxies.
