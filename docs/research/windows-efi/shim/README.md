# Experimental RAM GOP shim

This EFI application replaces the GOP interfaces with a reserved RAM framebuffer,
then chainloads the Microsoft loader on its own boot volume. CHAINLOAD omits the
replacement. The two Apple runs both stalled; this is not a working Windows backend.

Build outside the checkout:

```sh
bash docs/research/windows-efi/shim/build.sh /tmp/windows-shim-build
clang -fno-builtin -fshort-wchar -Wno-incompatible-library-redeclaration \
  -fsanitize=address,undefined docs/research/windows-efi/shim/test-blt.c \
  -o /tmp/windows-shim-build/test-blt
/tmp/windows-shim-build/test-blt
```

Install either EFI artifact as EFI/BOOT/BOOTAA64.EFI on a separate writable copy
of the WinPE disk. Both require the original Microsoft loader at
EFI/Microsoft/Boot/bootmgfw.efi and its accompanying BCD/resources. Detach the
mounted image before booting, and recover SHIMLOG.TXT or CHAINLOG.TXT after shutdown.
Use the matched VZ configuration recorded in the experiment report.

The shim implements RAM Blt operations and mirrors writes to the firmware GOP.
Direct framebuffer writes bypass that mirror; there is no post-ExitBootServices
display transport. Protocol reinstall success does not prove Windows consumed
the replacement. Misaligned Blt strides are rejected. These limits matter when
interpreting a negative result.

See [experiment record](../../windows-boot-experiments-2026-09-06.md) for hashes,
controls, results, and remaining uncertainty.

The optional `trace.patch` applies to a scratch copy of shim.c. It arms one-shot
callback file logging immediately before StartImage, without console output,
and skips file writes unless the observed TPL is APPLICATION. It assumes GOP
callbacks run at or below TPL_NOTIFY, which it uses to sample the current TPL.
The trace is a separate diagnostic intervention; absent lines do not prove
absence of GOP use. Apply with `patch scratch/shim.c < trace.patch`, then build
with the same compiler/linker flags as build.sh.
