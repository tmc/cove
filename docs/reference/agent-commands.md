---
title: Agent Commands
---
# Agent Commands

`cove exec` runs a command in a running VM through the guest agent:

```bash
cove exec ubuntu uname -a
cove exec -it ubuntu bash
cove exec -e CI=1 -w /work ubuntu go test ./...
```

`cove ctl exec` and `cove ctl agent-exec` expose the lower-level control
socket path. Use them for direct control-socket automation.

## argv Size Limit

Agent exec refuses oversized argv payloads before sending the request to the
guest. The default limit is 16 KiB total across all argv strings. Override it
with `COVE_AGENT_EXEC_ARGV_LIMIT=<bytes>` when a larger command line is
intentional.

Large blobs do not belong in argv. Use one of these paths instead:

- `cove ctl agent-cp <host-path> <guest-path>` for files.
- `cove ctl agent-write <guest-path> <data>` for small generated content.
- Pipe via stdin when the command supports stdin.

The refusal is intentional: argv truncation or corruption must not be silent.
