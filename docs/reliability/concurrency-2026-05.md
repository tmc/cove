# Concurrency Sweep

Date: 2026-05-05

Code scanned: `3456d67c4eba48024559dff7b809400fef8ec16d`

Command:

```
go test -race ./...
```

Result: pass.

The first race run found three package-main races:

- `authorization_test.go`: the AuthorizationCreate stub restore path rewrote
  global function variables while the watchdog goroutine could still read
  `authCreate`.
- `linux_shell_test.go`: the recording signaler test double appended to and
  read its call slice from different goroutines.
- `control_socket.go`: `ControlServer.Stop` cleared `lifecycleCtx` while the
  lifecycle policy monitor read it.

All three were fixed before the passing full race scan. No remaining race
reports were observed in `ITerm2Proxy`, fleet, daemon, image cache, or the
rest of the package set covered by `go test -race ./...`.
