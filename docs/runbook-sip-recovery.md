# SIP Recovery Runbook

## Goal

Automate `csrutil enable` / `csrutil disable` from Recovery Terminal with prompt-aware input handling.

## Generate Automation

Disable SIP with password prompt handling:

```sh
vz-macos -vm macos-3 sip disable-auto -password '<PASSWORD>'
```

If your Recovery build asks for explicit confirmation (`Are you sure`), include:

```sh
vz-macos -vm macos-3 sip disable-auto -password '<PASSWORD>' -confirm
```

Enable SIP:

```sh
vz-macos -vm macos-3 sip enable-auto -password '<PASSWORD>'
```

## Execute Automation

```sh
vz-macos -vm macos-3 run -recovery -no-resume -gui -unattended \
  -usb "/Users/tmc/.vz/vms/macos-3/recovery-disk.img" \
  -boot-commands "/Users/tmc/.vz/vms/macos-3/sip-disable-commands.txt"
```

## Prompt Handling

Generated scripts use conditional commands:

- `typeAndReturnIfText "Enter password|..."`
- `typeAndReturnIfText "Are you sure|y"`

This avoids hardcoding one prompt order.

## Verify After Reboot

```sh
vz-macos -vm macos-3 sip status
```

## Troubleshooting

- `Utilities -> Terminal` fails intermittently:
  - Use the single-step menu command path (`clickMenuItem` / `ctl click-menu`) instead of separate clicks.
- `shared-folders-apply: unknown command type`:
  - Running VM was started with an older control server binary; restart VM with current build.
- `agent unavailable`:
  - Guest agent is not running (expected in Recovery); guest-side checks and mounts are unavailable there.
