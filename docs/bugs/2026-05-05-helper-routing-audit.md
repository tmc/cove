# Helper routing audit

Date: 2026-05-05

## Report

QA verified no-sudo provisioning reaches the native admin dialog, but teardown
failed with a busy disk. Cove printed:

```text
Manual fix: hdiutil detach /dev/disk23 -force
```

That violates the helper principle: when `cove-helper` is installed and
running, cove should take the privileged action through the helper instead of
printing shell commands for the user to run.

## User-facing command suggestions

| Site | Message | Helper route |
| --- | --- | --- |
| `provision_mount.go` `detachDiskForPath` | `Manual fix: hdiutil detach <dev> -force` | Add helper `force_detach` and use it before printing fallback text. |
| `provision_mount.go` `checkDiskNotMounted` | `Detach with: hdiutil detach <dev> -force` / `./cove disk-detach` | Use helper force-detach in the automatic path; noninteractive fallback should mention helper status when available. |
| `block_device.go` | `run: sudo cove helper install` | Helper is not installed or stale, so there is no helper route yet. Keep as install guidance. |
| `elevated_run.go` manual elevation fallback | `sudo <cove> __elevated-op ...` | Only printed in restricted environments where native auth/helper cannot be used. Keep as fallback. |
| `agent_inject.go` generated restricted installer | `sudo hdiutil detach ...` inside a generated script | The script is for restricted/manual recovery and can stay out of the normal helper path. |

## Fix scope

This pass changes the normal provision/inject/up disk detach path. The helper
gets a typed `force_detach` operation with a safety check: the requested device
must be the device currently attached for the requested cove disk image path.

If the helper is installed and reachable, cove tries the helper and does not
print a manual `hdiutil` command. If the helper is not installed, cove keeps the
manual fallback.
