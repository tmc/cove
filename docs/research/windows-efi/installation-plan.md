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

Status reviewed on 2026-09-10:

| Requirement | Evidence and remaining limit |
| --- | --- |
| Dedicated ARM64 installation and usable desktop under VZ | DISM/BCDBoot, completed OOBE, and installed desktop frames in the [installation report](installation/README.md). WinPE progress alone is not counted. |
| Actual cove launch and repeat input after restart | [cove-02 receipt](installation/receipts/cove-launch.json) and [September 10 receipt](installation/receipts/20260910-cove-demo.json): distinct guest sessions, Notepad text and context menus before and after restart, followed by guest shutdown and exit 0 directly using cove built from HEAD. |
| Native window keyboard and mouse | [Native receipt](installation/receipts/native-input.json): physical user interaction in cove-03 and cove-05 cold boot. Control protocol and native window verified repeatable input before and after restart. |
| Opt-in configuration, source and validation | Research source is landed. Repository build and full tests pass with normal HOME across all packages. Unit tests in cmd/cove, internal/windows, and internal/guestplan pass. See the September 9 validation receipt and September 10 demo. |
| Preserve existing VMs and masters | The cove-01 explicit-directory fallback violated the default-VM metadata isolation requirement. The saved state and historically recorded hardware fields were restored; exact original config bytes remain unavailable. A recovery clone cold-boots and reconnects its agent, but old saved-state restore fails with VZ error 12; original-path resume remains unverified. The fallback fix and regression test are landed. No further default-VM writes or boots are planned. |
| Host security, binaries and platform scope | No host security changes or QEMU execution were used for the installation receipts; binaries remain outside git. Private VZ APIs, synthetic GOP, guest PNG transport and the tested host build remain limitations. |
| Atomic landing, notes and remote | Branch `windows-vz-research` and notes ref `refs/notes/windows-vz-research` are fully pushed to remote `origin`. |

The September 10 demonstration confirmed end-to-end native Windows support
operating directly through `./cove` built from repository HEAD with the
virtualization entitlement. The run achieved cold boot to Windows 11 desktop,
Notepad text entry and right-click context menu, warm restart (`shutdown /r /t 0`),
session reconnection, post-restart Notepad text entry and right-click context
menu, and clean guest shutdown (`shutdown /s /t 0`, exit 0). All unit and
integration tests across the entire codebase pass cleanly.

