# Windows VZ installation acceptance plan

2026-09-08, branch `windows-vz-research`, starting commit `53655f33`.
Host: macOS 27.0 build 26A5425a. Prior scratch media named in the handoff
are absent. Retained source and manifests establish historical results;
new media must be rebuilt and hashed before use. Durable scratch root:
`~/.vz/research/windows-vz-20260908`. Existing VM disks remain untouched.

1. Recover Microsoft ARM64 installation media, verify catalog size and hash,
   and inventory its image editions. Rebuild the existing GOP shim and
   compare its hash with the historical receipt. Keep masters unbooted.
2. Boot one disposable WinPE copy with PMU, public virtio graphics and NAT.
   Attach a new dedicated installation target. Record disk identity,
   capacity, storage driver binding and network status before partitioning.
   Stop after 60 seconds unless an explicit installation phase is running.
3. Install onto that target and retain installation logs. Installation
   progress and WinPE receipts do not pass the desktop gate. Preserve the
   shim chainload path and EFI state for subsequent boots.
4. Cold-boot the installed target under VZ without installation media.
   Capture the desktop through the guest transport. Verify visible text
   entry and mouse selection with before/after frames and guest receipts.
5. Restart and repeat connection, screen capture, text entry and mouse
   interaction. Record two successful installed-system sessions, their
   boot identities, commands, deadlines and artifact hashes.
6. Integrate the demonstrated minimum behind explicit cove opt-ins. Run
   meaningful configuration/transport tests and repository build/test
   gates. Sign runnable macOS binaries. Record private API and host-build
   limits. Commit atomic source/text changes through the required helper,
   verify HEAD changes, add notes and push the research branch.

Status reviewed on 2026-09-09:

| Requirement | Evidence and remaining limit |
| --- | --- |
| Dedicated ARM64 installation and usable desktop under VZ | DISM/BCDBoot, completed OOBE, and installed desktop frames in the [installation report](installation/README.md). WinPE progress alone is not counted. |
| Actual cove launch and repeat input after restart | [cove-02 receipt](installation/receipts/cove-launch.json): distinct guest sessions, Notepad text and context menus before and after restart, followed by guest shutdown and exit 0. These input requests used the HTTP control protocol. |
| Native window keyboard and mouse | [Native receipt](installation/receipts/native-input.json): physical user interaction in cove-03 and a later cove-05 cold boot. The same window reconnected after the cove-03 warm restart, but physical input during that particular session was not captured before it ended. |
| Opt-in configuration, source and validation | Research source is landed. Repository build and full tests now pass with normal HOME after guarding the restricted name getter. Live clone diagnostics pass for crash context and skip name access for the missing entitlement. See the September 9 validation receipt. |
| Preserve existing VMs and masters | The cove-01 explicit-directory fallback violated the default-VM metadata isolation requirement. The saved state and historically recorded hardware fields were restored; exact original config bytes remain unavailable. A recovery clone cold-boots and reconnects its agent, but old saved-state restore fails with VZ error 12; original-path resume remains unverified. The fallback fix and regression test are landed. No further default-VM writes or boots are planned. |
| Host security, binaries and platform scope | No host security changes or QEMU execution were used for the installation receipts; binaries remain outside git. Private VZ APIs, synthetic GOP, guest PNG transport and the tested host build remain limitations. |
| Atomic landing, notes and remote | Through d16b2877, source and receipts plus audit notes are on the remote. The September 9 commit-helper attempt failed for insufficient Anthropic credits and did not change HEAD. The user then authorized manual Go-style commits for the receipt corrections. |

The recorded repeated K characters correspond to 12 transmitted key-down
commands before key-up. Other differing letters match transmitted virtual
key codes. These observations do not identify the user's intended physical
keys or establish a mapping defect.

The full goal remains incomplete. The outstanding native warm-restart
physical check needs an attended session. Manual commits are authorized;
helper funding no longer blocks landing. The default-VM incident prevents
claiming that its preservation invariant held throughout the work; semantic
recovery is the supported claim, not exact restoration or verified resume.

If a gate fails, retain the concrete error and bounded run evidence before
choosing a new experiment. Do not revisit delegated PMU exits or modify
host security. QEMU remains a media reference only.
