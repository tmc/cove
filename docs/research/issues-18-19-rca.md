# Issues 18 and 19 RCA

This note covers GitHub issues:

- #18, "Provision dialog stays on screen ~20s after OK click"
- #19, "Auto-login does not take on first boot after fresh install (kcpassword + loginwindow.plist provisioned but ignored)"

R36 prep work already landed:

- `07d64e8` added watchdog tests for native authorization prompt hangs.
- `d430a1f` verified staged auto-login plist ownership metadata.

## Issue 18

The issue report says the macOS authorization dialog stays visible for about 20 seconds after the password is accepted during post-install provisioning. The current code path is:

1. `stopVMAndInject` stages provisioning files after install.
2. `applyProvisioningFilesForVM` checks the disk, then calls `attachAndMountDataVolume`.
3. `applyStagedFiles` builds the elevated manifest and calls `runElevated`.
4. `runElevatedManifestNative` calls `AuthorizationCreate` with `InteractionAllowed`, `ExtendRights`, and `PreAuthorize`, then launches the elevated helper.

The user-visible delay is therefore tied to asking for admin rights only at the point where the Data volume is already being applied. The dialog appears late in the install/provision flow, and the child helper's completion still affects how long the dialog feels connected to provisioning work.

Planned fix: pre-warm native authorization before any Data-volume attach attempt. The pre-warm uses the same Security.framework bridge as the real elevated call so the OS credential cache is populated before provisioning reaches disk attach and manifest application.

Live reproduction was not run in this worktree because no IPSW path or disposable VM target was provided. The issue body contains the reproduction command and observed behavior, and the fix is covered by a unit test that proves pre-warm runs before the disk attach helper.

## Issue 19

The issue report says a fresh provisioned VM reaches the login screen on first boot even though auto-login artifacts were staged. Current staging writes:

- `private/etc/kcpassword` with `password.EncodeKC(password)`, mode `0600`, owner metadata `root:wheel`
- `Library/Preferences/com.apple.loginwindow.plist`, mode `0644`, owner metadata `root:wheel`
- the cove auto-login script and LaunchDaemon, both owner metadata `root:wheel`

R36 verified the plist shape and owner metadata, but it did not assert the staged `kcpassword` bytes. That left a gap: a future path could preserve the plist and ownership while writing stale or malformed kcpassword content.

Planned fix: validate `kcpassword` immediately after direct injection and after staging. The expected bytes are exactly `password.EncodeKC(password)`, and staged manifest metadata must keep `root:wheel`. Extend tests to assert the bytes, metadata, and encode/decode round trip.

Live first-boot reproduction was not run in this worktree because no disposable IPSW/VM target was provided. The fix closes the concrete artifact-integrity gap identified by the issue and existing tests.
