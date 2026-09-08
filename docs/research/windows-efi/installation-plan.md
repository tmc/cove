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

Status on 2026-09-08: gates 1–5 now have installation, installed desktop,
cold-boot reconnection and guest-restart input receipts in
[the installation report](installation/README.md). The native AppKit window
uses guest PNG capture; it does not repair the VZ scanout. Gate 6 has source
and focused checks, but full cove validation is blocked by missing private
module replacements. The user authorized direct Go-style commits after the
required helper failed for insufficient credits. Physical keyboard/mouse
verification through the AppKit window is still pending. Do not mark the
complete goal achieved until the remaining validation gates are resolved.

If a gate fails, retain the concrete error and bounded run evidence before
choosing a new experiment. Do not revisit delegated PMU exits or modify
host security. QEMU remains a media reference only.
