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

By default the master token lives in the macOS keychain (service `com.tmc.cove.gateway`, account `master-token`). On first launch cove generates 32 random bytes, hex-encodes them into 64 characters, and stores the token with the description "cove gateway — grants full access to all local VMs", which appears on the keychain access prompt.

If the keychain is unavailable (headless login, no GUI session, Security.framework dlopen fails), cove falls back to `~/.vz/gateway.token` (mode 0600) and prints one line to stderr noting the fallback.

Retrieve it for scripting:

```bash
TOKEN=$(security find-generic-password -s com.tmc.cove.gateway -a master-token -w)
# or from the file fallback:
TOKEN=$(cat ~/.vz/gateway.token)

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
POST   /v1/vms/:name/request-stop            # no body; graceful ACPI power button
POST   /v1/vms/:name/stop                    # no body; force stop
GET    /v1/vms/:name/screenshot              # image/png
POST   /v1/vms/:name/type                    # body: {"text": "..."}
POST   /v1/vms/:name/key                     # body: {"code": N, "modifiers": M, "key_down": true}
POST   /v1/vms/:name/mouse                   # body: {"x": .., "y": .., "button": 0, "action": "click"}
```

### Example: list and status

```bash
curl -H "Authorization: Bearer $TOKEN" http://127.0.0.1:7777/v1/vms
```

The gateway only lists VMs with a reachable control socket under `~/.vz/vms/<name>/control.sock`; stopped VMs do not appear. `status` is always `"running"` for now — richer per-VM state comes from `/v1/vms/:name/status`.

```json
{
  "vms": [
    {"name": "default",   "status": "running"},
    {"name": "ci-runner", "status": "running"}
  ]
}
```

Empty case:

```json
{"vms": []}
```

```bash
curl -H "Authorization: Bearer $TOKEN" \
  http://127.0.0.1:7777/v1/vms/default/status
```

Response is a protobuf `ControlResponse` envelope with a `status` oneof payload:

```json
{
  "success": true,
  "status": {
    "state": "running",
    "canPause": true,
    "canResume": false,
    "canStop": true,
    "canRequestStop": true
  }
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

### Example: graceful shutdown

```bash
curl -X POST -H "Authorization: Bearer $TOKEN" \
  http://127.0.0.1:7777/v1/vms/default/request-stop
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
  -d '{"x":0.5,"y":0.5,"action":"click"}' \
  http://127.0.0.1:7777/v1/vms/default/mouse
```

**Key events**: `code` is the macOS virtual key code (36=Return, 53=Escape, 48=Tab; see [`HIToolbox/Events.h`](https://developer.apple.com/documentation/coregraphics/cgkeycode)). `modifiers` is a uint32 bitmap of modifier flags (Shift=0x20000, Ctrl=0x40000, Option=0x80000, Cmd=0x100000). `key_down` defaults to `true`; pass `false` to send a key-up event only.

**Mouse events**: `x`/`y` are normalized (0.0–1.0). `button` is 0=left, 1=right, 2=middle. `action` is one of `click`, `down`, `up`, `move`; omitting `action` with `click: true` (legacy body field) is also accepted.

## Guest agent

```
POST   /v1/vms/:name/agent/exec              # body: {"cmd": "...", "args": [...]}
GET    /v1/vms/:name/agent/read?path=/foo    # returns raw file bytes
POST   /v1/vms/:name/agent/write             # body: {"path": "...", "data": "<base64>", "mode": 0644}
POST   /v1/vms/:name/agent/cp                # body: {"src": "...", "dst": "...", "to_guest": true}
```

### Example: exec a command

```bash
curl -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"cmd":"sw_vers","args":[]}' \
  http://127.0.0.1:7777/v1/vms/default/agent/exec
```

Response is the `ControlResponse` envelope with the result in the `agentExecResult` oneof:

```json
{
  "success": true,
  "agentExecResult": {
    "exitCode": 0,
    "stdout": "ProductName:\t\tmacOS\nProductVersion:\t\t15.2\n...",
    "stderr": "",
    "durationSeconds": 0.12
  }
}
```

The `cmd` field is prepended to `args` before the call is dispatched, so `{"cmd":"sw_vers","args":[]}` and `{"args":["sw_vers"]}` are equivalent.

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

> [!NOTE]
> In the current gateway, `POST /v1/vms` is stubbed: it creates the operation record to exercise the long-running-operation plumbing, then immediately transitions the operation to `failed` with code `not_implemented`. Use `cove install` from the CLI to provision VMs until this lands (see `docs/designs/archive/001a-defer-create-vm-to-v02.md`). Clients can still use the shape below for retry and polling logic.

```bash
curl -i -X POST -H "Authorization: Bearer $TOKEN" \
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
Location: /v1/operations/op_a73d8770
Content-Type: application/json

{
  "id": "op_a73d8770",
  "resource": "vms/",
  "status": "pending",
  "created_at": "2026-04-19T21:26:26.949689-07:00",
  "updated_at": "2026-04-19T21:26:26.949689-07:00"
}
```

Then poll or stream:

```bash
curl -H "Authorization: Bearer $TOKEN" \
  http://127.0.0.1:7777/v1/operations/op_a73d8770
```

```json
{
  "id": "op_a73d8770",
  "resource": "vms/my-macos",
  "status": "running",
  "progress": {"phase": "download_ipsw", "percent": 42},
  "created_at": "2026-04-19T21:26:26.949689-07:00",
  "updated_at": "2026-04-19T21:26:28.512345-07:00"
}
```

On success `status` flips to `succeeded` and a `result` field appears; on failure `status` is `failed` with an `error` field:

```json
{
  "id": "op_a73d8770",
  "resource": "vms/",
  "status": "failed",
  "created_at": "2026-04-19T21:26:26.949689-07:00",
  "updated_at": "2026-04-19T21:26:27.101607-07:00",
  "error": {
    "code": "not_implemented",
    "message": "create_vm via HTTP API is deferred to v0.2; use 'cove install' from the CLI"
  }
}
```

## Operations

```
GET    /v1/operations                        # list recent operations
GET    /v1/operations/:id                    # current state snapshot
GET    /v1/operations/:id/events             # SSE stream of progress updates
```

`GET /v1/operations` wraps results in an object:

```json
{
  "operations": [
    {"id": "op_a73d8770", "resource": "vms/", "status": "failed", "created_at": "...", "updated_at": "...", "error": {"code": "not_implemented", "message": "..."}}
  ]
}
```

`GET /v1/operations/:id` returns the bare operation JSON (same shape as list entries, unwrapped). 404 if the ID is unknown.

Operations are written to `~/.vz/operations/<op_id>.json` with write-temp-then-rename on every state change and `fsync` of the parent directory. This means an operation record survives `cove serve` restart -- if `brew upgrade cove` restarts the gateway mid-install, clients can keep polling the same operation ID and see the correct state. Pending or running operations orphaned by a restart are rewritten as `failed` with code `server_restart`. Terminal operations are retained for 1 hour then garbage-collected.

## Snapshots

```
POST   /v1/vms/:name/snapshot                # body: {"name": "checkpoint1"}
GET    /v1/vms/:name/snapshots
POST   /v1/vms/:name/snapshots/:snap/restore
DELETE /v1/vms/:name/snapshots/:snap
GET    /v1/vms/:name/disk-snapshots          # read-only APFS disk snapshot inventory
GET    /v1/vms/:name/pit-snapshots           # read-only point-in-time snapshot inventory
```

```bash
curl -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"checkpoint1"}' \
  http://127.0.0.1:7777/v1/vms/default/snapshot

curl -H "Authorization: Bearer $TOKEN" \
  http://127.0.0.1:7777/v1/vms/default/snapshots

curl -H "Authorization: Bearer $TOKEN" \
  http://127.0.0.1:7777/v1/vms/default/disk-snapshots

curl -H "Authorization: Bearer $TOKEN" \
  http://127.0.0.1:7777/v1/vms/default/pit-snapshots
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

## Errors

Gateway-level errors (routing, auth, unknown VM) return plain-text bodies:

| Status | Body                                              | Cause |
| ------ | ------------------------------------------------- | ----- |
| `401`  | `unauthorized`                                    | Missing or wrong `Authorization: Bearer <token>`. Sets `WWW-Authenticate: Bearer realm="cove-gateway"` (or `"cove-vm"` in per-VM mode). |
| `404`  | `vm "<name>" not found or not running`            | The VM name in the URL has no reachable control socket under `~/.vz/vms/<name>/`. |
| `404`  | `operation not found`                             | `GET /v1/operations/:id` for an unknown ID. |
| `400`  | `missing vm name`                                 | `/v1/vms//...` or similar malformed URL. |
| `502`  | `connect to vm "<name>": …`                       | Socket disappeared between route lookup and dial, or the VM process died. |
| `502`  | `vm closed connection without response`           | The VM's control server closed the socket before writing a response. |

Per-VM control endpoints (proxied through the gateway) return JSON-wrapped errors for application-level failures. The VM's `ControlResponse` envelope carries `{"error": "…"}` and is echoed as the HTTP body; status is `500` for generic errors, `401` for `"unauthorized"` from the VM.

```bash
$ curl -i http://127.0.0.1:7777/v1/vms
HTTP/1.1 401 Unauthorized
Www-Authenticate: Bearer realm="cove-gateway"
Content-Type: text/plain; charset=utf-8

unauthorized
```

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
    print(vm["name"], vm["status"])

# Screenshot
r = requests.get(f"{BASE}/v1/vms/default/screenshot", headers=headers)
open("screen.png", "wb").write(r.content)

# Exec in guest
r = requests.post(
    f"{BASE}/v1/vms/default/agent/exec",
    headers=headers,
    json={"cmd": "uname", "args": ["-a"]},
)
print(r.json()["agentExecResult"]["stdout"])

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
