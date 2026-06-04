# Design 049: Plan 9 on Virtualization.framework

Status: Feasibility, blocked on guest boot and graphics work.
Date: 2026-05-17

## Goal

Run Plan 9 or 9front through Apple's Virtualization.framework without QEMU.

The useful end state is graphical 9front in a Cove window. The first viable
native VZ milestone is smaller: serial-only arm64 9front booting far enough to
mount its disk and accept commands.

## Findings

Virtualization.framework is not an emulator. On Apple Silicon, Cove can build
generic arm64 virtual machines with EFI, virtio block, virtio network, virtio
serial, virtio graphics, USB input, vsock, and VirtioFS. It cannot run an x86
Plan 9 guest and it cannot expose QEMU's PC BIOS, `virt` machine version, GIC
selection, `-bios`, or TFTP/user-network helper devices.

`rsc/plan9` is not a native VZ target. Its documented boot path is
`qemu-system-x86_64`; the boot script starts an x86 VM, uses a raw PXE disk,
serves TFTP from the checkout, and forwards 9P through QEMU user networking.
Its Plan 9 configuration selects `amd64/9k10`, VESA graphics, and a PS/2 mouse.
That is a good QEMU experience, but it maps poorly to VZ.

The 9front arm64 QCOW is the only obvious ISA match for Apple Silicon, but its
official FQA documents it as a QEMU/hypervisor image that is serial-only. It
requires QEMU's `virt` machine, GICv3, non-transitional virtio PCI devices,
U-Boot supplied as `-bios u-boot.bin`, and serial console operation. The same
section explicitly says the image does not provide a graphical interface.

VZ disk attachments accept RAW and ASIF disk images. The official arm64
artifact is QCOW, so Cove also needs either a raw upstream artifact, a converter
step, or a small QCOW-to-raw import path before VZ can attach the disk.

## Consequence

Native graphical Plan 9 is not a Cove-only integration task today. A native VZ
implementation needs Plan 9/9front guest work:

- an arm64 boot path compatible with `VZEFIBootLoader`, probably a
  `BOOTAA64.EFI` loader or a U-Boot EFI chainload path;
- platform discovery for the device tree or ACPI tables VZ presents;
- PCI, GIC, and ECAM behavior compatible with VZ's generic platform;
- virtio block and virtio network enumeration under VZ;
- a serial console path for early bring-up;
- a virtio-gpu 2D draw driver and USB/input handling before `rio` can be
  graphical in a VZ window.

Host file sharing should not start with VirtioFS. Plan 9's natural boundary is
9P, so the first useful host export should be 9P over TCP or vsock once the
guest can boot and network.

## Host Shape

The Cove side should use a separate VM kind once a bootable guest artifact
exists:

```
~/.vz/vms/plan9front/
  disk.img          # RAW or ASIF, not QCOW
  efi.nvram         # VZ EFI variable store
  machine.id        # generic VZ machine identifier
  serial.log        # boot evidence
```

The VM configuration should be deliberately small:

- `VZGenericPlatformConfiguration`;
- `VZEFIBootLoader` with a per-VM variable store;
- one `VZVirtioBlockDeviceConfiguration` for the raw disk;
- one `VZVirtioNetworkDeviceConfiguration` with NAT for early testing;
- one `VZVirtioConsoleDeviceSerialPortConfiguration` writing to `serial.log`;
- `VZVirtioGraphicsDeviceConfiguration`, USB keyboard, and USB pointing only
  after the guest has a graphics/input driver path.

`VZLinuxBootLoader` should be considered only if 9front can produce an arm64
image conforming to the Linux arm64 boot protocol. Assume EFI until proven
otherwise.

## Implementation Slices

Slice 1: host-side probe only.

Add a hidden `cove plan9 probe-vz -disk disk.img` that constructs the generic
VZ configuration, validates it with `ValidateWithError`, writes the exact
device plan as JSON, and refuses to call the result supported. This catches
Cove-side mistakes without pretending the guest boots.

Slice 2: serial boot harness.

Create or require an EFI boot artifact, attach a raw 9front arm64 disk, start
the VM with serial logging, and define the first success gate as a loader banner
in `serial.log`. If the VM drops to EFI shell, that is expected evidence, not a
failure of the harness.

Slice 3: guest boot work.

Patch or configure 9front so its arm64 kernel boots on VZ's generic platform and
sees the virtio block device. The success gate is a Plan 9 prompt over serial.

Slice 4: network and host export.

Bring up virtio-net, then attach host services over 9P. Prefer a small host 9P
server over TCP first; evaluate vsock after basic network boot is stable.

Slice 5: graphics.

Add or enable a 9front driver for VZ's virtio-gpu scanout plus USB keyboard and
pointing input. The success gate is `rio` visible through Cove's normal VZ
graphics path and a captured screenshot proving nonblank output.

## Non-goals

- Do not land a QEMU runtime wrapper as `cove plan9`.
- Do not present `plan9port` as VM support; it is useful host userland, not a
  guest.
- Do not use `plan9front.qcow2` as a Cove VM validity marker. VZ cannot attach
  QCOW directly.

## References

- Apple Virtualization disk attachments support RAW and ASIF:
  `github.com/tmc/apple/virtualization/vz_disk_image_storage_device_attachment.gen.go`.
- Apple Virtualization framework overview and VZ device bindings:
  `github.com/tmc/apple/virtualization/doc.gen.go`.
- rsc/plan9 boot instructions: <https://github.com/rsc/plan9>.
- rsc/plan9 QEMU launcher:
  <https://raw.githubusercontent.com/rsc/plan9/main/boot/qemu>.
- 9front arm64 QCOW FQA:
  <https://fqa.9front.org/fqa3.html#3.3.1.1.1>.
