# cove shell — Docker-shaped exec UX

**Status**: accepted planning input.
**Source**: [/tmp/cove-loop-roadmap-cursor.md](../../tmp/cove-loop-roadmap-cursor.md)
plus the linux-shell-host T3 step 2 pre-flight findings (the sub-agent
that landed [`63d3234`](../../) and called out the vsock-ownership
constraint before writing client code).
**Roadmap**: candidate v0.2.1 (Slices 1–2 reuse the v0.2 unary RPCs) or
v0.3 (Slice 3 ships the proto bidi extension). Cite both; let the
roadmap pick.
**Branch**: planning.

## Goal

`cove shell <vm>` is a Docker-shaped subcommand — same UX as
`docker exec -it <container> bash` — that runs from any terminal
regardless of which process is hosting the VM. The v0.2 in-process
[`cove run -linux -shell`](../../linux_shell.go) flag landed in
`63d3234` is the precursor: it proved the PTY allocation, SIGWINCH /
SIGINT plumbing (`fb7bce2`), and the unary `ResizeExecTTY` /
`SignalExec` RPCs work end-to-end. This design ships the standalone,
cross-process UX the in-process flag explicitly defers to.

## Why this is non-trivial — vsock ownership

The linux-shell-host pre-flight surfaced an architectural invariant:

- The guest agent is reachable only via `VZVirtioSocketDevice` on the
  running VM. Dialing vsock requires a `VZVirtualMachine` instance
  reference (`agent_control.go:160`, `agent_control.go:282`).
- Only the cove process that called `initWithConfiguration:queue:`
  holds that instance. A second `cove shell <vm>` process **cannot**
  open vsock to the same guest — Virtualization.framework does not
  expose the socket device to other processes.
- The existing cross-process bridge is the per-VM control socket at
  `~/.vz/vms/<name>/control.sock` (`control_socket.go:48`, auth at
  `control_socket.go:728`).

Therefore `cove shell <vm>` must broker through the control socket;
the VM-owning process forwards to the in-process agent client. Same
shape `cove ctl` uses for unary commands, extended to bidi streams.

## Architecture options

The pre-flight identified three options. This design picks one.

- **Option B — Control-socket protocol extension** (recommended).
  Add three command types to the JSON-line protocol
  (`agent_control.go:341` `handleAgentCommand`):
  `agent-exec-attach` (server sends stdout + exit; client sends
  stdin frames), `agent-exec-resize`, `agent-exec-signal`. Stdin
  sub-channel: `{"type":"stdin","exec_id":"…","data":"<base64>"}`
  frames on the same connection. The VM-owning cove process
  forwards each to the in-process `internal/agent` client. Pro:
  reuses the existing socket, `control.token` auth, and the
  `control_http.go` gateway. Con: protocol gets bigger; concurrent
  clients need coordination (below).
- **Option C — HTTP gateway routes** via `cove serve`. Wire
  `POST /v1/vms/{name}/agent/exec/attach`, `…/{id}/resize`,
  `…/{id}/signal` through `control_http.go:58` (unary
  `agent/exec` already lives there). Pro: aligns with existing
  Docker-shaped URLs. Con: requires `cove serve` to be running;
  awkward for a one-shot CLI.
- **Option B' — Hybrid**. Control socket for control + a separate
  Unix socket per exec for the stdin/stdout byte stream. Pro: clean
  byte-stream separation. Con: more sockets, fiddlier teardown.

**Recommendation: Option B.** The pre-flight leaned this way; the
JSON-line protocol already carries every other VM operation; base64
framing is fine at human-typing rates. Option C is a follow-on for
`cove serve` users (deferred). Option B' adds ops complexity for a
marginal byte-budget win.

## Bidi stdin: the v0.3 proto dependency

`proto/agent.proto:21` is `rpc ExecStream(ExecRequest) returns
(stream ExecOutput);` — server-streaming only. Three options:

1. Change `ExecStream` to bidi. Backwards-incompatible.
2. Add `rpc ExecAttach(stream ExecAttachRequest) returns (stream
   ExecOutput);` — first request carries exec params, subsequent
   carry stdin. Additive; legacy clients keep using `ExecStream`.
3. Separate `rpc StdinStream(stream StdinChunk) returns (StdinAck);`
   running parallel to `ExecStream`. Two streams per exec.

**Recommendation: option 2 (`ExecAttach`).** Additive; one stream per
exec keeps the client simple. Slice 3 is the v0.3 proto bump.

## Multi-client coordination

Two `cove shell <vm>` clients on one VM: each invocation creates a
new exec session (fresh shell, new `exec_id`). No multiplexing in
v1. The agent's `activeExec` map already keys on `exec_id`, so
multiple sessions coexist today. Same behaviour as `docker exec`.

## Slice 1 — control-socket extension (~150–200 LOC)

Add the three command types to `control_socket_commands.go` and
dispatch in `agent_control.go:341`. The VM-owning cove process
forwards each:

- `agent-exec-attach`: open `internal/agent.ExecStreamControl` with
  `tty=true`; pump `ExecOutput` frames back; read
  `{"type":"stdin",…}` frames and push them to the PTY master fd
  once Slice 3 is in.
- `agent-exec-resize` → `ResizeExec` (already wired,
  `linux_shell.go:81`).
- `agent-exec-signal` → `SignalExec`.

Slice 1 wires stdin to /dev/null (matches the v0.2 limitation in
`linux_shell.go:6`). Plumbing is end-to-end; bidi stdin waits for
Slice 3. No client binary yet. Tests reuse `control_socket_test.go`.

## Slice 2 — `cove shell` client (~100–150 LOC)

New subcommand `cove shell <vm> [-- <args>]`. Default `bash -l`
(matches `linuxShellCommand` in `linux_shell.go:29`).

1. Resolve `<vm>` to `~/.vz/vms/<vm>/control.sock`; load
   `control.token`.
2. Open the socket; send `agent-exec-attach`.
3. `term.MakeRaw(stdin)`; `signal.Reset(SIGINT) + Notify` per the
   `fb7bce2` pattern (so Ctrl-C goes to guest, not host); SIGWINCH
   → `agent-exec-resize`.
4. Pump stdin (Slice 3) and stdout. Restore TTY on exit.
5. Friendly errors: VM not running → "no running VM at <name>";
   auth fail → "control token mismatch"; agent unreachable →
   "guest agent not responding".

Tests reuse `linux_shell_test.go` and
`integration_linux_shell_test.go` patterns.

## Slice 3 — proto bidi extension (~50 LOC + generated)

`proto/agent.proto` gets a new RPC:

```proto
rpc ExecAttach(stream ExecAttachRequest) returns (stream ExecOutput);

message ExecAttachRequest {
  oneof request {
    ExecRequest start = 1;   // first message
    bytes stdin = 2;          // subsequent messages
  }
}
```

Existing `ExecStream` stays. The agent advertises `ExecAttach` via
a new `features` list on `InfoResponse`. v0.2 client + v0.3 agent →
fall back to `ExecStream`. v0.3 client + v0.2 agent → fall back to
`ExecStream`, degrade to read-only stdin (today's behaviour), print
a warning. Tests cover both directions.

This slice is the v0.3 proto bump; it makes `cove shell` a true
interactive shell rather than a tail.

## Failure rules

- VM not running → control-socket dial fails fast, names the VM.
- Auth token mismatch → reject before opening the exec.
- Agent unreachable (vsock dial fails in the VM-owning process) →
  control-socket returns `error: agent not ready`; client surfaces
  as "guest agent not responding (still booting?)".
- `exec_id` collision → reject second `agent-exec-attach` (UUIDs
  make this unreachable; agent still defends).
- Mid-stream agent disconnect → client restores TTY, exits
  non-zero with a brief diagnostic. No silent hangs.

## Tests

| Slice | Tests |
|---|---|
| 1 | Control-socket integration: a fake VM-owning cove process serves `agent-exec-attach` and forwards to a stub `internal/agent` client. Asserts framing, resize forwarding, signal forwarding, and clean teardown. |
| 2 | Client-side unit tests for arg parsing, signal-handler install/restore (mirrors `linux_shell_test.go`), and a control-socket integration test that drives `cove shell` against the Slice 1 fake. |
| 3 | Proto compat: v0.2 client (`ExecStream` only) against v0.3 agent works; v0.3 client (`ExecAttach`) against v0.2 agent falls back to `ExecStream` with a warning. |

## Non-goals

- **Multi-client multiplexing.** v2 of this design.
- **Daemon-mode (`docker exec -d` analogue).** Always interactive.
- **`docker cp` parity.** `cove cp` is a separate design.
- **Remote-host `cove shell`.** Always local control-socket; remote
  is a `cove serve` concern (Option C, deferred).
- **macOS guest support in v1.** `cove shell <vm>` works for any guest
  whose agent supports `ExecAttach`; the shell command default
  (`bash -l`) is Linux-shaped, but `--` overrides for macOS.

## Acceptance gates

- **Slice 1**: control-socket commands `agent-exec-attach`,
  `agent-exec-resize`, `agent-exec-signal` work end-to-end against a
  running VM; tests green; no client-side binary required.
- **Slice 2**: `cove shell <vm>` connects, runs `bash -l`, and the
  user sees output. Stdin remains read-only (limitation inherited
  from `ExecStream`).
- **Slice 3**: `cove shell <vm>` is fully bidirectional; fallback
  works in both v0.2↔v0.3 directions.

## Open questions

1. **Reuse the `cove serve` HTTP gateway under the hood?**
   `cove shell` could speak HTTP to a long-running `cove serve`
   instead of the control socket directly. Recommendation: no —
   keep direct-to-socket; HTTP is Option C, a follow-on slice.
2. **Surface "VM stopped" vs "agent unreachable" cleanly.** Today
   the socket says `agent not ready` for both. Recommend a
   structured error code so the client can phrase each.
3. **Reuse `runLinuxShellSession` when the VM is hosted by the
   same `cove run` process?** Marginal win; recommend no — keep
   the path uniform.

## Handoff

Slice 1 is a v0.2.1 candidate (no proto bump). Slices 2–3 want the
v0.3 proto bump. Per [022](022-v04-anthropic-adapter.md)'s shape
decision (the standalone subcommand owns its own loop), `cove shell`
owns the host TTY/signal/stream loop end-to-end — not a thin wrapper
around `runLinuxShellSession`.
