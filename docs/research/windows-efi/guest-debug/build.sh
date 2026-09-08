#!/bin/bash
set -euo pipefail
src=$(cd "$(dirname "$0")" && pwd)
out=${1:?usage: build.sh output-directory}
mkdir -p "$out"
cc=(xcrun clang -target aarch64-unknown-windows -ffreestanding -fshort-wchar
    -fno-stack-protector -fno-stack-check -fno-builtin -Wall -Wextra -Os)
linker=${LLD_LINK:-/opt/homebrew/opt/lld/bin/lld-link}
"${cc[@]}" -Wno-unused-function -c "$src/trap.c" -o "$out/trap.o"
"${cc[@]}" -c "$src/trap.S" -o "$out/trap-vector.o"
"${cc[@]}" -DNO_SHIM -c "$src/entry.c" -o "$out/entry.o"
"${cc[@]}" -c "$src/entry.S" -o "$out/entry-vector.o"
"${cc[@]}" -c "$src/child.c" -o "$out/child.o"
"$linker" /subsystem:efi_application /entry:efi_main /machine:arm64 /nodefaultlib "/out:$out/TRAP.EFI" "$out/trap.o" "$out/trap-vector.o"
"$linker" /subsystem:efi_application /entry:efi_main /machine:arm64 /nodefaultlib "/out:$out/ENTRY.EFI" "$out/entry.o" "$out/entry-vector.o"
"$linker" /subsystem:efi_application /entry:efi_main /machine:arm64 /nodefaultlib "/out:$out/CHILD.EFI" "$out/child.o"
"${cc[@]}" -DNO_SHIM -c "$src/step.c" -o "$out/step.o"
"${cc[@]}" -c "$src/step.S" -o "$out/step-vector.o"
"$linker" /subsystem:efi_application /entry:efi_main /machine:arm64 /nodefaultlib "/out:$out/STEP.EFI" "$out/step.o" "$out/step-vector.o"
"${cc[@]}" -DNO_SHIM -c "$src/trace.c" -o "$out/trace.o"
"${cc[@]}" -c "$src/trace.S" -o "$out/trace-vector.o"
"$linker" /subsystem:efi_application /entry:efi_main /machine:arm64 /nodefaultlib "/out:$out/TRACE.EFI" "$out/trace.o" "$out/trace-vector.o"
"${cc[@]}" -c "$src/trace-child.S" -o "$out/trace-child.o"
"$linker" /subsystem:efi_application /entry:efi_main /machine:arm64 /nodefaultlib "/out:$out/TRACE-CHILD.EFI" "$out/trace-child.o"
"${cc[@]}" -c "$src/site-child.S" -o "$out/site-child.o"
"$linker" /subsystem:efi_application /entry:efi_main /machine:arm64 /nodefaultlib "/out:$out/SITE-CHILD.EFI" "$out/site-child.o"
"${cc[@]}" -c "$src/fault-child.S" -o "$out/fault-child.o"
"$linker" /subsystem:efi_application /entry:efi_main /machine:arm64 /nodefaultlib "/out:$out/FAULT-CHILD.EFI" "$out/fault-child.o"
"${cc[@]}" -Wno-unused-function -c "$src/counter.c" -o "$out/counter.o"
"${cc[@]}" -c "$src/counter.S" -o "$out/counter-vector.o"
"$linker" /subsystem:efi_application /entry:efi_main /machine:arm64 /nodefaultlib "/out:$out/COUNTER.EFI" "$out/counter.o" "$out/counter-vector.o"
"${cc[@]}" -DNO_SHIM -c "$src/emu.c" -o "$out/emu.o"
"${cc[@]}" -c "$src/emu.S" -o "$out/emu-vector.o"
"$linker" /subsystem:efi_application /entry:efi_main /machine:arm64 /nodefaultlib "/out:$out/EMU.EFI" "$out/emu.o" "$out/emu-vector.o"
"${cc[@]}" -c "$src/emu-child.S" -o "$out/emu-child.o"
"$linker" /subsystem:efi_application /entry:efi_main /machine:arm64 /nodefaultlib "/out:$out/EMU-CHILD.EFI" "$out/emu-child.o"
