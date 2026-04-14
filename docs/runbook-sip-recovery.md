# SIP Recovery Runbook

## Goal

Automate `csrutil enable` / `csrutil disable` from Recovery Terminal with prompt-aware input handling.

## Generate Automation

Disable SIP with password prompt handling:

```sh
cove -vm macos-3 sip disable-auto -password '<PASSWORD>'
```

If your Recovery build asks for explicit confirmation (`Are you sure`), include:

```sh
cove -vm macos-3 sip disable-auto -password '<PASSWORD>' -confirm
```

Enable SIP:

```sh
cove -vm macos-3 sip enable-auto -password '<PASSWORD>'
```

## Execute Automation

```sh
cove -vm macos-3 run -recovery -no-resume -gui -unattended \
  -usb "/Users/tmc/.vz/vms/macos-3/recovery-disk.img" \
  -boot-commands "/Users/tmc/.vz/vms/macos-3/sip-disable.vzscript"
```

## Prompt Handling

Generated scripts use `rsc.io/script` conditions plus explicit prompt progression:

- `[text-visible:Authorized+user] type '...'`
- `[text-visible:Password] type-keycodes '...'`
- `[text-visible:Password] wait-prompt-clear 'Password'`

This avoids hardcoding one prompt order while keeping the script syntax shell-like.

## Verify After Reboot

```sh
cove -vm macos-3 sip status
```

## Troubleshooting

- `Utilities -> Terminal` fails intermittently:
  - Use the single-step menu command path (`clickMenuItem` / `ctl click-menu`) instead of separate clicks.
- `Authentication failure. (additional failure: User interaction required.)`:
  - This is usually a VM account-state problem, not an OCR or prompt-ordering bug.
  - The target user must be created through the bootstrap-recovery flow so it gets full recovery authorization.
  - Confirm provisioning actually completed inside the guest. A staged `.provision/` directory on the host is not enough; the guest should have `/var/db/.vz-provisioned` and `/var/log/vz-provision.log`.
  - The guest provisioning run must also complete `diskutil apfs updatePreboot /`.
  - If those markers are missing, reprovision the VM and let the guest-side provision LaunchDaemon finish before testing `csrutil` again.
- `shared-folders-apply: unknown command type`:
  - Running VM was started with an older control server binary; restart VM with current build.
- `agent unavailable`:
  - Guest agent is not running (expected in Recovery); guest-side checks and mounts are unavailable there.
