# cove v0.1.1 — release notes

Patch release bundling four fixes from the v0.1.0 end-to-end smoke
test (2026-04-26) plus a documentation correction on the TCC routing
story. No new features. No CLI breaking changes.

## What's fixed

### `cove serve` gateway now registers routes for legacy-symlinked VMs

In v0.1.0, `GET /v1/vms` listed VMs surfacing under `~/.vz/vms/<name>`
as symlinks pointing at peer dirs (`~/.vz/<name>`), but the per-VM
proxy endpoint `/v1/vms/<name>/*` returned 404 for those same VMs.
Both the initial gateway scan and the configured-VM discovery used
`os.DirEntry.IsDir`, which returns false for symlinks. Switching
both scans to `os.Stat` follows symlinks and registers the routes.

Affects users on the legacy `~/.vz/<name>/` layout (which the v0.1.0
release notes called out as the migration path). Clean v0.1.0+
installs were unaffected.

### `cove ctl operations get/wait/list` renders proto Result fields

The control socket returned full `OperationInfo` payloads on the
wire, but the CLI default-rendered them as just `OK`. `-raw`
revealed the dropped fields. v0.1.1 adds a typed renderer for both
single-op responses (id, status, resource, created, updated, error
code/message when set) and list responses (tabular ID / STATUS /
RESOURCE / UPDATED). Legacy `resp.Data` rendering is preserved as
the fallback for older commands.

### `cove-helper` LaunchDaemon plist hardened against respawn churn

The v0.1.0 plist set `KeepAlive=true` unconditionally with no
`ThrottleInterval`, so a helper that crashed (e.g. a stale binary
from a pre-`bc89dc2` install hitting a read-only-fs `mkdir` on the
pre-existing-VM ensure path) respawned at the launchd default of
~6/min. Symptoms: menu-bar icon flicker, `/var/log/cove-helper.log`
filling with create-VM errors. v0.1.1:

- `KeepAlive` becomes `{SuccessfulExit: false}`, so a daemon that
  exits cleanly is not bounced; only crashes trigger respawn.
- `ThrottleInterval=30` caps respawn rate to once every 30s.
- `cove helper status` SHA256-compares the running cove binary with
  the file installed at `/usr/local/libexec/cove-helper`. A mismatch
  prints `stale` plus the exact remediation: `re-run sudo cove
  helper install to refresh`.

If you are running a v0.1.0 helper, install v0.1.1 and run
`sudo cove helper install` once to pick up the new plist.

The helper daemon also gains structured `log/slog` output. Each line
in `/var/log/cove-helper.log` now carries `component=cove-helper`
plus per-request `peerUid`, `op`, and `manifestBytes` fields, and
every rejection path (peer-UID mismatch, decode error, malformed
manifest) is logged at `WARN` instead of being silent. Set
`COVE_HELPER_LOG_JSON=1` in the LaunchDaemon plist environment for
JSON output if you forward the log to a parser.

### Documentation: TCC routing is scaffolding, not a complete fix

The v0.1.0 release notes described path-aware TCC routing as the
fix for VirtioFS access from the agent. Empirically (v0.1.0 smoke
test, 2026-04-26 on a live VM with five mounted shares), the routing
scaffolding shipped correctly but the user agent itself still hits a
TCC gate on `readdir` of `/Volumes/<share>`: TCC's Full Disk Access
is granted per-binary, not per-session, so a LaunchAgent running in
the user's Aqua session does not automatically inherit FDA.

`stat` succeeds (4.5ms) but `readdir` times out — the user-visible
shape of the gap.

v0.1.1 corrects this story:

- The v0.1.0 release notes now describe the routing as scaffolding,
  with a pointer to the FDA grant requirement.
- `docs/research/tcc-via-user-agent.md` now documents the empirical
  result, why per-binary FDA blocks the close-of-#21 path, and a
  manual workaround: System Settings → Privacy & Security → Full
  Disk Access → add `/usr/local/bin/vz-agent` (inside the guest).
- Follow-ups (FDA prompt during `cove up`, signing-identity bundle,
  `cove doctor` probe) are tracked but not landed.

If you tagged v0.1.0 and want VirtioFS access from `cove ctl
agent-exec`, the manual FDA grant is the supported path today.

## What's not in v0.1.1

- No FDA-prompt-on-first-run or `cove doctor` TCC probe. Both are
  scoped to a later release.
- No re-tag of v0.1.0; the v0.1.0 release notes are amended in
  place to call routing "scaffolding".

## Install

```bash
brew upgrade cove
# or
go install github.com/tmc/cove@v0.1.1
```

If you have a v0.1.0 helper installed:

```bash
sudo cove helper install
```

(The new `cove helper status` will print `stale` until you do.)
