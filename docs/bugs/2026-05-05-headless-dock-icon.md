# Headless Dock Icon

Fixed in `a68f44d` and covered by `f1380b0`.

## Root Cause

`runVMHeadless` correctly prepares `NSApplicationActivationPolicyAccessory`, but the headless controller setup called `configureProcessIdentity`, which touched `NSApplication.DockTile` to set the VM-name badge. Accessing the Dock tile was enough to materialize a Dock icon during `cove run -headless`.

The GUI path still sets the badge after promotion to `NSApplicationActivationPolicyRegular`, when a Dock icon is expected.

Password and authorization dialogs do not require a headless Dock icon. Password prompts run through `osascript`, headless mode disables the password-dialog preference, `AuthorizationCopyRights` uses SecurityAgent, and in-process AppKit panels run from GUI contexts after promotion.

## Verification

Built and signed the test binary:

```
go build ./...
go build -o cove .
codesign -s - -f --entitlements internal/autosign/vz.entitlements ./cove
go test ./...
```

Behavioral check on `cove-test`:

```
base-cove-dock-count=1

headless-status:
{
  "capture_mode": "private-framebuffer",
  "headed": false,
  "supported": true,
  "window_ready": true
}

headless-dock-count=1

open:
{
  "capture_mode": "window",
  "headed": true,
  "supported": true,
  "window_ready": true
}

open-dock-count=2
```

The pre-existing Dock item was from a separate headed `cove up` process. The headless run did not add another Dock item; `cove ctl gui open` did.

`screencapture -x /tmp/cove-r41-evidence/before-headless.png` failed on this host with `could not create image from display`, so the live Dock verification used System Events Dock item counts instead.
