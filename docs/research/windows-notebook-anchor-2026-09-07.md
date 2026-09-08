# Windows research NotebookLM anchor

Notebook: https://notebooklm.google.com/notebook/c2575e86-d977-4072-afea-632a4163d56a
Conversation: `a89ac319-b87f-4e34-9d5f-01980bc35b08`.

The user requested the notebooklm-assisted-self-prompting skill using session
`rollout-2026-09-06T18-08-49-01a07969-253e-72e1-af1e-d5b6b2eee8ae.jsonl`.
A cleaned transcript (tool payloads/images omitted, credential-related lines
removed) and current Windows source/results were synced sequentially under
`tmp: cove-windows-self-*`. Existing iOS sources are a separate workstream.

The mission remains Windows boot and guest observability under Apple VZ;
NotebookLM advice is checked against local evidence before execution. A suggested
experiment is not proof that its expected diagnostic fields or conclusions exist.

## First iteration

The anchor recommended exposing the startup NSError domain, code, and userInfo.
Verified the binding returns a retained NSError, added the diagnostic to the
probe, built/signed it with ordinary virtualization entitlements, and ran one
bounded scratch-disk GDB launch. Result:

```
VZErrorDomain code=1
NSLocalizedFailure = Internal Virtualization error.
NSLocalizedFailureReason = The virtual machine failed to start.
```

No underlying cause or entitlement-specific field was present. This neither
proves nor disproves entitlement enforcement. The proposal's claim that a
standard startup error would disprove the gate was rejected. Build and probe vet
passed; raw run log is `/tmp/cove-nlm-self-20260907/gdb-detail.log`.

The result and the correction that only one privileged amfidont attempt occurred
were synced back to the anchor. The next query asks whether ordinary VZ serial
attachments can support Windows ARM boot-debugging, requesting a primary-source
research question if the notebook cannot establish transport compatibility.

## Transport research correction

The second anchor response rejected virtio-console early debugging and suggested
QEMU memory inspection. Its broader claim that Windows has no early virtio
transport was insufficiently supported. Primary-source research found
[Cloud Hypervisor's KDNET guide](https://github.com/cloud-hypervisor/cloud-hypervisor/blob/main/docs/windows-kdnet-debugging.md),
which describes a shipped virtio-net debug transport. This is not proof of VZ
compatibility or pre-boot-manager support.

The actual verified ARM64 WinPE boot.wim index 2 contains
`Windows/System32/kd_02_1af4.dll`. Extraction and `file` identify an AArch64 native
DLL, not an x86-only candidate. Scratch copy:
`/tmp/cove-nlm-self-20260907/kdnet/kd_02_1af4.dll`.
No binary is staged. The next factual checks are NIC/device feature compatibility
and availability during bootdebug versus kernel debugging. Microsoft distinguishes
those phases explicitly in its
[serial extensibility documentation](https://learn.microsoft.com/en-us/windows-hardware/drivers/debugger/kdserial-extensibility-code-samples).

This finding was synced under `tmp: cove-windows-self-kdnet`. It reopens a concrete
guest-side debugging candidate while the host-side launcher remains unvalidated.

## Boot-manager-only KDNET control

The notebook's objection that all KDNET transports were kernel-only was corrected
against [Microsoft's -bkw documentation](https://learn.microsoft.com/en-us/windows-hardware/drivers/debugger/setting-up-a-network-debugging-connection-automatically).
The follow-up anchor accepted a boot-manager-only control with qualified outcomes.
Actual ARM64 module disassembly checks vendor 0x1af4 and device 0x1000/subsystem1
or 0x1041. Boot-manager UTF-16 strings also contain the kd transport filename
patterns and kdstub.dll; those strings alone do not prove successful loading.

A scratch WinPE session ran bcdedit against its own EFI BCD. Recovered
[BCD settings](windows-efi/results/kdnet-bcd.txt) show bootmgr bootdebug Yes,
winload bootdebug No, kernel debug No, and NET transport to the QEMU virtual host.
The key is a disposable test value. QEMU uses modern PCI virtio-net 1af4:1041,
an isolated user-network backend and filter-dump capture.

The first 24-second control reached the WinPE Setup GUI and read 601,008,640
bytes, but emitted zero Ethernet frames. Supplying the original ARM64 kdstub.dll
and kd_02_1af4.dll beside the fallback and Microsoft EFI loader paths changed
behavior: the next control parked at 3,282,432 bytes, still with no packets.
[Counter summary](windows-efi/results/qemu-kdnet-results.json).

QEMU's ordinary GDB stub then provided actual guest CPU visibility without any
amfid attachment. Repeated inspection found PC 0x13c1d6200 in a branch-to-self
at VBAR_EL1+0x200, ELR_EL1 0x13c20d40c, ESR_EL1 0x96000046, FAR_EL1 0x3fffc014.
The faulting instruction is `strb w1, [x8]`, writing zero at 0x3fffc014.
[Debugger record](windows-efi/results/qemu-kdnet-fault.txt).
[Runtime PCI layout](windows-efi/results/qemu-kdnet-pci.json) places the virtio
NIC BAR4 at 0x3fffc000, size 16384: the fault address lies at offset 0x14 in
that BAR. This localizes the control failure to a write in the NIC aperture;
it does not yet establish why that address was inaccessible or identify Apple's
stall. The last debugger inspection detached successfully and all QEMU runs ended.

Next: investigate the control's BAR mapping / MMIO access failure before treating
KDNET as a validated Apple observability path. Source/runtime evidence is stronger
than either blanket rejection or assuming driver-file presence proves support.
Scratch harnesses and original captures: `/tmp/cove-windows-kdnet-20260907/`.

## Low-MMIO control and first VZ capture

A read-only physical [page-table walk](windows-efi/results/qemu-kdnet-page-walk.json)
confirmed a zero level-2 descriptor for 0x3fffc014. QEMU's runtime memory tree also
showed the low PCI MMIO aperture ending at 0x3efeffff, below this BAR4 address.
The descriptor for a valid low-window comparison address was present.

Changing only the QEMU machine's documented `highmem-mmio=off` option made the
boot-manager-only setup complete DHCP and emit repeated UDP packets to the
configured debugger port 50000. [Packet summary](windows-efi/results/qemu-kdnet-lowmmio-packets.txt)
and [PCI layout](windows-efi/results/qemu-kdnet-lowmmio-pci.json) preserve the result.
This establishes early debug-transport transmissions with these settings; no
WinDbg handshake or interactive Windows debugging session has been established.
The original capture is `/tmp/cove-windows-kdnet-20260907/bootdebug-lowmmio-run/network.pcap`.

Added an opt-in capture-only NIC to winbootprobe using the existing filehandle
network session. The 24-second Apple run with the same debugger media started
but recorded zero frames. [Run log](windows-efi/results/apple-kdnet.txt).
This NIC deliberately supplies no DHCP replies, so the pass criterion at this
stage was any initial transmission, not a full connection. The existing pcap
writer emits its header on first packet, so the zero-frame file is zero bytes;
the session counters provide the evidence of silence. No capture error was
reported before intentional cancellation. This negative does not identify the
Apple execution point or establish driver incompatibility.

Capture package tests and probe vet passed. A validated Apple debugger path
remains incomplete; the QEMU transport result must not be promoted to VZ success.

## VZ capture receive-path control

The NotebookLM capture review recommended using an existing macOS guest rather
than writing a new SNP transmitter. The probe already supports macOS. A stopped
reference VM was APFS-cloned into
`/tmp/cove-windows-kdnet-20260907/macos-capture-control`; only the clone was booted.
The source disk had no open holders, and the running default VM was untouched.
State files were copied to the filenames expected by vzkit; suspend state was
not used. The same signed winbootprobe ran with `-macos` pointing to that clone,
`-nocap -seconds 30 -pcap /tmp/cove-windows-kdnet-20260907/macos-capture.pcap`.

[Control log and DHCP summary](windows-efi/results/macos-capture-control.txt)
show guest DHCP requests received by the capture-only NIC. This validates the
host receive path with a working guest driver. It does not validate Windows'
driver initialization or prove that its zero-frame run reached networking.
The pcap stays in scratch; no packet payload or guest disk is staged.

Next: inspect Apple's EFI PCI NIC BAR assignments and compare with the QEMU
low-MMIO control. BAR locations alone cannot reveal Windows' page tables or
prove the same fault occurred. The earlier NotebookLM claim that a PCI scan
would instantly establish an identical fault was too strong. SNP enumeration
is optional inventory, not a KDNET compatibility test. Privileged host attach
remains held because BBFA has no validated amfidont replacement.

## EFI PCI inventory

A separate `windows-efi/pci.c` application reuses the frozen EFI diagnostic's
report helpers and reads 256 bytes of configuration space per PCI I/O handle.
It performs no BAR sizing writes or device initialization. Protocol ABI was
checked against [EDK2 PciIo.h](https://github.com/tianocore/edk2/blob/master/MdePkg/Include/Protocol/PciIo.h),
including compile-time method-offset assertions. `build-pci.sh` builds warning-clean
with reproducible PE output. Tested SHA-256:
`5a0858dd42175544f773390d01b2ca401e96aec0f8f0fcceee6468aa4e79d7f9`.

Fresh copies of a diagnostic FAT disk ran for at most 12 seconds under the same
Apple probe with its capture-only NIC and QEMU low-MMIO harness with modern
virtio-net-pci. Both persisted PCIDIAG.TXT; Apple shut down cleanly. Scratch:
`/tmp/cove-windows-pci-20260907/` (raw reports, binaries, images, command/capture logs).
No Windows loader ran during this inventory.

| Measurement | Apple EFI | QEMU low-MMIO EFI |
|---|---|---|
| PCI I/O handles | 7 | 3 |
| NIC vendor:device | 1af4:1041 | 1af4:1041 |
| Bus:device:function | 00:01.0 | 00:02.0 |
| Main virtio BAR | BAR0 = 0x280020000 | BAR4 = 0x10040000 |
| Other NIC BAR | BAR2 = 0x50007000 | BAR1 = 0x10048000 |
| PCI command at inventory | 0x16 | 0x0 |

[Apple raw config](windows-efi/results/apple-pci.txt) and
[QEMU raw config](windows-efi/results/qemu-pci.txt) preserve all six BAR words and
capability bytes. The Apple main BAR combines low word 0x80020004 (64-bit memory)
with high word 0x2; it really is above 4 GB. The vendor capability at 0x40
selects BAR0, offset zero for common configuration. QEMU's common configuration
capability selects BAR4, offset zero. These are firmware-inventory measurements,
not the Windows runtime's page tables or proof of where its execution stopped.

The NotebookLM PCI review incorrectly suggested Apple's NIC would typically be
MMIO-only and PCI enumeration would miss it. The actual device is PCI-backed in
this configuration. It also incorrectly called QEMU's earlier 0x3fffc014 fault
above 4 GB; that address is below 4 GB but outside that run's low PCI aperture.
Do not generalize the known QEMU aperture fault into an unverified 4-GB limit.

Next useful discriminator: determine whether the shipped Windows KDNET extension
can initialize a valid above-4-GB virtio common BAR in a QEMU control, or obtain
Windows execution-state evidence on Apple. The observed BAR difference is a
hypothesis input, not grounds for blind Apple BAR relocation or another GOP shim.

## KDNET capability prerequisite found before the BAR experiment

Read-only inspection found a more specific driver prerequisite. Decoding the
saved NIC capability chains with `windows-efi/pci-caps.py` yields:

- [Apple](windows-efi/results/apple-pci-caps.json): types 1, 3, 2, 4, followed by MSI-X; no type 5.
- [QEMU](windows-efi/results/qemu-pci-caps.json): types 5, 2, 4, 3, 1.

Type 5 is VIRTIO_PCI_CAP_PCI_CFG, the PCI configuration access capability.
[Virtio 1.2 section 4.1.4.9](https://docs.oasis-open.org/virtio/virtio/v1.2/virtio-v1.2.html)
describes it as an alternate register-access method and requires devices to
present it. This is a finding about this measured Apple NIC configuration,
not an inventory of every Apple device or host release.

The shipped ARM64 `kd_02_1af4.dll` has SHA-256
`9ab61d697fe4a7d3a762c26ef2e6c6bda43fb8b8a1b43c7fd5c72a717d58887c`.
Its [selected disassembly](windows-efi/results/kdnet-capability-check.txt) shows:

1. RVAs 0x1934–0x19b8 traverse the PCI capability chain and populate five
   pointers at stack offset 0x110, indexed by vendor capability type minus one.
2. RVAs 0x1a54–0x1a90 check all five pointers. At RVA 0x1a68, a null pointer
   branches to RVA 0x1868, which returns 0xc0000001. Type 5 skips the BAR-resource
   check but does **not** skip the non-null pointer check.
3. The common-register address and first device-reset call follow this validation
   at RVAs 0x1a94–0x1ad0. The address calculation uses a 64-bit resource pointer
   plus a zero-extended 32-bit capability offset; this does not by itself prove
   end-to-end support for arbitrary 64-bit physical BARs.

Thus, if this routine receives the measured Apple configuration, the absent
capability is sufficient for this driver path to reject initialization before
its reset write. This is static control-flow evidence, not proof that Apple
bootmgr reached this routine. It explains a candidate **debug-transport failure**,
not the original no-debug Windows boot failure. No DLL or host enforcement was
patched. Missing KDNET packets cannot be used to localize the original stall.

The bar-review NotebookLM answer overclaimed that 64-bit load/store instructions
proved driver compatibility and made mapping the Apple blocker. They prove
neither. Defer high-BAR relocation until the capability prerequisite is accounted
for. A bounded QEMU debugger control that removes type 5 from the driver's
observed capability list could validate the rejection path without modifying
Apple's device model. If further transport work requires driver/device changes,
weigh that cost against obtaining direct Windows execution-state observability.

## Runtime validation: missing type 5 rejects KDNET, not WinPE

Three bounded QEMU low-MMIO runs used pristine copies of the debugger media,
modern virtio-net-pci, and the existing QEMU GDB server. Hardware breakpoints
used the previously measured relocated base 0x401a3000, not the preferred PE
image base; actual hits and instruction bytes verified the address in these runs.
The first breakpoint was 0x401a4a54 (RVA 0x1a54), after pointer collection and
before validation. Five pointers occupy stack offsets 0x110 through 0x130.

- [Control](windows-efi/results/qemu-kdnet-cap-control.txt): all five pointers
  nonzero; detached without mutation; KDNET UDP packets appeared.
- [Guarded attempt](windows-efi/results/qemu-kdnet-cap-guard.txt): a mistyped
  expected instruction byte caused the pre-write assertion to reject the change.
  No pointer was cleared. A second breakpoint at the routine's return instruction
  observed w0=0. This is an unmodified run, not a treatment result.
- [Verified treatment](windows-efi/results/qemu-kdnet-cap-missing.txt): corrected
  the check using the four original instruction words. Cleared exactly eight
  bytes at sp+0x130; readback verified the first four pointers were unchanged
  and only the fifth became zero. The return breakpoint at 0x401a488c observed
  w0=0xc0000001. Debugger detached; no packets were captured. Windows nevertheless
  reached the visible WinPE Setup driver screen, with 601,106,944 disk bytes read.

[Counts and statuses](windows-efi/results/qemu-kdnet-cap-results.json).
Raw logs, complete harnesses, debugger commands, captures, and screenshot:
`/tmp/cove-windows-cap-runtime-20260907/`. Each run had a 20-second debugger
observation deadline, then 12 seconds after detach, followed by QMP quit and
process cleanup. The DLL files, Apple device model, and host security were
unchanged. LLDB's Python assertion error does not stop subsequent command-file
commands: always inspect the explicit treatment marker and return status before
calling a run a treatment. The guard prevented the erroneous write as intended.

This dynamically validates the prerequisite identified statically. It does not
prove Apple's bootmgr invokes the extension, but the measured Apple capability
chain is incompatible with this routine's check. It also demonstrates that a
KDNET initialization failure need not prevent WinPE from booting. Therefore do
not attribute the original Apple Windows stall to missing capability 5, and do
not use KDNET silence as execution-location evidence. A usable Apple debugger
transport now needs this incompatibility addressed or a different transport;
BAR relocation alone does not resolve this prerequisite.

## PL011 generic-EFI startup comparison

Local private bindings expose `_VZPL011SerialPortConfiguration`. The transcript's
previous PL011 failure used `macserial -macos ~/.vz/vms/default.covevm`; it was
not a generic-EFI test. A bounded matched comparison closed that distinction.
A temporary `-pl011` option attached this configuration with a file serial output
attachment. The probe was built and signed with the normal virtualization
entitlement, without private debug entitlements or host security changes.

- [PL011 added](windows-efi/results/apple-pl011.txt): configuration validation
  succeeded, but VM start returned VZErrorDomain code 1 with only a generic
  internal error. The guest never started.
- [No-UART control](windows-efi/results/apple-pl011-control.txt): the same binary,
  same EFI diagnostic on a separate pristine copy, same remaining configuration,
  started and shut down cleanly after its diagnostic.

This shows the PL011 addition prevents startup in this tested configuration;
it does not establish whether entitlement enforcement, a device restriction,
or another framework limitation caused the generic error. This path does not
currently provide Windows serial observability. The temporary main-probe flag
was removed; [experiment patch](windows-efi/pl011.patch) and logs preserve it.
The signed binary and both images remain in `/tmp/cove-windows-pl011-20260907/`.
The patch is relative to the staged winbootprobe, applied from repository root
with `git apply --unidiff-zero docs/research/windows-efi/pl011.patch` in a scratch
source copy if reproducing. No binary is staged.

The new KDNET and PL011 results were sent to collaborator 7EB1 for an independent
remaining-options assessment. No experiment VM remains live. Direct Apple guest
execution-state access remains unavailable; this is an external prerequisite
for the existing host GDB route, not a claim that bypassing host enforcement is
the only possible future debugging approach. Await concrete supported alternatives
or an operator-provided working debugger environment before further blind boots.

## New observable surface: loopback NBD request tracing

7EB1 proposed tracing disk-request offsets. Local verification found the public
[VZNetworkBlockDeviceStorageDeviceAttachment](https://developer.apple.com/documentation/virtualization/vznetworkblockdevicestoragedeviceattachment)
and installed `qemu-nbd` request/reply trace events. This is a concrete additional
observable surface despite the anchor's earlier blanket "absolute limit" verdict.
It reveals block requests, not guest PC or instruction execution.

The probe's optional `-nbd URL` replaces the scratch EFI disk's host attachment
while retaining its guest USB mass-storage presentation. It requires `-efi` so
normal VM bundles cannot accidentally select this route. `qemu-nbd` binds only
127.0.0.1:10899, exports a fresh scratch clone, and logs `nbd*` events. The normal
virtualization entitlement suffices. Probe build/sign and vet passed.

[EFI diagnostic preflight](windows-efi/results/apple-nbd-diagnostic.txt) booted,
wrote its report, and shut down cleanly over NBD. The subsequent 24-second
[unchanged WinPE run](windows-efi/results/apple-nbd-winpe.txt) remained running but
issued only 81 reads, totaling 3,044,352 logical bytes. All 81 simple reply events
reported success; their payload lengths sum to the requested read lengths.
These [server trace events](https://github.com/qemu/qemu/blob/master/nbd/trace-events)
record reply issuance, not proof of guest consumption or completed boot progress.

`windows-efi/fat-map.py` maps this image's GPT/FAT32 file extents. It explicitly
rejects other partition filesystems; this WinPE-only disk contains one FAT32
partition, not an installed Windows NTFS volume. Reconstructed BOOTAA64.EFI, BCD,
and boot.wim bytes all matched SHA-256 hashes of the original extracted files.
[Receipt, extents, and summary](windows-efi/results/apple-nbd-winpe.json).

No observed read intersects boot.wim. Final reads cover BOOTAA64.EFI; the final
64-KB request overfetches its tail into neighboring BCD/STL files. Such overlap is
not proof of semantic BCD access. This suggests no WIM streaming occurred during
this bounded run, but a matched file/NBD behavior comparison and timed read-count
samples remain before attributing the same stage to the original file-backed
stall. Logical NBD totals need not equal host physical I/O counters because of
caching, readahead, and rounding. File extents within compressed WIM contents are
outside this mapper's scope.

Raw requests, decoded reads, complete harness, cloned disks and logs remain in
`/tmp/cove-windows-nbd-20260907/`. Both the preflight and WinPE run terminated their
probe and NBD server. No host security changes or working VM modifications were
needed. This is the current next experiment; a lack of register access does not
justify ignoring a newly verified, more limited source of evidence.

## Matched file/NBD comparison: bounded stall confirmed

Using the same signed probe and separate fresh copies of `winpe-base.dmg`, both
24-second runs remained running without further storage progress between 6 and
20 seconds. [File samples](windows-efi/results/apple-matched-file.json) and
[NBD samples](windows-efi/results/apple-matched-nbd.json) retain counters and PIDs.

| Measurement | File, 6s -> 20s | NBD, 6s -> 20s |
|---|---|---|
| Backing-file physical reads | 5,586,944 -> 5,586,944 | 5,734,400 -> 5,734,400 (NBD server) |
| VM RSS | 172,474,368 -> 172,474,368 | 173,867,008 -> 173,817,856 |
| VM user CPU delta | 335,105,010 ns | 334,904,265 ns |
| Logical requests | not observable | 81 -> 81 |
| Logical read bytes | not observable | 3,044,352 -> 3,044,352 |
| Successful server replies | not observable | 81 -> 81 |

NBD's VM process has zero physical file reads because the server owns the backing
file. Both stopped successfully at the bounded deadline. The complete NBD trace
was byte-identical at 6 and 20 seconds; [trace snapshot](windows-efi/results/apple-matched-nbd.trace).
No observed read intersects boot.wim's verified extent. Physical read totals
slightly differ between backends as expected; this is supporting evidence of a
matched park, not proof that the unobservable file-backed request set is identical.

The comparison harness is preserved as `windows-efi/compare-nbd.py`, with the
original fixed scratch paths exposed as arguments. Example from repository root:

```sh
python3 docs/research/windows-efi/compare-nbd.py \
  /tmp/cove-windows-20260906/winpe-base.dmg \
  /tmp/cove-windows-nbd-20260907/winbootprobe /tmp/new-nbd-comparison
```

Use a fresh output directory and a free loopback port. The script creates copies,
starts only its own probe/server, samples their counters, and cleans up its child
processes. All raw tested commands remain in the original scratch directories.

Conclusion shared with 7EB1: the NBD run supports an earlier-than-WinPE stall in
this observation window. It does not prove a pre-BCD point, whether bootmgfw
executed, or whether it faulted or looped. Stop BCD relocation, BAR relocation,
and further blind shim variants. Meaningful next boot work requires a concrete
way to inspect guest execution state, such as a working authorized guest debugger
environment. Current private GDB and PL011 configurations fail startup, KDNET's
shipped driver rejects the measured capability set, and no validated replacement
host debugger attachment is available. Windows-on-VZ success remains unproven.

## Delegated save-state debugging experiment

At the user's request, a subagent owns a new guest-debugging investigation.
A scratch EFI diagnostic with ordinary virtualization entitlements passed public
save validation, paused after one second, and saved a 59,772,928-byte state file.
[Public save log](windows-efi/results/efi-save-public.txt). This proves EFI save
eligibility for this configuration, not access to guest registers.

The saved file begins with `VZVMSave` encoded as the little-endian magic
0x565a564d53617665, followed by an AEA1 container at offset 0x1000. Extracting that
payload to a scratch file and running system `aa list` failed key derivation.
[Archive reader result](windows-efi/results/efi-save-archive.txt). No guest CPU
state has been decoded. Only the standalone diagnostic's save was inspected;
working guest memory was not scanned.

Requesting private save options `compress=false, encrypt=false` trapped in the
host framework. An ordinary launched-child LLDB session localized the branch:
`_saveMachineStateToURL:options:completionHandler:` calls
`VirtualizationEntitlements::from_current_process()` at +84, loads a flag byte,
and tests bit 1 at +96. The false branch reaches `assertion_trap` at +268,
before save-options processing. [Debugger evidence](windows-efi/results/efi-save-entitlement-assertion.txt).
This establishes a process-entitlement guard for the tested private method;
it is not a guest fault. No guard was bypassed or host security changed.

Raw source, binaries, state, and logs: `/tmp/cove-guest-state-20260907/`.
The next delegated control is guest-side: install an EFI exception vector,
execute a known BRK, restore firmware vector state, and persist captured
ELR/ESR/FAR. Success would establish a limited guest-side observation mechanism,
not a Windows debugger. Windows may replace VBAR; that must remain an explicit
limit when considering subsequent loader-entry instrumentation.

## Guest exception control: initial positive result

The delegated standalone EFI control installed a 2-KB-aligned EL1 vector table,
masked asynchronous exceptions, executed BRK #7, captured exception registers,
advanced past that known instruction, restored the original VBAR and DAIF, then
persisted its report. [Initial Apple report](windows-efi/results/apple-efi-brk-initial.txt)
records expected and observed ELR both 0x140002044, ESR 0xf2000007 (BRK #7),
and SPSR 0x800003c5 (EL1h). This is direct guest exception-register observation
without private host debug entitlements. FAR is not meaningful for this BRK.

The initial implementation only handles the deliberately triggered control;
it advances ELR by four for every vector and is unsuitable for arbitrary Windows
faults. Explicit CurrentEL/SPSel preconditions and x16/x17 preservation sentinels
are being added before a stronger control is claimed. Loader instrumentation
must account for Windows replacing VBAR, changing MMU state, and using another
stack. An exception handler cannot safely call EFI filesystem services merely
because those services worked after recovery in the controlled BRK experiment.
No Windows exception has been captured yet.

The strengthened [BRK control](windows-efi/results/apple-efi-brk-verified.txt)
subsequently passed with CurrentEL=4 and SPSel=1 checked before vector installation.
Expected/observed ELR both equal 0x14000204c; ESR is exactly 0xf2000007.
The handler preserved x16=0x1234 and x17=0x5678 across the exception. This confirms
its two scratch-register saves in this control, not arbitrary-context recovery.
The subagent is preparing a one-shot loaded-child entry breakpoint; no Microsoft
loader entry observation has yet been reported.


## Microsoft EFI entry observed

The delegated one-shot entry breakpoint passed for an own-source EFI child and
then the unchanged Microsoft loader file (SHA256
26085cabe01870a8921b61efd135a59898a51daae25b64b6bedba5830fb472f9).
The probe patches only the loaded image in guest RAM. On Apple VZ, image base
0x26de42000 plus entry RVA 0x34b70 matched captured ELR 0x26de76b70;
ESR 0xf2000007 identified the inserted BRK #7. The original entry instruction
was 0xa9bb53f3. This proves entry arrival in the instrumented launch, before
executing the first original Windows instruction. It does not locate the
unmodified boot's stall or establish an interactive debugger.

Recovery accepts only the expected ELR/ESR and unchanged TTBR0/TTBR1/TCR/SCTLR,
restores the parent register/stack/vector state, and abandons StartImage.
The guest is disposable; subsequent normal firmware operation is not claimed.
[Source, build and matched reports](windows-efi/guest-debug/README.md) preserve
this result. A bounded own-child single-step control is the next experiment;
Microsoft stepping is gated on that control passing.


## One original Windows instruction executed

The own-child software-step control captured PC=entry+4 and ESR 0xcf000022
(same-EL software-step exception). The same probe then passed on the verified
Microsoft loader: entry 0x26de76b70 advanced to ELR 0x26de76b74, ESR 0xcf000022,
and SP changed from 0x26fbd8840 to 0x26fbd87f0, exactly -80 for original
instruction 0xa9bb53f3. Raw receipt:
`/tmp/cove-guest-state-20260907/step-windows-report.txt`.

This demonstrates guest instruction stepping under ordinary host virtualization
entitlements. It does not identify the original stall. The next bounded trace
will preserve registers, record PCs in guest memory, and stop before execution
that could invalidate recovery. NotebookLM's previous description of entry
recovery as a "clean detach" was incorrect: StartImage is deliberately abandoned
and the guest must be discarded. No post-recovery normal boot is established.


## Bounded trace reaches first firmware call

The branching control recorded 25 expected steps and preserved x9-x12 sentinels.
The matched Windows trace recorded 67 instructions before its deliberate
loaded-image boundary stop. BLR at bootmgfw RVA 0xbe6c0 transferred to firmware
PC 0x26fae1980, with expected return LR 0x26df006c4 (RVA 0xbe6c4).
[Trace receipt](windows-efi/guest-debug/results/apple-windows-trace.txt) records
this boundary; it is not an observed fault or original-stall PC.

Static inspection of the same verified PE maps the call to RuntimeServices
GetVariable: SystemTable+0x58 supplies the runtime table and offset 0x48 is
GetVariable in the [UEFI table definition](https://uefi.org/specs/UEFI/2.10/04_EFI_System_Table.html).
The argument string at RVA 0x13060 is `SetupMode`, and GUID at RVA 0x13308 is
8be4df61-93ca-11d2-aa0d-00e098032b8c. This is static call identification, not
yet an observed return status. A fresh-run breakpoint at RVA 0xbe6c4 will test
whether this call returns, after an own-child second-site control.

The tracer changes execution timing and uses memory below the child's SP.
StartImage can change interrupt masks: the report's SPSR does not support a
claim of continuously masked IRQ/FIQ. Arbitrary faults, modified page tables,
and iOS execution remain outside the validated controls.


## First firmware call returned

The own-child second-site breakpoint captured its known function return x0=0x1234.
The Windows breakpoint at RVA 0xbe6c4 then captured exact ELR 0x26df006c4 and
ESR 0xf2000007, with x0=0 (EFI_SUCCESS). Thus the first SetupMode GetVariable
call returned in this instrumented launch. No single stepping occurred before
this breakpoint. Raw receipt:
`/tmp/cove-guest-state-20260907/site-windows-report.txt`.

Caller-saved registers other than x0 are not treated as returned variable data.
The next experiment resumes the bounded trace at this verified return site;
this success alone still does not identify the original stall.


The following fresh-run site probe at RVA 0x34bb0, the return of the entire
initial routine at RVA 0xbe620, also captured x0=0 and exact expected
ELR/ESR. Receipt: `/tmp/cove-guest-state-20260907/site-init-report.txt`.
This skips further individual variable-query probes. The bounded trace will
resume at this verified routine return after a non-entry trace control.


The next trace from 0x34bb0 reached another deliberate firmware boundary at
callsite RVA 0x34c24. To avoid serial probes of each call, the following site
probe targeted RVA 0x34c9c after this small call group; it was reached.
Captured x0=0x800000000000000e is EFI_NOT_FOUND from the last AllocatePages
call. Static arguments request one LoaderData page at fixed address 0x102000
(AllocateAddress). The next instructions do not check this status. This failure
is not established as causal and does not justify a memory-layout modification.
Raw receipt: `/tmp/cove-guest-state-20260907/site-callgroup-report.txt`.


## First captured Windows synchronous fault

The trace starting at RVA 0x34c9c recorded 777 steps and recovered a synchronous
exception at ELR 0x26de77804 (RVA 0x35804), ESR 0x02000000. The original PE
instruction there, 0xd53b9d09, disassembles as `mrs x9, PMCCNTR_EL0`.
The preceding PC ring reaches that exact instruction. This is the first actual
Windows fault observed, distinct from the deliberate earlier trace boundaries.
Raw receipt: `/tmp/cove-guest-state-20260907/trace-callgroup-report.txt`.

It is not yet established as the uninstrumented stall's cause: 777 software
steps preceded it. The next controls are an own-source EFI child with the same
instruction, then a Windows run with fault-only vector capture, no instruction
patch and no software stepping. Recovery must accept the exact fault ELR/ESR
and unchanged MMU. Counter emulation or a boot workaround should wait for these controls.


Static scope check found 416 `PMCCNTR_EL0` reads in the loader's disassembly,
plus a `PMCR_EL0` read and seven PMU-register writes. This is not just one
isolated counter access. Full disassembly stays in scratch.
QEMU's [HVF implementation](https://github.com/qemu/qemu/blob/master/target/arm/hvf/hvf.c)
contains explicit PMU register emulation, including PMCCNTR reads, conditional
on its PMU and interrupt-controller configuration. This supplies a possible
explanation for the QEMU/VZ difference; source inspection alone does not prove
which path the earlier QEMU run used.


## Fault reproduced without patching or stepping

Both fault-only controls passed. An own-source child containing the same
`mrs x9, PMCCNTR_EL0` instruction raised ESR 0x02000000. The unchanged loaded
Windows image raised the same exception at RVA 0x35804, with no code patch
and no software stepping. Only the custom exception vector/setup and disposable
recovery remained. Exact expected ELR/ESR and MMU equality checks passed.
Raw receipts: `/tmp/cove-guest-state-20260907/fault-control-report.txt` and
`/tmp/cove-guest-state-20260907/fault-windows-report.txt`.

This removes cumulative single-stepping as the explanation for that observed
fault and establishes a concrete PMCCNTR access blocker in this Apple VZ setup.
It does not prove that fixing PMU behavior would complete Windows boot.
Next control: guest CPU PMU feature report and availability/advance of the ARM
generic virtual counter, before choosing any counter substitution experiment.


## Counter capability control

[Counter report](windows-efi/guest-debug/results/apple-counter-capabilities.txt):
ID_AA64DFR0_EL1=0x10305006, PMUVer=0; CNTFRQ_EL0=24,000,000. Three CNTVCT
samples advanced (0x54dd662, 0x54f8715, 0x5513332) without exceptions. This
supports a narrowly bounded diagnostic substitute: return generic-timer ticks
for exact faulting PMCCNTR reads, with a read ceiling and stop on other faults.
It is not CPU-cycle-equivalent or full PMU emulation. The own-child control must
verify target-register handling, preserved sentinels, and advancing values
before the next Windows run.


## Bounded counter substitute: control passes, Windows inconclusive

The own-child emulation control consumed three PMCCNTR reads targeting x0, x9
and XZR; values advanced, sentinel registers survived, and the expected BRK99
marker was captured. The Windows five-second run did not produce a final
recovery report. No Windows emulated-read count or subsequent PC was recovered.
This cannot establish even one successful Windows substitution, a reached
64-read ceiling, or failure of the PMU hypothesis. Do not extend the boot wait.

The next discriminator adds a one-shot breakpoint at RVA 0x35808, immediately
after the first failing read, alongside the tested counter handler. An own-child
post-read breakpoint control precedes Windows. This tests resumption after one
substitution before attempting a longer continuation trace.


## One Windows substitution resumes execution

The matched own-child post-read breakpoint passed. Windows then reached the
one-shot breakpoint at RVA 0x35808 with exactly one emulated PMCCNTR read at
RVA 0x35804. Captured x9=0xfa249b falls inside the independently sampled CNTVCT
interval [0xf8ce60, 0xfa249f]. Exact ELR/ESR confirms the following instruction
was reached. Raw receipt: `/tmp/cove-guest-state-20260907/postread-windows-report.txt`.
This proves one substitution resumes Windows, not later boot progress.

A concrete remaining probe limitation is that asynchronous vector slots freeze,
while firmware has left IRQ/FIQ unmasked in captured guest states. Next control
records vector identity and safely recovers from an ordinary interrupt, without
interpreting stale ESR or attempting IRQ forwarding. The changed vector prologue
must re-pass the synchronous emulation child and an own asynchronous control
before Windows. An interrupt receipt would diagnose instrumentation behavior,
not a Windows CPU fault.


## Interrupt receipt control does not resolve later loss

The modified vector prologue re-passed synchronous emulation. An own IRQ child
then produced vector slot 5 (EL1h IRQ) and preserved x16/x17 sentinels; ESR was
not interpreted for that asynchronous event. Windows nevertheless again produced
only its initial report within five seconds. No Windows interrupt, emulation
count or later PC was recovered. This does not establish IRQ absence or VBAR
replacement. Register-equality guards do not verify unchanged page-table contents.

Next discriminator is a short combined counter-substitution/continuation trace
with the already-tested stop-before-system-instruction and image-boundary guards.
Its purpose is to capture the next execution boundary before the handler becomes
unusable, not to lengthen the unobserved boot wait.


## Native PMU setting changes guest feature advertisement

Read-only Objective-C runtime inspection found
`VZGenericPlatformConfiguration._setPerformanceMonitoringUnitEmulationEnabled:`.
The generated private bindings already expose this setter. The normally signed
probe now has an opt-in `-pmu` flag, checks setter/getter availability and that
the setting was retained, then starts the same generic guest configuration.

The same COUNTER.EFI diagnostic booted successfully with ordinary virtualization
entitlements and reported ID_AA64DFR0_EL1=0x10305106, PMUVer=1 (baseline0).
Counter frequency/advance remained valid. Raw receipts:
`/tmp/cove-windows-native-pmu-20260907/counter-report.txt` and `counter.log`.
No host security change was required. Guest-side workaround work is paused;
the next run boots original WinPE media with native PMU enabled and unchanged
virtio graphics to measure actual boot progress.


## WinPE user mode established on Apple VZ

The 30-second Apple run reached WinPE user mode. A pristine clone of the
QEMU-validated receipt media produced WINPE-RECEIPT.TXT both before and after
wpeinit, reporting ARM64 Windows 10.0.26100.4349. See receipt-shim-winpe.txt.
The master had no receipt before boot. This establishes execution of Windows
userspace, not visible Setup, a usable desktop, or an installed Windows system.

The normally signed winbootprobe used -pmu with public virtio graphics and the
existing guest GOP shim. No AMFI or host security changes were needed. The
private generic-platform PMU setter changes guest PMUVer from 0 to 1; without
it, the unchanged Microsoft loader faults on PMCCNTR_EL0. Native PMU with
unmodified BltOnly GOP instead crashes the VM service. The PMU/shim combination
passes the startup receipt. The host crash's memory mapping owner remains
unresolved; do not infer a particular device from the SIGBUS alone.

Command and EFI hashes are in receipt-shim-command.json. The modified WIM hash
is 6444aab958243ca33eef0c3982943652b248b831cc721deebcc20bb74dfaf7ec;
its preparation and QEMU control are in ../winpe-receipt/. Raw media stay in
/tmp/cove-winpe-receipt-20260907 and /tmp/cove-windows-native-pmu-20260907.
receipt-run.py records the exact run procedure and relies on scratch assets;
it is a historical harness, not a standalone media builder.

The shim's reserved linear framebuffer does not forward direct Windows writes
to the host display after ExitBootServices. Rendering remains unresolved.
Private host linear-framebuffer configuration is rejected as a restricted
device. The next task is a supported guest display/output path and integration
of the tested PMU plus shim combination; neither PMU alone nor the untested
full-install path should become the default based on this receipt.


## Latest NotebookLM drift check

The native-pmu-winpe source family was synced and reviewed in the existing
conversation. The review agrees that the receipt proves WinPE userspace only,
with rendering, full installation and production integration unfinished. Its
wording that bypassing the fault "requires native PMU emulation" is too broad:
this is the tested successful path, not proof that alternatives cannot work.
Its unrelated iOS status paragraph is outside this experiment's scope and is
not adopted. Raw review: /tmp/cove-nlm-self-20260907/native-pmu-review.md.


## Hypervisor reference check and scope

The user-requested HVF assessment is complete. A bounded QEMU debugger control
proved register reads and a physical-memory read matching EDK2. First run had
an invalid multiprocess detach but rendered a post-resume command prompt;
corrected detach passed, while its post-resume screenshot was black. Combined
end-to-end debugger/display health remains unproven. No more QEMU variants are
needed for the present VZ objective; retain this as a qualified reference.

The notebook flags importing a full QEMU backend as a pivot away from VZ.
That is consistent with the current task: investigate a guest-side display
capture/transport next. Its claim that user-requested HVF *research* was drift
is not adopted; the user explicitly requested it. Its "structurally impossible"
HVF/VZ attachment claim is broader than evidence: no documented API was found.
Raw review: /tmp/cove-nlm-self-20260907/hvf-drift.md.


## Windows Setup captured inside Apple VZ

The same Go/Windows ARM64 GDI screen program passed QEMU first, then captured
three 1920x1200 PNGs inside Apple VZ with native PMU plus GOP shim. Viewed PNGs
show Setup and the expected missing-install.wim dialog. All capture calls
returned success. Raw images: /tmp/cove-winpe-screen-20260907/SCREEN{0,1,2}.PNG;
source, commands, text logs and image hashes: windows-efi/winpe-screen/.

The guest is rendering; the remaining problem is live frame transport and
input. This was offline PNG recovery after each bounded 30-second run, not
host framebuffer access or a live display. Network/agent transport remains
unqualified. Keep the next slice focused on that boundary, not more GOP shims.


## VirtIO network driver and DHCP pass on VZ

The existing PMU/shim boot stack plus public NAT NIC loaded ARM64 NetKVM with
drvload. ipconfig shows 192.168.64.53/24, DHCP/gateway192.168.64.1; pnputil
reports the matching VirtIO adapter Started. This clears driver binding and
address assignment, not live application transport. Receipts and source are
in windows-efi/winpe-network/. Next: bounded live frame upload and input reply.

Notebook review misread "no more GOP shims" as deleting the existing shim.
The actual code retains it. Its proposed requirement to inject the driver into
the WIM was also too narrow: loading the copied driver with drvload succeeds.
The distinction between KDNET and runtime networking is now directly measured.


## Live guest frame transport passes; shortcut effect unverified

The second host receiver run passed its local HTTP control and received six
Windows Setup PNGs during the 30-second VZ run. Guest GDI capture/upload returned
success six times. The first single-threaded receiver timed out; cause remains
unresolved, not proven to be firewall or a guest network fault. No host firewall
settings changed. Source/receipts: windows-efi/winpe-live/.

The host returned shift-f10 on frame0 and SendInput accepted four events, but
subsequent images did not show a command prompt. Live frame transport is proven;
the intended input effect remains unverified. Next inspect foreground/focus and
use a clearly observable input action, then build the live viewer. Do not call
automatic screenshot upload an interactive Windows desktop.


Alt+Space foreground check: six live frames again, SendInput success, foreground
Windows Setup, no visible menu or image change. Preserve as an input-effect
negative, not a network failure. Next own-window input control should test the
injection ABI and event delivery before more application-specific shortcuts.


## Visible text input round trip passes

The own Win32 EDIT control pumps messages on its creating OS thread. Host
response type-cove produced COVEINPUT: in GetWindowText and the uploaded PNG.
This proves actual Unicode input effects, unlike the Setup shortcut tests.
Six live frames arrived during the bounded VZ run. Receipts/source:
windows-efi/winpe-live/edit-control/. Next build a small viewer over the tested
frame/text channel, without claiming untested mouse or shortcut behavior.


## Viewer backend passes; browser check fails independently

A prototype local UI exposes live frames and a bounded text queue. Its HTTP
input endpoint queued VIEWER, and the WinPE EDIT control both reported and
rendered VIEWERINPUT: in a later live frame. Six frame uploads arrived. The
endpoint was exercised directly, not through a browser click. Headless Brave
produced Crashpad permission errors and timed out without a screenshot. Browser
layout/form interaction therefore remains unverified; no host permission was
changed. Exact source and receipts: windows-efi/winpe-live/viewer/.

The next product work is a persistent viewer with actual input validation and
integration with the normal cove Windows path. Full Windows installation remains
outside the proven WinPE result. Do not relabel the diagnostic EDIT window a
usable Windows desktop.


## Final scope review

The completion review agrees that original research requirements are satisfied
and landing remains blocked. Its invented "14 atomic commits" is rejected;
no such number appears in the original goal. The source/text index is ready,
HEAD remains062e4ab6, and the required helper still has no funded backend.
The user has been asked to restore credits or name an existing cgpt backend.
No further Windows experiments are needed solely to fill the wait.
