# Windows boot experiments

Aligned with session 7EB19DC7 on 2026-09-06. The previous audit demonstrated
restricted-device entitlement checks and stalled Windows-media boots. It did not
establish that a linear framebuffer alone is sufficient to boot Windows.

## Current status (2026-09-07)

Windows reaches WinPE user mode on Apple VZ with native PMU emulation and the
guest GOP shim. A fresh 30-second run wrote startup receipts before and after
wpeinit, reporting ARM64 Windows 10.0.26100.4349. The same modified WIM passed
QEMU first. [Current handoff](windows-efi/DEBUG-HANDOFF.md) and
[raw receipt](windows-efi/native-pmu/receipt-shim-winpe.txt).

Guest EL1 instrumentation identified the earlier PMCCNTR_EL0 fault. Native
PMU support clears that compatibility blocker; the GOP shim is also needed in
the tested combination. Guest-side rendering, live PNG transport and visible Unicode text input now
pass. Full installation remains unverified; see the [completion audit](windows-efi/COMPLETION-AUDIT.md).
Earlier sections below preserve chronological findings and obsolete next steps.
Source and text evidence remain uncommitted because the required commit helper
backend reports insufficient credits.

## Order and decision points

1. Use a deterministic scratch ESP: an explicit EFI application at
   `\EFI\BOOT\BOOTAA64.EFI`, fresh NVRAM, recorded SHA-256 hashes, and a writable
   diagnostic report. Do not rely on shell FS numbering or an ambient `/tmp` file.
2. Boot a guest EFI diagnostic on Apple firmware. Record every GOP mode, current
   pixel format, framebuffer base/size, ACPI tables, and explicit chainload status.
3. In parallel, establish a QEMU ramfb positive control using the same Windows
   boot files. Reaching WinPE is stronger evidence than a running VM or disk reads.
4. Only if Apple exposes BltOnly and the QEMU control boots, test a RAM-backed GOP
   shim. Compare unmodified and shimmed boots with identical media/configuration.
   Distinguish boot-manager progress, kernel entry, WinPE, and a usable display.
5. Prepare a private linear-framebuffer test on a spare macOS installation only
   if results justify it. Changing the spare's host security policy requires an
   operator decision. Starting the private device does not establish that Apple's
   firmware exposes it through GOP.

A QEMU success is a media control, not proof that GOP is the sole difference:
QEMU and VZ also differ in firmware, ACPI, and devices. Likewise, a RAM-backed GOP
may advance the loader without providing visible graphics after ExitBootServices.

## Sources

- [Previous local audit](macos27-windows-virtualization-2026-09-05.md).
- [EDK2 VirtioGpuDxe](https://github.com/tianocore/edk2/blob/master/OvmfPkg/VirtioGpuDxe/Gop.c)
  initializes GOP as PixelBltOnly. This is upstream behavior; Apple must be measured.
- [Microsoft GOP test](https://learn.microsoft.com/en-us/windows-hardware/test/hlk/testref/6afc8979-df62-4d86-8f6a-99f05bbdc7f3)
  requires a physical framebuffer.
- [QEMU ARM64 graphics guidance](https://www.qemu.org/docs/master/system/whpx.html)
  describes virtio GPU's linear-framebuffer incompatibility with Windows.
- [Official Windows ARM64 media](https://www.microsoft.com/en-us/software-download/windows11arm64).

## Initial inventory

The old Windows VZ and QEMU bundles and installer ISO are absent. QEMU 11.0.1 and
its ARM64 EDK2 firmware are installed. The macOS default VM is outside this experiment.
The existing `winbootprobe` required a Windows disk and silently ignored EFI-store
copy failures; its fixed capture schedule also exceeded short observation windows.
The scratch EFI path removes those dependencies and waits for the stop callback.

Artifacts are under `/tmp/cove-windows-20260906` (host/media) and
`/tmp/cove-windows-efi-20260906` (guest diagnostic).

## Validation and current external dependencies

`go build ./...` passed. `go test ./...` failed; the previously documented
`TestPrivateAPI_NameGetSet` SIGTRAP was reproduced in isolation.
`go test ./... -skip '^TestPrivateAPI_NameGetSet$'` passed (cmd/cove: 146.506s).
The signed harness rejects nonpositive observation windows and missing EFI disks.

Microsoft's download page returned the English ARM64 SKU, but its download-link
endpoint returned `ErrorSettings.SentinelReject`. An existing ISO or an official
user-generated download link is pending; Windows-media experiments cannot yet run.
The diagnostic and QEMU EFI control do not depend on that media.

The required commit helper failed with an Anthropic insufficient-credit error.
Selecting its OpenAI backend instead failed because no API key was configured.
No commit was created by these attempts. The harness, diagnostic source, and
records are staged together as one reproducible diagnostic change.

## Guest measurements

The [reproducible probe and raw reports](windows-efi/README.md) now establish:

| Firmware | GOP handles / modes each | Active framebuffer | ACPI table signatures |
| --- | --- | --- | --- |
| Apple VZ EFI | 2 / 37; all BltOnly | base 0, size 0 | FACP GTDT APIC MCFG |
| QEMU EDK2 + ramfb | 2 / 3; all BGRReserved8 | base 0x13c7a0000, size 3145728 | FACP APIC PPTT GTDT MCFG SPCR DBG2 IORT BGRT |

Both complete reports were recovered from independent copies of the same FAT disk.
The handles may alias one implementation. ACPI presence differences are recorded,
not diagnosed as Windows blockers. Apple had no hidden linear GOP mode in this run.

The first Apple capture-enabled run persisted its complete report, then the VM
service errored near the initial screenshot. A no-capture repeat persisted the
same findings and cleanly shut down. The harness now recognizes clean guest exit
without attempting to stop an already stopped VM. Use no-capture for this probe;
the cause of the screenshot interaction remains unestablished.

This satisfies the Apple-side gate for the shim. QEMU EFI diagnostic success does
not satisfy the Windows-media gate: WinPE has not yet been booted. No shim or host
security-policy change was attempted.

The final no-capture harness run exited successfully after the guest shut down.
QEMU serial output additionally confirmed `PERSIST: ok`; Apple report persistence
was independently verified by mounting the stopped guest disk read-only.

## Branch review: cove-worktree-windows-current (2026-09-07)

The user identified this Git branch, not a directory. Local tip `d0b7dec7`
contains a direct QEMU/HVF Windows backend, default `ramfb+virtio-gpu-pci`,
persistent EFI variables, NVMe storage, USB install media, and automatic boot
keypresses. Its cached remote history at `7a8a0bf1` records July 2 Windows desktop
and agent verification in `docs/designs/044-qemu-display-window.md`; some
interactive display checks remained unverified. This is prior QEMU evidence,
not a VZ Windows boot result. The branch was read without checkout or merge.

Its Windows guide also identifies an ESD media route that the initial inventory
missed. The HTTPS Microsoft catalog at `https://go.microsoft.com/fwlink?linkid=2156292`
was fetched successfully. Its published ARM64 en-US ESD URL responds with HTTP 200
and size 4,527,171,158 bytes. The image is downloading to the existing scratch
folder; its catalog SHA-1 will be verified before extraction. Installed Homebrew
wimlib-imagex 1.14.5 and mkisofs run successfully. CrystalFetch's bundled wimlib
traps on this host, so use the working Homebrew executable. The earlier request
for a user-supplied ISO is no longer the only media path.

The branch also already provides `-windows-gop-shim`, installs an external EFI
application at the fallback boot path, checks for the Microsoft boot manager, and
hashes the shim for its boot-image cache. No guest shim implementation was found
in its tree. Reuse that integration if the conditional shim experiment succeeds;
this flag alone is not evidence of a working Windows-on-VZ shim.

## Windows positive control (2026-09-07)

The actual ESD download matched 4,527,171,158 bytes and catalog SHA-1
`c78fd344e845d3b17cb91c40bf4a856459da1b6c`. Image 1 supplied boot files; images
2 and 3 were exported into `sources/boot.wim`, with Setup marked bootable.
These files were copied to a fresh FAT32/GPT disk with Microsoft's original
`EFI/BOOT/BOOTAA64.EFI`. No diagnostic or shim was installed in this image.

QEMU ramfb booted this disk to the Windows 11 Setup WinPE GUI within a 45-second
observation. Screenshot: `/tmp/cove-windows-20260906/qemu-winpe/screen.png`.
The GUI asks to install a media driver; this WinPE-only image has no install.wim
and the test VM has no installation target. This is a WinPE boot result, not a
completed Windows installation or proof that Setup can install with this config.
Disk reads advanced from 378,738,176 bytes at six seconds to 601,008,640 bytes at
45 seconds. The screenshot, rather than these counters, establishes WinPE entry.

The same-media QEMU gate is now satisfied. The guest-side GOP shim is the next
experiment; successful QEMU boot does not establish Windows-on-VZ compatibility.

## Same-media Apple baseline

A pristine copy of the exact FAT image that reached QEMU WinPE was booted under
VZ with no shim and no screenshot capture. The service read 6,475,776 bytes by
six seconds and exactly the same total at 20 seconds, with about 172 MB RSS and
335 ms CPU over that interval. It remained Running until the bounded 24-second
stop. [Raw process counters](windows-efi/results/apple-winpe-baseline.json) and
[media hashes](windows-efi/results/winpe-media.json) preserve this baseline.

The QEMU control used two CPUs / 4 GiB; VZ used four CPUs / 8 GiB. Comparisons
between these hypervisors also differ in firmware and device models. The upcoming
shim comparison must retain the VZ baseline configuration and media, changing only
the boot shim. Neither disk-read progress nor a synthetic GOP by itself proves
usable graphics after ExitBootServices or a shippable Windows backend.

## Apple chainloader control

For the matched chainloader/shim pair, a separate pristine base copies Microsoft's
original fallback loader to `EFI/Microsoft/Boot/bootmgfw.efi`. Both copies have
SHA-256 `26085cabe01870a8921b61efd135a59898a51daae25b64b6bedba5830fb472f9`.
The fallback entry is then replaced by the control or shim, on independent clones.

The chainloader-only control leaves GOP untouched. Its recovered
[CHAINLOG.TXT](windows-efi/results/apple-chainload.txt) confirms successful
LoadImage and entry into StartImage, with no return recorded. The service read
5,619,712 bytes at both six and 20 seconds during the same 24-second bounded run
([counters](windows-efi/results/apple-chainload.json)). Thus the changed entry
path still stalls; its exact byte total differs from the unmodified baseline.
The zero process write counter does not mean the report was unwritten: the file
was recovered from the detached guest image.

Tested CHAINLOAD.EFI SHA-256:
`aecd784ec7c4d9207366e9229c8176cce3611784cad4cf73e7780d936e6e0f3e`.
Raw artifacts: `/tmp/cove-windows-20260906/apple-chainload/`.
The first shim was held for correction of RAM framebuffer Blt semantics before
testing; a negative result requires reviewing those semantics, not just successful
protocol installation.

A clone of this exact chainloader-control disk also reached the Windows Setup
WinPE GUI under QEMU ramfb at 45 seconds, with 601,172,480 disk-read bytes.
Screenshot: `/tmp/cove-windows-20260906/qemu-chainload/screen.png`.
This validates the additional same-device chainload path under EDK2.

## Corrected RAM GOP shim result

SHIM v2 replaced both GOP handles successfully and advertised BGRReserved8
1920x1200, framebuffer base 0x26d850000, size 9,216,000 bytes. The recovered
[SHIMLOG](windows-efi/results/apple-shim-v2.txt) records successful LoadImage
and entry into StartImage without a recorded return. Disk reads were exactly
6,590,464 bytes at six and 20 seconds, with about 173 MB RSS and 336 ms CPU
over the interval ([counters](windows-efi/results/apple-shim-v2.json)).
The larger total than the chainloader control is not sustained progress and
does not establish a Windows loader milestone.

This implementation did not unblock boot within the matched observation.
Successful protocol replacement does not prove that Windows consumed it;
the test does not eliminate other linear framebuffer implementations or
identify the remaining blocker. There is no basis here for calling the shim
shippable or changing host security settings for a private framebuffer test.

Tested SHIM SHA-256:
`bab5bd9e8822c51c0cc03713f63912886d51a510d5cd7d4ad6c3cc2ba876ef40`.
[Source, build, and framebuffer tests](windows-efi/shim/README.md) are preserved.
The repository build reproduces that exact hash. Address/undefined sanitizer
checks passed for fill, synthetic readback, padded stride, overlapping copies,
overflowing video coordinates, and mode validation. These tests check those
operations; they are not a complete UEFI conformance certification.

Raw artifacts remain in `/tmp/cove-windows-20260906/apple-shim-v2/`; original
guest binaries and source revisions remain in `/tmp/cove-windows-shim-20260907/`.

## Callback trace follow-up

A separate [trace patch](windows-efi/shim/trace.patch) arms one-shot file records
for QueryMode, SetMode, and Blt immediately before StartImage, avoiding console
recursion and skipping writes above TPL_APPLICATION. The tested trace EFI hash is
`d630eb895bc987d24e679d79bb84d7b84a0de424276bcbde8b7349affe0406ff`.
Apple again installed both interfaces and loaded Microsoft successfully, then
stalled at 6,602,752 bytes at both samples, with no callback records in the
[recovered report](windows-efi/results/apple-shim-trace.txt).

The same traced image under QEMU also produced no callback records, while disk
reads advanced from 378,181,120 to 601,172,480 bytes by 20 seconds. Its screenshot
retained EFI text, consistent with the shim lacking a direct-write scanout path;
this variant does not have a visually confirmed WinPE milestone. The original
and chainloader-only QEMU controls do have that milestone. See the QEMU
[report](windows-efi/results/qemu-shim-trace.txt) and
[counters](windows-efi/results/qemu-shim-trace.json).

Absent callback records cannot establish that Apple parked before graphics use:
direct mode/framebuffer access is invisible, and skipped or failed file writes
are also possible. The QEMU comparison demonstrates extensive loader I/O without
these records. Further useful work requires stronger loader or guest-memory
observability; repeating blind shim changes is not justified by this result.
Private framebuffer work remains gated. Source and evidence are staged without
binaries; landing remains blocked by the previously recorded commit-helper
credit/backend failure.

## Guest debugger launch attempt

Added an opt-in loopback GDB port to winbootprobe using the same vzkit debug-stub
hook as cove. Built a scratch binary with a unique signing identifier and the
private virtualization entitlement. The user authorized use of the installed
`/Users/tmc2/go/bin/amfidont`, and entered sudo authentication in iTerm split
92C688A6. No SIP or boot-argument changes were made.

The launcher was killed with exit 137 during attachment, before reporting child
launch. The crash report records EXC_GUARD, SET_EXCEPTION_BEHAVIOR, with
`task_set_exception_ports` in the faulting stack. See the
[termination summary](windows-efi/results/amfidont-gdb-failure.json).
This is a launcher failure, not evidence that the guest GDB stub was rejected.
The amfid daemon remained running and ordinary process launches still worked.
BBFA, the tool author, received the exact command, log, and crash report and is
investigating. No privileged retry was made after this failure.

Command and raw log: `/tmp/cove-windows-gdb-20260907/{run.sh,launch.log}`.
The probe build and `go vet ./cmd/winbootprobe` passed.

Control: the same probe with normal virtualization entitlements starts and runs
for eight seconds without GDB, but adding `-gdb 12347` causes immediate VM startup
failure after configuration validation. This is consistent with the restricted
stub gate; the generic startup error alone does not identify its internal check.
Logs: `/tmp/cove-windows-gdb-20260907/public-{no-gdb,launch}.log`.
BBFA identified EXCEPTION_DEFAULT in the installed tool's Mach driver as a
candidate cause of the guard violation and is validating a fix on a test child
before another amfid attach.

BBFA's follow-up: identity-protected exception registration passed ordinary
child breakpoint/watchpoint/detach tests, but a hardened child was killed with
CODESIGNING/Invalid Page when executable text was patched. No validated launcher
replacement was supplied. The privileged retry remains stopped; these findings
do not establish whether the guest GDB stub would work after authorized launch.
