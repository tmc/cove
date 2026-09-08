# Guest EFI GOP probe

This standalone AArch64 UEFI application records all GOP handles and modes, the
active framebuffer, and ACPI table signatures. It writes `EFIDIAG.TXT` on its own
FAT volume, flushes after each section, prints persistence status to the console,
and returns to firmware after three seconds. It does not chainload Windows yet.

Build with Apple clang and Homebrew `lld` (or set `LLD_LINK`). Output stays outside
the checkout. Guest Secure Boot must be off; this guest PE image needs no macOS
code signature. Sign the host Go binary with the virtualization entitlement.

Run from the repository root, using fresh output paths:

```sh
bash docs/research/windows-efi/build.sh /tmp/windows-efi-build
go build -o /tmp/windows-efi-build/winbootprobe ./cmd/winbootprobe
codesign -s - -f --entitlements cmd/cove/vz.entitlements /tmp/windows-efi-build/winbootprobe
hdiutil create -size 64m -fs 'MS-DOS FAT32' -volname EFIPROBE -layout GPTSPUD /tmp/windows-efi-build/base.dmg
hdiutil attach /tmp/windows-efi-build/base.dmg -nobrowse
```

Use the device and mount path returned by attach; do not assume a disk number.
Create `EFI/BOOT` on that volume and copy `BOOTAA64.EFI` there. Record its SHA-256
and detach the image before any VM attaches it. Preserve this pristine base and
copy it separately for each experiment; fresh copies also avoid stale report tails.

```sh
cp -c /tmp/windows-efi-build/base.dmg /tmp/windows-efi-build/apple.img
/tmp/windows-efi-build/winbootprobe -efi /tmp/windows-efi-build/apple.img -graphics virtio -seconds 12 -nocap
cp -c /tmp/windows-efi-build/base.dmg /tmp/windows-efi-build/qemu.img
python3 docs/research/windows-efi/qemu.py /tmp/windows-efi-build/qemu.img /tmp/windows-efi-build/qemu-output
```

The QEMU runner uses the local Homebrew QEMU/EDK2 installation, HVF, ramfb, a USB
boot disk, no networking, a bounded observation, QMP capture, and process cleanup.
After each VM exits, attach its image read-only and copy out `EFIDIAG.TXT`, then
eject the returned device. Do not mount an image while its VM runs.

The Apple capture-enabled run completed the report but subsequently encountered a
VM service error. The no-capture runs completed and shut down cleanly; use `-nocap`
for this diagnostic. This does not establish the cause of the capture failure.

## Recorded measurements

[Apple without capture](results/apple.txt) (also [initial capture run](results/apple-capture.txt)): two GOP handles, 37 modes each, all BltOnly; active
framebuffer base and size zero. [QEMU ramfb](results/qemu-ramfb.txt): two handles,
three linear BGR modes each; nonzero framebuffer base and 3,145,728-byte backing.
Handles may alias the same underlying protocol; this is not a count of displays.

Original tested EFI SHA-256:
`c5b09c4932a39b277b59fc1d3be7e0348a4f6484c9bfbd594e5a296e0aae8c41`.
PE timestamps can change on rebuild; record each binary's actual hash.

These are EFI diagnostic results, not Windows boot results. They establish the GOP
difference and working report persistence. The same-media Windows control subsequently reached WinPE under QEMU ramfb;
the Apple shim comparison did not unblock the loader. See [experiment record](../windows-boot-experiments-2026-09-06.md) and the [current observability record](../windows-notebook-anchor-2026-09-07.md).

QEMU serial output also recorded `PERSIST: ok (EFIDIAG.TXT written+flushed)`.
Apple persistence was verified by reading the complete report after guest shutdown;
its console was not wired to serial.

Checked-in report copies omit trailing spaces; raw files remain in the recorded
scratch directory and guest disk images. No field values were otherwise changed.
