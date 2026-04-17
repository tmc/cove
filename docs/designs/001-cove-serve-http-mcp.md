# cove serve — HTTP & MCP design

**Status**: draft v2 (post-round-2 Council + second-opinion review)
**Author**: cove team
**Date**: 2026-04-16
**Target ship**: before cove 0.1 launch (this week / next)

## Changelog

- **v2 (2026-04-16)**: Post-round-2 Council + second-opinion review. Added LRO durability via write-temp-then-rename + fsync-parent so `cove serve` restart or brew upgrade mid-operation doesn't produce client 404s or silent duplicate installs. Moved master token into macOS keychain by default with descriptive label visible on every access prompt; `-token-file` remains for CI. Added prominent multi-user-host warning recommending `-per-vm-auth` for shared Mac minis. Added open question 12 on the shared-host story (file-lock vs per-user sockets vs `coved`).
- **v1 (2026-04-16)**: Post-Council-round-1 + user interview revisions. Flipped gateway auth default to master-token with `-per-vm-auth` opt-in. Pulled `create_vm` forward into v0.1 scope. Added long-running operations pattern (202 + `/v1/operations/:id` + SSE events). Pruned open questions resolved in round-1.
- **v0 (2026-04-16)**: Initial draft for Council review.

## Goal

Close the "AI-agent-ready" gap with trycua/lume by exposing cove's existing per-VM control socket over HTTP and MCP, **without adding a daemon, a new binary, or a persistent background service**.

## Non-goals

- A system-wide always-on daemon (`coved`). Out of scope — our model is per-VM process, not host service.
- A separate `cove-serve` binary. Out of scope — same-binary subcommand keeps the brew install story simple.
- Rewriting the control protocol. The existing `proto/controlpb/` protobuf messages stay as the wire format; HTTP/MCP are transports.
- Changing the guest-agent path. vz-agent keeps speaking vsock-gRPC; it's downstream of the control socket.
- Multi-host clustering. Single-host only.

## The gap we're closing

Lume ships `lume serve` (HTTP on :7777) and `lume serve --mcp` (stdio MCP). Both are subcommands on the `lume` binary; both terminate when the user quits. They share the in-process `LumeController` with all the other subcommands.

See `libs/lume/src/Commands/Serve.swift`, `libs/lume/src/Server/HTTP.swift`, `libs/lume/src/Server/MCPServer.swift`.

Cove today has none of this. Our control socket is Unix-only and per-VM. AI agents and Node.js tooling that can't dial Unix sockets directly have no way in.

## Architecture: three shapes, one binary

All three modes live as subcommands on the existing `cove` binary. No new binary.

### 1. Per-VM embedded HTTP (`cove run -http`)

The `cove run` process already owns a Unix listener (`control_socket.go:192`). Add a second listener, optional, bound to TCP:

```
cove run -http 127.0.0.1:7777
cove run -http :0                      # auto-pick a free port, print it
cove run -http-token-file ~/.cove/api.token
```

Both listeners go through the same handler dispatch — the existing `ControlServer.handleCommand` routine. Unix stays the canonical transport (zero-config, per-VM token); HTTP is the opt-in network-accessible alternative.

Why this is default-off: we don't want a random `cove run` to open a TCP port on someone's machine.

### 2. Multi-VM gateway (`cove serve`)

A new subcommand — dedicated long-running process that doesn't own any VM, but reads the VM registry at `~/.vz/vms/` and proxies each VM's Unix socket under a `/vms/<name>/` prefix.

```
cove serve                              # default :7777, localhost
cove serve -http 127.0.0.1:7777
cove serve -listen tcp://:7777          # unixgram:// also allowed for API-over-socket
cove serve -token-file ~/.cove/api.token
cove serve -per-vm-auth                 # strict mode: require per-VM token
cove serve -vms vm1,vm2                 # optional allowlist
```

Startup sequence:
1. Enumerate `~/.vz/vms/*/control.sock` that exist and are listening.
2. For each, read `~/.vz/vms/<name>/control.token` for per-VM auth (used only in `-per-vm-auth` mode).
3. Load or generate the gateway master token at `~/.vz/gateway.token`.
4. Start HTTP listener, route `/vms/<name>/...` to the corresponding Unix socket.
5. Watch `~/.vz/vms/` for new VMs starting up (fs notify); hot-add routes.

Why this makes sense: a single HTTP endpoint that addresses all running VMs is exactly what CI systems, MCP clients, and browser dashboards want.

### 3. MCP stdio (`cove serve --mcp`)

Same `cove serve` subcommand, different transport. When `--mcp` is set, we bind stdio instead of TCP and speak Model Context Protocol.

```
cove serve --mcp
```

Tools exposed (initial set, matching lume's MCP surface):
- `list_vms`
- `run_vm(name, options)`
- `stop_vm(name)`
- `screenshot(name)` → base64 PNG
- `exec(name, cmd, args)` → stdout/stderr/exit
- `agent_read(name, path)` / `agent_write(name, path, data)`
- `status(name)` → state, capabilities
- `vzscript_run(name, recipe)`

Important: MCP mode writes *nothing* to stdout except MCP protocol framing (matching `Serve.swift:25`). All logging goes to stderr.

## HTTP API surface (v0.1)

Thin CRUD mapping onto the existing control commands. Body is JSON; response is JSON or binary (for screenshots). The v0.1 surface groups cleanly into three areas: lifecycle, guest agent, and creation.

### Lifecycle

```
GET    /healthz                                 # 200 OK, no auth
GET    /v1/vms                                  # list VMs + states
GET    /v1/vms/:name/status                     # state, capabilities
POST   /v1/vms/:name/pause
POST   /v1/vms/:name/resume
POST   /v1/vms/:name/stop                       # {"force": false}
GET    /v1/vms/:name/screenshot                 # image/png
POST   /v1/vms/:name/type                       # {"text": "..."}
POST   /v1/vms/:name/key                        # {"code": N, "modifiers": [...]}
POST   /v1/vms/:name/mouse                      # {"x": .., "y": .., "click": true}
```

### Guest agent

```
POST   /v1/vms/:name/agent/exec                 # {"cmd": "...", "args": [...], "as_user": false}
GET    /v1/vms/:name/agent/read?path=/foo       # file body
POST   /v1/vms/:name/agent/write                # {"path": "...", "data": "base64..."}
POST   /v1/vms/:name/agent/cp                   # {"src": "host", "dst": "guest"}
```

### Creation

```
POST   /v1/vms                                  # create VM; returns 202 + operation
                                                # body: {"name": "...", "installer": {...}, "cpu": 4, "memory_gb": 8, ...}
```

`POST /v1/vms` is async — returns `202 Accepted`. See "Long-running operations" below.

### Snapshots & events

```
POST   /v1/vms/:name/snapshot                   # {"name": "checkpoint1"}
GET    /v1/vms/:name/snapshots
POST   /v1/vms/:name/snapshots/:snap/restore
DELETE /v1/vms/:name/snapshots/:snap

GET    /v1/vms/:name/events                     # SSE stream of state changes
```

Still deferred to v0.2: image pull/push over HTTP (large binary transfers, registry semantics; CLI stays primary).

## Long-running operations

Operations that take more than ~5 seconds (VM creation today; import, clone, snapshot-heavy restore, and future image pull in v0.2) use a Google-Cloud-style long-running-ops pattern.

**Flow:**

1. Client submits: `POST /v1/vms` with create payload.
2. Server enqueues work, responds immediately:
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
3. Client polls or streams for progress.

**Endpoints:**

```
GET    /v1/operations/:id                       # current state snapshot
                                                # status: pending|running|succeeded|failed
                                                # progress: {"phase": "download_ipsw", "percent": 42}
                                                # result: {...} when succeeded
                                                # error:  {...} when failed

GET    /v1/operations/:id/events                # SSE stream of progress events
                                                # emits {phase, percent, message} until terminal
GET    /v1/operations                           # list recent operations
```

Operations persist to disk across `cove serve` restart. Each operation is written to `~/.vz/operations/<op_id>.json` with write-through on every state change (pending → running → phase updates → terminal). On `cove serve` startup, the operations store reloads existing files and re-indexes them; terminal operations are retained for 1 hour then GC'd, in-flight operations that had no owning process on reload are marked `failed` with a "server restarted mid-operation" error.

This pattern applies to `create_vm` in v0.1 and preemptively to `import`, `clone`, and `image pull` when they land.

### Durability

The operations store uses **write-temp-then-rename + fsync parent dir** on every state transition, the same pattern used for `disk.img.partial` elsewhere in the codebase:

1. Marshal the operation snapshot to JSON.
2. Write to `~/.vz/operations/<op_id>.json.tmp` and `fsync` the file.
3. `rename(2)` onto the final path (atomic within the same filesystem).
4. On darwin, open `~/.vz/operations/` and `fsync` the directory fd so the rename is durable.

Why this matters: `cove serve` crashing — or, more realistically, `brew upgrade cove` restarting the gateway — midway through a 15GB IPSW download would otherwise leave either (a) a half-written `op_abc.json` that the reloader parses as corrupt JSON and returns `500` for every subsequent `GET /v1/operations/op_abc`, or (b) no file at all, so the client polls and gets `404`, assumes the operation is lost, and issues a duplicate `POST /v1/vms` — now two 15GB downloads racing into the same VM directory. Atomic rename means the reader sees either the pre-transition state or the post-transition state, never a torn write. Fsync-parent on darwin ensures the rename survives a kernel panic or power loss, not just a process crash.

Implementation lives in `control_operations_store.go` (~90 LOC total, half a day).

## Auth

**Default: gateway master token stored in the macOS keychain.** On first `cove serve` startup, if no master token exists in the keychain, we generate one (32 random bytes, hex-encoded) and store it as a generic password item with service `cove-gateway`, account `$USER`, and a human-readable description — exactly:

> `cove gateway — grants full access to all local VMs`

The description is visible on every macOS keychain access prompt. A user who sees an unexpected access dialog (some other tool trying to read the gateway token) has a fighting chance to notice and refuse. Every authenticated request carries `Authorization: Bearer <master-token>`.

Commands used:

```
# Store (on first serve or rotation):
security add-generic-password -s cove-gateway -a $USER -w $TOKEN \
  -j "cove gateway — grants full access to all local VMs" -U

# Retrieve:
security find-generic-password -s cove-gateway -a $USER -w
```

**File fallback for CI.** Passing `-token-file <path>` switches to file-backed storage at that path (`0600`). This is the supported path for CI runners and headless environments where the login keychain isn't unlocked. When the keychain is unavailable for any other reason (e.g. running outside a GUI login session), `cove serve` falls back to `~/.vz/gateway.token` automatically and emits a one-line stderr warning:

> `cove serve: storing master token as file (no keychain); pass -token-file for CI-expected file storage or run as GUI session for keychain`

**Opt-in: per-VM tokens.** Passing `-per-vm-auth` flips the gateway into strict mode: each `/v1/vms/<name>/...` route requires that VM's own token from `~/.vz/vms/<name>/control.token`. Useful for multi-tenant setups or when you want to hand one token to one automation without exposing the others.

- `cove run -http` reuses the per-VM token (no gateway involved).
- `cove serve -token-file <path>` overrides storage to a file at `<path>` (still master-mode unless `-per-vm-auth` is also set).
- `localhost` binding by default. No `0.0.0.0` unless explicit.
- No TLS in v0.1. Document that `cove serve` is for localhost or trusted-network use; remote access goes through SSH tunnels.

Rationale for master-default: Council round-1 noted that per-VM-by-default makes scripting painful (client has to discover and rotate N tokens). Master-token matches what users actually do with Docker, Podman, and lume. Per-VM stays available for the paranoid case.

### Multi-user hosts — read this before deploying to a shared Mac

> **Multi-user hosts.** Master-token default is correct for single-user laptops. For shared Mac minis, CI runners with multiple users, or any host where >1 UID can read the keychain, pass `-per-vm-auth` to force per-VM tokens. Master token grants full `agent_exec` inside every running VM; treat it like your SSH private key.

## File layout

```
control_socket.go            # existing — keep as-is
control_http.go              # NEW — http.Handler over ControlServer dispatch
control_mcp.go               # NEW — MCP stdio handlers, reuses control_http logic
control_operations.go        # NEW — long-running op registry + SSE broadcaster
control_operations_store.go  # NEW — atomic JSON persistence (write-temp-then-rename + fsync parent)
serve.go                     # NEW — `cove serve` subcommand
serve_gateway.go             # NEW — multi-VM proxy + VM discovery
serve_test.go                # NEW — http.httptest coverage, MCP golden tests
```

No new packages. Everything lives next to existing control plumbing.

## Observability

- Structured logs via the existing logger, one line per request with method + path + vm + status + duration.
- `/metrics` (Prometheus text format) behind a flag in v0.2, not v0.1.
- Request IDs propagated into the control socket's command log for correlation.

## Compatibility & rollout

- Both subcommands are additive. Zero effect on existing `cove` users.
- Feature-flagged behind the flag itself: no `-http` = no TCP listener. `cove serve` must be invoked explicitly.
- Document in `docs/reference/http-api.md` + `docs/features/mcp.md`. Shipping docs in the same PR.

## Risks & mitigations

| Risk | Mitigation |
|---|---|
| TCP port exposure on laptop | Default localhost; `0.0.0.0` requires explicit address; no auto-start |
| Token leakage via env/log | Token only in `Authorization` header; redact in logs |
| MCP stdout pollution breaks clients | Strict stderr-only logging in MCP mode, tested in CI |
| VM socket churn (start/stop) | Gateway watches `~/.vz/vms/`, hot-reloads routes |
| SSE stream holds file descriptors | Connection ceiling + keepalive timeout |
| HTTP transport drift from Unix | Single dispatch function; transport layers are thin |
| Master token = full host access | Document clearly; `-per-vm-auth` for least-privilege; `0600` perms on token file |
| Master token grants access to all VMs | Keychain-stored; description "cove gateway — grants full access to all local VMs" visible on every macOS access prompt |
| Corrupt partial operation JSON on crash | write-temp-then-rename + fsync parent dir |

## Estimated scope

| Piece | LOC | Days |
|---|---|---|
| `control_http.go` handler + routes | ~250 | 1 |
| `control_operations.go` + create_vm wiring | ~150 | 1 |
| `control_operations_store.go` (atomic JSON persistence) | ~90 | 0.5 |
| `serve.go` + `serve_gateway.go` | ~200 | 1 |
| `control_mcp.go` (mcp-go library) | ~200 | 1 |
| Tests (httptest + MCP golden + op lifecycle) | ~250 | 1 |
| Docs: http-api.md, mcp.md, examples | ~250 | 0.5 |
| **Total** | **~1390** | **~6 days** |

## Open questions for Council

### Resolved in round-1

- **Gateway token model** → master-token default, `-per-vm-auth` opt-in (settled via user interview).
- **Discovery mechanism** → fs-notify on `~/.vz/vms/` (settled).
- **SSE vs WebSocket for events** → SSE (settled; WS only if bidirectional need emerges in v0.2).
- **MCP tool namespace** → flat names, matching lume (settled).
- **Launch priority** → ship HTTP + MCP together; MCP reuses the HTTP dispatch (settled).
- **Brew cask upgrade path** → no changes needed to `dist/homebrew/cove.rb` (settled).
- **v0.1 scope includes `create_vm`** → yes, behind the long-running-ops pattern (settled).

### Still open

1. **`create_vm` installer options schema** — mirror the `cove install` CLI flags 1:1, or introduce a more structured JSON shape (installer.kind, installer.source, installer.provisioning)? Leaning structured, but want Council opinion.
2. **Rate limiting** — do we need any per-token QPS caps in v0.1, or is that premature for a localhost-default surface?

12. **Shared-host story.** Single-user assumption holds for dev laptops. First CI customer running parallel `cove serve` instances against the same `~/.vz/` will force a decision: (a) file-lock the VM registry, (b) per-user socket paths, (c) daemonize to `coved`. Pick when the first report lands, not now.
