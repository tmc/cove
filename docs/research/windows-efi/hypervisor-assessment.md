# Hypervisor.framework for Windows

2026-09-07. Research only; no new VM launches or implementation.

**Yes, through QEMU HVF first.** Direct Hypervisor.framework gives a VMM control
of guest RAM and vCPU state, which addresses the visibility missing from our
VZ path. It does not provide a supported way to attach those controls to an
existing VZ VM. Building another VMM is unnecessary to test the benefit: our
successful QEMU control already uses `-machine virt,accel=hvf -cpu host` and
`ramfb`. See [its command](winpe-receipt/qemu-command.json).

The current Apple VZ result is **WinPE userspace reached**, with receipts before
and after `wpeinit`, using native PMU emulation plus the synthetic GOP shim.
The remaining gap is rendering the reserved RAM framebuffer after
ExitBootServices. The private native linear-framebuffer device is rejected;
PMU alone with Apple's BltOnly GOP crashes the service. These are distinct
results, not evidence that Windows still stops before WinPE. See
[native PMU evidence](native-pmu/README.md).

## What lower-level ownership buys

| Need | Direct Hypervisor.framework | Implication here |
| --- | --- | --- |
| Framebuffer bytes | `hv_vm_map` maps the VMM's own host memory into guest physical space. | A VMM that owns the RAM can read the framebuffer after firmware services end, once it knows the GPA, dimensions, format and stride. Rendering and synchronization remain its work. |
| Execution state | vCPU register/system-register access; exit information contains syndrome and virtual/physical addresses; debug-exception trapping is configurable. | A stopped vCPU can be inspected without installing a guest exception handler. Not every guest exception necessarily exits to the host. |
| Existing VZ guest | VM creation is process-scoped; vCPUs belong to their creating threads. | No documented attach/import API exposes another process's VZ RAM or vCPU handles. Calling these APIs from cove would manage a separate VM. |

These capabilities and ownership rules are documented in Apple's
[Hypervisor overview](https://developer.apple.com/documentation/hypervisor) and
[vCPU management](https://developer.apple.com/documentation/hypervisor/vcpu-management).
The installed SDK's `hv_vm.h`, `hv_vcpu.h`, and `hv_vcpu_types.h` confirm the
ARM64 declarations (core calls available from macOS 11). The public
[`com.apple.security.hypervisor` entitlement](https://developer.apple.com/documentation/bundleresources/entitlements/com.apple.security.hypervisor)
is required; this is separate from VZ's restricted-device entitlement.
The no-attach conclusion concerns the documented API, not every possible
private implementation technique.

Hypervisor.framework is not a Windows machine model. Firmware, ACPI/platform
tables, storage, input, display, interrupt/timer integration and device drivers
still need compatible implementations. Existing Go bindings in
`../apple/hypervisor/functions.gen.go` relative to the cove checkout expose
calls such as `HVVmCreate` and `HVVmMap`; they do not supply that machine model.

## Why reuse QEMU

QEMU 11.0.1's `ramfb_create_display_surface` maps the configured guest-physical
framebuffer into a host display surface; `ramfb_display_update` refreshes it.
That directly implements the host side missing from our synthetic VZ GOP.
It does not depend on later EFI Blt callbacks. See
[ramfb.c](https://github.com/qemu/qemu/blob/v11.0.1/hw/display/ramfb.c).

Its ARM HVF implementation also handles PMU registers, including PMCCNTR reads,
and implements debugger register synchronization, stepping and debug traps.
This is **QEMU behavior layered over HVF**, not evidence that a minimal
`hv_vm_create` guest automatically gets a compatible PMU. Source inspection
is pinned to the installed control's QEMU version, 11.0.1. See
[ARM HVF implementation](https://github.com/qemu/qemu/blob/v11.0.1/target/arm/hvf/hvf.c).

QEMU's GDB stub offers register and memory inspection, including a physical
memory mode. It can be a controlled reference for loader/PMU traces and
framebuffer contents. A matching Windows-media run under QEMU still has
EDK2 and QEMU devices, not Apple's firmware/device implementations; its trace
cannot establish what the VZ guest executed. Installed-binary debugger
behavior needs a short control before relying on it. See
[QEMU GDB documentation](https://www.qemu.org/docs/master/system/gdb.html)
(the online manual is newer than the pinned source).

## Ranked next options

1. **Use QEMU HVF as the rendering/debug reference, then evaluate a cove backend.**
   Boot and rendering are already demonstrated. The separate `cove-windows`
   checkout contains `cmd/cove/windows_qemu.go`, `qemu_display.go`, and QEMU
   control tests: inspect and reuse specific pieces rather than merging its
   unrelated history. Main does not yet have that live backend.
2. **Keep the VZ PMU/shim path as a separate integration experiment.** Its
   userspace receipt is valuable, but a guest display transport/driver or
   another VZ-specific RAM/display interface would still be needed. Direct
   HVF does not fill that hole in place.
3. **Build a bespoke HVF VMM only if QEMU integration proves unsuitable.**
   Owning RAM would solve capture access, but reproducing the working firmware
   and device environment is much more work than another API wrapper.

The lowest-cost *new* experiment is one disposable copy of the already
validated QEMU media with a local Unix-socket GDB stub. Pause after the startup
receipt, read PC/registers and a known guest-memory range, resume, and confirm
another screen capture. Pass means the installed HVF backend provides useful
non-guest-resident observability without losing the known render path. This
is a debug/backend qualification, not a repeat of whether Windows can boot.
If the immediate goal is only a usable display, skip even that experiment
and review the existing QEMU display integration against the known control.

## Installed-backend control

Register and firmware-backed physical-memory reads passed. The first run
rendered a command prompt after resume but its detach packet returned E22.
The corrected run acknowledged detach and reported running after resume, but
its final screenshot was black. Do not combine these runs into a fully passing
debugger/display control. See [receipts](hvf-debug/README.md). This remains a
QEMU reference, not VZ debugging or rendering.

## Delegated-exception setter evidence (2026-09-07)

E20D supplied a [probe and gate report](delegated-exits/delegated-exit-gate.md)
for macOS 27.0 build 26A5425a. Review of its source, all 64 recorded results,
and setter disassembly agrees with the reported restriction: singleton
exception classes 0..63 accept only 0x16. EC 0 and EC 0x18 are rejected.
The disassembly contains an unconditional comparison with 0x16 at
0x223fa42cc followed by a branch to rejection. We reviewed the supplied
artifacts; we did not rerun the probe. The disassembly copy removes one
trailing blank line; the other three copies preserve their bytes. Raw
originals remain untouched in `/tmp/vz-hv-survey/`.

This closes UNDEF/sysreg delegation through
`VZVirtualMachineStartOptions._setDelegatedExceptionClasses:` on this build.
It does not exclude other VZ hooks. Acceptance of 0x16 does not demonstrate
HVC delivery: callback receiver, completion outcomes, authorization, and a
live round-trip remain unverified. The PMU emulation toggle remains the
working PMU hook found in this investigation, not a proven exhaustive API.
