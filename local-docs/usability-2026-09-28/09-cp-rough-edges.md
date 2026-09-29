# `cove cp` rough edges

Status: FIXED 22503af1

- Directory copy to an existing destination reports success and copies
  nothing (agent_control.go:854-868; cp.go never sets Overwrite and
  discards resp.Data). Add -f; fail otherwise.
- No output or progress; progress logs go to the cove run process
  (internal/agent/client.go:582).
- Control connection deadline is 5 min (internal/control/server.go:198)
  while copies may run 10-30 min; client timeout 10 min (cp.go:229) vs
  server 30 min for dirs.
- Guest->host: no directories; `cove cp vm:/f .` fails ("is a directory");
  failed download leaves a truncated file.
- Host->guest `vm:/tmp/` fails; want docker-cp semantics (append basename,
  N sources + dir).
- `vm:~/...` rejected (cp.go:210).
- Dir copy stages /tmp/vz-cp-<base>.tar: fixed name collides, doubles disk.
- guest-write (vzscript.go:630) base64s the whole file into one JSON line;
  control scanner caps at 1 MiB -> fails around 750 KB.

## Resolution

1. Added `-f` / `--force` flags to `cove cp` and passed `Overwrite: force` in copy requests. Enforced in `handleAgentCopyDir` and `handleAgentCopyDirFromGuest` by failing with `destination "..." already exists (use -f to overwrite)` if destination exists and overwrite is false.
2. Printed copy summary message (`resp.Data` or `agentFile.Message`) to stdout in `cove cp`.
3. Extended control client timeout to 30 min in `cmd/cove/cp.go`, and cleared the control socket deadline (`conn.SetDeadline(time.Time{})`) during request handling in `internal/control/server.go`, restoring a 5 min idle deadline after each request completes.
4. Implemented guest-to-host directory copying (`handleAgentCopyDirFromGuest`) by archiving guest directory to a temporary tar, streaming to host, and extracting with `--strip-components=1`. Cleaned up truncated host files on failed file copy in `internal/agent/client.go` and `cmd/cove/agent_control.go`.
5. Supported Docker-cp semantics: trailing slash destination appends source basename (`vm:/tmp/` or host destination `.` / `dir/`).
6. Supported `vm:~` and `vm:~/...` paths in `parseCpOperand` and expanded `~` against the guest console/target user's home directory in `cmd/cove/agent_control.go`.
7. Used randomized nonces and timestamps in staging tar filenames (`/tmp/vz-cp-%s-%d-%08x.tar`) to prevent collisions.
8. Increased control scanner buffer max capacity from 1 MiB to 16 MiB (`16*1024*1024`) in `internal/control/server.go`.
9. Added comprehensive unit tests in `cmd/cove/agent_control_test.go`, `cmd/cove/cp_test.go`, `internal/agent/client_test.go`, and `internal/control/server_test.go`.

