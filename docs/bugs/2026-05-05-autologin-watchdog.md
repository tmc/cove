# Auto-login watchdog misses split provision/run

Status: fixed by follow-up implementation
Date: 2026-05-05

## Observation

MLX Go QA tested `~/go/bin/cove b83b0b71` with
`mlxgo-fresh-headed2-20260505`. Provisioning completed after the native
administrator prompt and created the `mlxqa` user with SecureToken enabled, but
the next headed boot stopped at the login screen for `mlxqa`. The user agent
was unavailable until QA pressed Return, typed `mlxqa123`, and reached Finder.

That is a cove bug. The headed macOS path has a login-screen watchdog in
`provision_automation.go` (`runLoginScreenWatchdog`) whose job is to type the
cached password when `kcpassword` and `loginwindow.plist` do not carry the boot
all the way to the desktop.

## Credential Chain

The working one-command path is:

1. `cove up -gui -user X -password Y` stages and applies provisioning files.
2. `macos.go` writes `<vmDir>/autologin.json` with
   `writeLoginScreenCredentialsCache`.
3. `resolveLoginScreenWatchdogCredentials` returns the in-process
   `provisionUser` and `provisionPassword` while the provisioning marker is
   present.
4. `runMacOSVM` registers `runLoginScreenWatchdog`.

The broken split-command path was:

1. `cove provision -user X -password Y -stage-only` writes the disk-side
   staging directory, including `private/etc/kcpassword` and
   `Library/Preferences/com.apple.loginwindow.plist`.
2. `cove provision -apply` mounts the Data volume and applies the staged files,
   but did not write `<vmDir>/autologin.json`.
3. `cove run -gui` has no `-user` or `-password` flags, so
   `resolveLoginScreenWatchdogCredentials` falls back to
   `bootLoginScreenCredentials`.
4. `loadBootLoginScreenCredentials` first checks `<vmDir>/autologin.json`,
   finds nothing, and then tries to recover credentials by mounting the VM disk
   and reading `kcpassword` plus `loginwindow.plist`.
5. That disk-mount fallback runs on the boot path immediately before the
   Virtualization framework opens the same disk. If it fails or races, the
   watchdog receives empty credentials and never starts.

## Fix

`cove provision -apply` should write the same host-side
`<vmDir>/autologin.json` cache after the staged manifest has applied
successfully. The apply process no longer has the original `-password` flag in
scope, so it should read the staged `kcpassword` and `loginwindow.plist` files
with the same parser used by the disk fallback, then write the cache with mode
`0600`.

The boot path should also stop mounting the disk as a fallback. The disk reader
can remain available for explicit repair and tests, but `cove run -gui` should
only use the host-side cache so it does not compete with VM boot for the disk
attachment.
