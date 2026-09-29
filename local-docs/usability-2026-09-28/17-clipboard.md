# Clipboard sharing

Status: FIXED 09c643d1

Clipboard relies on SPICE (macos.go:848-864 via apple/x/vzkit/clipboard)
and needs spice-vdagent in the guest (provision -guest-tools). vz-agent
clipboard helpers are text-only and Windows-only
(cmd/vz-agent/clipboard_stub.go). No file or image path.

Fix: `cove doctor` flags missing guest tools when clipboard is on; consider
a text/image bridge through the user agent for macOS guests.

## Resolution

1. Added `-clipboard` flag support to `cove doctor` (defaults to true matching `cove run`).
2. Added guest tools (`spice-vdagent`) verification probe to `verifyRunningGuestProbes` in `cmd/cove/provision_verify.go`:
   - For Linux: checks `command -v spice-vdagent` or `/usr/bin/spice-vdagent` or `/usr/local/bin/spice-vdagent`.
   - For macOS: checks `command -v spice-vdagent` or `/Library/LaunchDaemons/com.utmapp.spice-vdagentd.plist` or `/usr/local/bin/spice-vdagent`.
   - Reports `- Guest tools (clipboard): not found; clipboard sharing requires spice-vdagent (run 'cove provision -guest-tools')` when missing and clipboard sharing is enabled.
   - Reports `+ Guest tools (clipboard): spice-vdagent present` when found.
3. For stopped macOS VMs, checks whether `vz-guest-tools.pkg`, `.vz-guest-tools-installed`, `com.utmapp.spice-vdagentd.plist`, or `spice-vdagent` exist on the disk image when mounted, and flags missing guest tools when clipboard is enabled.
4. Added comprehensive unit tests in `cmd/cove/provision_verify_test.go` covering probe presence, Linux and macOS probe commands, clipboard disabled behavior, probe pass/fail reporting, and disk-level detection.
