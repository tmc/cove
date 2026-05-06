# TCC Apple Events Prompt From Guest Terminal Mode

Fresh macOS guests can show a TCC prompt like:

> vz-agent wants access to control Terminal

This is Apple Events automation permission (`kTCCServiceAppleEvents`). It is
independent from Full Disk Access. Granting Full Disk Access to `vz-agent` does
not grant permission to automate Terminal.

## Trigger

The prompt came from the explicit guest Terminal path in `vzscript`: the user
agent launched `open -a Terminal <wrapper.command>` so a long-running script
could be watched in a GUI Terminal window. LaunchServices attributes that Apple
Events request to `vz-agent`, which is installed as a raw Mach-O at
`/usr/local/bin/vz-agent`.

## Decision

Avoid Apple Events by default. `# runs-on: terminal` and `cove vzscript run
-terminal` now stream output back to the host terminal over the existing
`agent-exec-stream` path. That preserves the useful part of Terminal mode
(visible long-running output) without asking the guest to automate Terminal.

For users who explicitly want a guest Terminal window, `# runs-on: terminal-gui`,
`guest-terminal`, and `cove vzscript run -terminal-gui` keep the GUI path. Before
calling `open -a Terminal`, cove checks whether a Terminal Apple Events grant is
already present. If not, it falls back to host-streamed output instead of
triggering a fresh prompt.

Apple's macOS Mojave release notes say Apple Events automation approval can be
preflighted with `AEDeterminePermissionToAutomateTarget`:
https://developer.apple.com/documentation/macos-release-notes/macos-mojave-10_14-release-notes

That API should move into `vz-agent` once the agent has a stable app identity.
This slice uses a read-only TCC database probe because the current Terminal
launcher is assembled host-side and executed by the raw agent binary.

## Signature Check

Two consecutive ad-hoc signed `vz-agent` builds from identical source produced
matching bytes and matching cdhashes in this checkout:

```text
CDHash=e59da941dfcb365a07ee132204ec7dd910f47d49
CDHash=e59da941dfcb365a07ee132204ec7dd910f47d49
```

A reproducible build using `-trimpath -buildvcs=false -ldflags=-buildid=` was
also stable:

```text
CDHash=11eb16fd9096d7996b2bb7e4398a458e4bea3690
CDHash=11eb16fd9096d7996b2bb7e4398a458e4bea3690
```

That makes a future `.app` bundle with `NSAppleEventsUsageDescription` viable,
but bundling is intentionally deferred. Bundling changes launchd paths,
Info.plist ownership, signing, install, upgrade, and uninstall behavior.

## Recovery

If a user denied the prompt and later wants explicit GUI Terminal mode, reset
stale Apple Events decisions inside the guest:

```sh
tccutil reset AppleEvents
```

Then rerun the explicit GUI command and approve Terminal automation when macOS
asks.
