# Default VM P1 validation

The real default completed Setup Assistant through framebuffer OCR and reached
Finder with the local account `cove`. No disposable clone was substituted.

Before setup, the stopped guest's disk, auxiliary store, hardware model, machine
identifier, and configuration were APFS-cloned to:

```
/tmp/cove-p1-20260906/default-before-setup.covevm
```

The generated password is in `/tmp/cove-p1-20260906/password` (mode 0600; parent
directory mode 0700). Setup output and screenshots are in the same protected
artifact directory; `setup-desktop.png` shows Finder and the cleared overlay.

Agent installation was staged using `cove -vm default provision-agent`, with
`COVE_FORCE_MANUAL_ELEVATION=1`. The generated installer runs through sudo in an
iTerm2 split. The original generated script failed because hdiutil does not support ASIF on
this host. The corrected script prefers `diskutil image attach --noMount --plist`,
normalizes device names, discovers only APFS containers backed by that image,
and ejects the disk through an EXIT trap. It retains hdiutil for older hosts.
The real installation copied the binary and both plists, then ejected disk5.
`agent-install-fixed.log` records the successful run.

A subsequent plain `cove -vm default run` cold-boot reconnected vz-agent in about
four seconds (`cold-boot.log`). The account remained at the login screen until
explicit input through the VM window; this is not evidence of automatic login.
After login, an agent round-trip returned exit status 0 and confirmed:

- `id cove`: UID 501, group 80 (admin).
- `/dev/console`: owner cove, UID 501.
- `pgrep -l Finder`: Finder running.

`agent-desktop-proof.log` contains the response, and `desktop-final.png` shows
Finder with the agent background-activity notification. The default remains
running at its usable desktop.

`go build ./...` passed. `go test ./... -skip '^TestPrivateAPI_NameGetSet$'`
passed; the excluded native diagnostic is documented in the P0 report.
The generated installer passed `bash -n` and live installation validation.
