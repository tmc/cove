# Copies into the guest run as root

Status: FIXED 941306a0

Cause: handleAgentCopy (cmd/cove/agent_control.go:826) always uses the root
daemon agent (vsock 1024). agentstate.RouteFor("cp", ...) exists
(internal/agent/routing.go:85) but is not called. handleAgentWrite already
routes to the user agent (agent_control.go:514-518).

Symptom: copies to ~/Desktop, ~/Downloads, ~/Documents fail with EPERM
(guest TCC) unless the agent has FDA; successful copies are root-owned and
the user can't edit/delete them. Directory copies extract with host UIDs.

Fix: streaming UserCopyIn/UserCopyOut on the UserAgent service
(proto/agent.proto) routed by path, or chown to the console user after
CopyIn.

## Resolution

1. Updated `agentstate.IsUserPath` in `internal/agent/routing.go` to recognize `~/` paths (e.g. `~/Desktop`, `~/Documents`) as user paths in addition to `/Users/<user>/...` and `/Volumes/...`.
2. Updated `handleAgentCopy` and `handleAgentCopyDir` in `cmd/cove/agent_control.go`:
   - Checks `agentstate.RouteFor("cp", cmd.GuestPath, linuxMode)`.
   - Logs `agent-route: cp ... -> user agent (TCC path)` at debug level when routing to user agent.
   - For `ToGuest = true`:
     - If route is `RouteUser` and user agent is available, stages the file to `/tmp` via daemon agent, chowns stage to console/target user if known, and executes via `ua.UserExec` to move/copy into the destination directory and set permissions. This inherits the user session's TCC context (allowing writes to `~/Desktop`, `~/Downloads`, etc.) and ensures the file is created with user ownership.
     - Fallback copies via daemon agent set file ownership to the target/console user when known or when copying into user paths.
   - For directory copy `handleAgentCopyDir`:
     - Checks user routing and uses user agent to extract the staged tar with `--no-same-owner --strip-components=1` into the destination, running under the user context.
     - When using daemon agent, passes `--no-same-owner` to `tar xf` and recursively chowns the directory to the console/target user so host UIDs are not preserved.
   - For `ToGuest = false` (copies from guest):
     - If route is `RouteUser` and user agent is available, stages the file to `/tmp` via `ua.UserExec` (which has user TCC access to read protected directories) before streaming out via the daemon agent.
3. Added unit tests in `cmd/cove/agent_control_test.go`, `cmd/cove/log_spam_test.go`, and `internal/agent/routing_test.go`.
