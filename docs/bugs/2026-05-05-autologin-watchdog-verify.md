# Auto-login watchdog verification

Status: pass
Date: 2026-05-05

## VM

- VM: `mlxgo-fresh-headed2-20260505`
- Starting state: running, no host-side
  `~/.vz/vms/mlxgo-fresh-headed2-20260505/autologin.json`
- Existing disk-side provisioning was left intact; the VM was not
  re-provisioned.

## Cache Seed

The host-side cache was seeded to simulate the file that fixed
`cove provision -apply` now writes:

```sh
umask 077
cat > ~/.vz/vms/mlxgo-fresh-headed2-20260505/autologin.json <<'JSON'
{"Username":"mlxqa","Password":"mlxqa123"}
JSON
chmod 600 ~/.vz/vms/mlxgo-fresh-headed2-20260505/autologin.json
```

The resulting file mode was `0600`.

## Run Evidence

Command:

```sh
./cove -vm mlxgo-fresh-headed2-20260505 run -gui -verbose 2>&1 | tee /tmp/r41-autologin-watchdog-run.log
```

Key lines:

```text
[login-watchdog] cached credentials loaded for mlxqa
=== Login screen detected — typing cached password ===
Detected login screen - attempting keyboard login...
[typeText] typing 8 chars: "mlxqa123"
Login successful - reached desktop
Guest agent: connected
```

Follow-up status:

```text
$ ./cove -vm mlxgo-fresh-headed2-20260505 ctl agent-status
{
  "daemon": "connected",
  "guiSession": {
    "seat": "console",
    "type": "console",
    "user": "mlxqa"
  },
  "summary": "daemon connected; GUI session active (user=mlxqa, console)",
  "user": "connected"
}
```

Screenshot evidence was captured at
`/tmp/r41-autologin-watchdog-desktop.jpg`.

## Result

The existing stuck VM reached a logged-in `mlxqa` console session without QA
typing the password manually. This verifies that the missing host-side
credential cache was the practical blocker for split `provision -apply` followed
by `run -gui`.
