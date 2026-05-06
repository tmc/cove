# Fresh VM Login Misclassification

Date: 2026-05-05

## Report

A fresh macOS VM created with:

```bash
cove up -user mlxqa -password mlxqa123 -headless -memory 8 -cpu 4 -no-shutdown
```

reported Skip Setup Assistant and Auto-Login provisioning, but the first GUI
boot reached the login prompt. OCR saw `Name`, `Enter Password`, and
`Agent: connecting...`. `cove ctl detect` reported the pixel state as
`desktop`.

## Bug A: User account never created

The attempted recovery proved this is not only auto-login failing. The login
window rejected `mlxqa` / `mlxqa123` after `cove ctl` typed the credentials, so
the `mlxqa` account was never created inside the guest.

The macOS provisioning path stages the right ownership metadata. The staged
LaunchDaemon and auto-login repair LaunchDaemon are recorded as `root:wheel`;
the auto-login payload also stages `/private/etc/kcpassword` and
`/Library/Preferences/com.apple.loginwindow.plist`.

Those files still depend on the apply phase writing root-owned files into the
guest Data volume. macOS launchd silently ignores LaunchDaemon plists that are
not owned by root:wheel, so a non-root `cove up` path must make that requirement
obvious before the VM boots. The existing FAQ documents the same failure mode:
without sudo, the injected files can receive the invoking user's UID and the
first-boot provisioning daemon never runs.

The fix is to fail before claiming provisioning success. When macOS `cove up`
would perform first-boot user provisioning and the host process is not root, it
must return a non-zero error before install/provision/run continues:

```bash
sudo cove up -vm <name> -user <user> -password <password> ...
```

## Bug B: Login prompt detected as desktop

`ctlDetectScreen` in `ctl.go` takes a screenshot, prints the pixel detector
result from `DetectScreenState`, then creates an OCR service and prints the OCR
detector result from `DetectScreenStateOCR`.

The bug is in the OCR detector's signal strength. `DetectScreenStateOCR` reduced
OCR output to plain text, so it could only match generic text like
`enter password` or `login window`. It could not use OCR positions to recognize
the macOS login prompt's central `Name` plus `Password` fields. If pixel
heuristics see dock-like elements near the bottom, the debug output can still
present the screen as desktop even though the central form is a stronger login
signal.

The narrow detector fix is to inspect OCR observations before falling back to
pixel heuristics: if `Name` and either `Password` or `Enter Password` are both
found in the central two-thirds of the frame, classify the screen as
`login_screen`.

## Bug C: Noninteractive privilege prompt hang

The attempted manual recovery:

```bash
cove provision -user mlxqa -password mlxqa123 -skip-setup-assistant
```

hung in the privileged write step. The likely path is
`AuthorizationExecuteWithPrivileges` through `runElevatedManifestNative`.
That native dialog path is acceptable for an interactive terminal, but it is not
acceptable when stdin is noninteractive. In that case cove must fail fast with a
plain error and a `sudo cove ... provision -apply` recovery command instead of
waiting for a GUI authorization prompt the caller may never see.

The reset-password recovery also failed while looking for the guest Data
partition:

```text
could not find Data partition for disk /dev/disk23
```

Without the `diskutil list` text that the detector parsed, the next diagnosis
has no evidence. The immediate fix is diagnostic: include the full
`diskutil list` output in that failure so the partition parser can be corrected
from real layout data.
