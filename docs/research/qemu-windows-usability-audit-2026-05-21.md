# QEMU Windows Usability Audit

Date: 2026-05-21

## Objective

Bring the direct QEMU/HVF Windows backend close to the native
Virtualization.framework user experience for setup, inspection, automation, and
support.

## Current Evidence

The live VM `windows-qemu-auto3` has verified:

- QEMU backend state reports `running`;
- daemon and user agents are connected;
- `exec` defaults to the logged-in Windows user session;
- `exec --daemon` runs as `nt authority\system`;
- `gui -vm windows-qemu-auto3` opens the recorded local VNC URL;
- `gui status` reports `qemu-vnc-external`, `vncAuth: none`,
  `screenshotBackend: rfb`, and `textBackend: rfb`;
- forced RFB screenshot produces a valid 800x600 JPEG;
- forced RFB text input returns `OK`;
- RFB pointer move returns `OK`;
- RFB-backed OCR click on `Recycle Bin` returns `clicked "Recycle Bin"`;
- `gui diagnose` captures the current RFB screen, reports `screen: desktop`,
  and explains that a connected user agent means Windows may already be logged
  in without showing a username prompt;
- `gui open` starts a Cove-owned AppKit viewer process by default when a local
  QEMU VNC endpoint is available
  and `gui status` reports `qemu-vnc-cove` while its `qemu/viewer.pid` process
  is alive;
- `gui close` closes the Cove-owned viewer process and leaves the QEMU VM
  running;
- direct QEMU RFB text input landed in Windows Notepad and was visible through
  OCR;
- live host click/key events through the experimental Cove AppKit viewer landed
  in Windows Notepad once the viewer used one persistent RFB connection for both
  display and input;
- the Cove viewer saves its window frame through the same NSWindow autosave
  mechanism as native VM windows, under a `windows-qemu` VM identity;
- the Cove viewer installs a minimal macOS menu with screenshot and close-viewer
  actions, and the input path filters host Cmd/Control/Option shortcuts so menu
  keys are not forwarded into Windows;
- the Cove viewer installs a matching toolbar with screenshot and close-viewer
  items;
- support bundle includes `vm/qemu-status.json` with guest password redacted.

Repository gates passed after the latest slices:

- `go test ./...`;
- `go build ./...`;
- `go build -o /tmp/cove-qemu-ui .`;
- `codesign -s - -f --entitlements internal/autosign/vz.entitlements /tmp/cove-qemu-ui`;
- `git diff --check`.

## Done

- QEMU status bypasses the native control socket and reads QEMU metadata.
- `ctl ready` works against QEMU Windows daemon and user agents.
- `exec`, `shell --`, and `exec --daemon` choose the right Windows agent.
- `gui` and `vnc` aliases reopen the local VNC console and explain external
  close behavior.
- `gui diagnose` writes a current screenshot under `qemu/screenshots`, reports
  the detected screen state, and repeats the QEMU VNC-vs-Windows-credential
  distinction.
- Experimental `qemu-display` opens a Cove-owned AppKit window for the QEMU RFB
  stream. It is the default `gui open` path when a QEMU VNC endpoint is
  available, records `qemu/viewer.pid` for `gui status` and close control, and
  uses one persistent RFB connection for display refresh plus keyboard and
  pointer input.
- QEMU display frame persistence uses the shared native window autosave helper
  with a Windows QEMU identity.
- QEMU display menu support provides screenshot and close-viewer actions without
  requiring a VZ toolbar delegate.
- QEMU display toolbar support exposes the same screenshot and close-viewer
  actions as visible window controls.
- `agent-upgrade` builds the Windows agent as a GUI subsystem binary.
- Windows provisioning disables display sleep and configures daemon and user
  agents.
- Clipboard plumbing uses QEMU SPICE vdagent paths where configured.
- Support bundles include QEMU metadata, process metadata, README, serial
  artifacts, optional screenshots, and computed QEMU status.
- `doctor qemu` validates host prerequisites and QEMU backend override values.
- `internal/rfb` provides a stdlib-only RFB 3.8 no-auth client with raw
  framebuffer updates, key events, text input, pointer events, unit tests, and a
  skipped-by-default live test.
- QEMU screenshots prefer RFB and fall back to monitor `screendump`.
- QEMU text input prefers RFB and falls back to monitor `sendkey`.
- QEMU mouse input uses RFB pointer events, with `usb-tablet` as the default
  Windows QEMU input device so absolute pointer coordinates match the screen.
- QEMU `click-text` uses OCR plus RFB pointer clicks when VNC is available, with
  keyboard-safe fallback when no VNC endpoint exists.

## Remaining Gap

QEMU Windows has a default Cove-owned AppKit display window with live keyboard
and pointer input, but it is not native-VZ-equivalent yet. The remaining gap is:

- native focus semantics without the global event-monitor fallback.

## Next Implementation Slice

Continue the Cove-owned AppKit viewer:

1. Replace the global event-monitor fallback with ordinary AppKit focus
   delivery, or make the fallback an explicit documented behavior.
2. Keep external VNC as fallback when the AppKit viewer fails.
3. Extend support bundles to record whether the Cove-owned QEMU window was
   active.

## Stop Condition

The QEMU backend should not be called native-VZ-quality until a live Windows VM
shows the Cove-owned QEMU window, `gui close` closes that window without
stopping QEMU, and screenshot/OCR/input still pass against the same visible
desktop.
