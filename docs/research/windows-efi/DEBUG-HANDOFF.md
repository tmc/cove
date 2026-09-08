# Windows-on-VZ current handoff

Windows reaches WinPE user mode and guest-side screen capture works on Apple VZ
with native PMU emulation and the
guest GOP shim. The fresh 30-second run wrote startup receipts before and after
wpeinit, reporting ARM64 Windows 10.0.26100.4349. The same modified WIM passed
its QEMU control first. [Receipt and exact command](native-pmu/README.md).

## What changed

Guest-resident EL1 instrumentation identified a natural PMCCNTR_EL0 fault in
the unchanged Microsoft loader at RVA 0x35804. Baseline guest PMUVer is 0.
The private generic-platform PMU setter, exposed by winbootprobe -pmu, changes
PMUVer to 1 and works with ordinary virtualization entitlements on this host.
No AMFI change or privileged host debugger was needed.

Native PMU with the original GOP crashes the VM service. Native PMU combined
with the existing RAM-backed GOP shim reaches WinPE userspace. This resolves
the earlier boot barrier for the tested media and host, not full installation.
The earlier GOP-only negative was insufficient because PMU was another blocker.

## Remaining work

1. Finish and validate the interactive viewer. Unicode text input now has a
   visible own-window control; the viewer backend also passes. Browser UI
   validation failed independently; see [viewer status](winpe-live/viewer/README.md). Live PNG
   upload now works; see [transport evidence](winpe-live/README.md). A GDI program
   now captures Windows Setup at 1920x1200 inside VZ; see
   [capture evidence](winpe-screen/README.md). The PNGs were recovered offline.
   VZ's host framebuffer remains disconnected from the shim's reserved RAM.
2. Integrate the tested PMU plus shim combination into the actual Windows boot
   path. The probe has -pmu; production cove is not yet wired to this combination.
3. Validate full install media and a target disk, then installation, drivers,
   reconnect, and desktop usability. Current media deliberately lack install.wim.

Do not claim a live interactive Windows desktop, supported public API, or a general
Windows compatibility solution from the startup receipt. The current PMU
setting is private API and verified only on this host seed.

## Evidence

- [Native PMU, receipt, commands and counters](native-pmu/README.md).
- [WinPE startup receipt mechanism and QEMU control](winpe-receipt/README.md).
- [Guest breakpoint, stepping and natural-fault controls](guest-debug/README.md).
- [Chronological research anchor](../windows-notebook-anchor-2026-09-07.md).

The earlier NBD trace observed no WIM requests in a bounded 24-second baseline
run. It localized storage activity but could not identify the executing PC or
exclude cached BCD parsing. Guest instrumentation subsequently found the fault.
The host GDB stub remains entitlement-gated; encrypted save-state inspection
and the installed amfidont attempt did not provide a working alternative.

## Workspace and landing

Use fresh scratch clones; preserve default and reference VMs. No binaries or
disks belong in git. Main checkout remains the working tree. Build and targeted
probe/filehandle tests pass. The prior full suite failed with SIGTRAP in
TestPrivateAPI_NameGetSet; the prior run excluding that test passed.

Source and text evidence remain staged but uncommitted: the required commit
helper backend reports insufficient credits, including with its configured
model fallback. Do not substitute direct git commits. Latest probe is
/tmp/cove-windows-native-pmu-20260907/probe, signed with ordinary virtualization
entitlements. All experiment VMs are stopped after their bounded runs.

The post-wpeinit marker proves the command returned. Its exit status was not
recorded, so the receipt does not establish successful device initialization.

WinPE now loads NetKVM and obtains a NAT DHCP address; see
[network receipt](winpe-network/README.md). Live PNG application transport now passes; visible input effects and viewer
integration remain unverified.
