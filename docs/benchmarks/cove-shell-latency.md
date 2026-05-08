# `cove shell` roundtrip latency

`BenchmarkCoveShellRoundtrip` in `cove_shell_bench_test.go` measures the
host-side cost of one `cove shell <vm> -- <cmd>` invocation against an
in-process fake control socket. It exercises the production
`runShellSession` path: dial, ExecAttach handshake, frame pump,
teardown. No real VM, no vsock, no PTY.

## Scenarios

| Axis    | Values                                |
|---------|---------------------------------------|
| Phase   | `cold` (fresh server per iter), `warm` (one server, many attaches) |
| Payload | `stdout1B`, `stdout1KiB`, `stdout1MiB` (single stdout frame size) |

`cold` approximates first-command latency; `warm` approximates a hot
path (steady-state `cove shell` invocations against a long-running VM).
ExecAttach has no client-side multiplexing today (design 023), so each
warm iteration still pays one full attach.

## Run

```sh
go test -run='^$' -bench=BenchmarkCoveShellRoundtrip -benchtime=3s .
```

`b.SetBytes` reports MB/s for the stdout payload; `b.ReportAllocs`
reports allocations per attach.

## Reading the numbers

The fake server in `shell_test.go` sleeps 20 ms after sending the `done`
frame to give the client time to drain. That sleep is outside the
client's measured wall time (the client returns once it sees `done`),
so it does NOT inflate `ns/op`. If you change that drain in the test
harness, recheck this assumption.

The benchmark is dominated by:

1. unix socket dial + accept (cold path only)
2. one JSON request + one protojson `ControlResponse` per frame
   (attach + stdout chunk + done = 3 frames in the small case)
3. base64 + UTF-8 redaction over the stdout payload

For 1 MiB payloads the JSON+base64 path begins to dominate; that's the
expected shape, not a regression to chase. Cold-vs-warm delta isolates
the dial+accept cost.

## Live numbers

Pending on tested hardware. Capture with:

```sh
go test -run='^$' -bench=BenchmarkCoveShellRoundtrip -benchmem \
  -benchtime=3s . | tee docs/benchmarks/results-cove-shell.txt
```

Re-run on the same host before/after any change to `runShellSession`,
`pumpShellFrames`, or the agent control-socket framing.
