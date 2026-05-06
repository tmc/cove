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

## Bug A: Auto-login provisioning visibility

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

The narrow fix is visibility, not a provisioning rewrite: when macOS `cove up`
is about to stage and apply user provisioning with auto-login while the host
process is not root, print a prominent stderr warning with the recovery command:

```bash
sudo cove -vm <name> provision -user <user> -password <password> -skip-setup-assistant -auto-login
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
