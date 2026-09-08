#!/bin/bash
set -euo pipefail
src=$(cd "$(dirname "$0")" && pwd)
out=${1:?usage: build.sh output-directory}
mkdir -p "$out"
linker=${LLD_LINK:-/opt/homebrew/opt/lld/bin/lld-link}
for mode in SHIM CHAINLOAD INSTALLED; do
    flags=(-UNO_SHIM)
    if [[ $mode == CHAINLOAD ]]; then flags=(-DNO_SHIM); fi
    if [[ $mode == INSTALLED ]]; then flags=(-UNO_SHIM -DINSTALLED_BOOT); fi
    xcrun clang -target aarch64-unknown-windows -ffreestanding -fshort-wchar \
        -mno-red-zone -fno-stack-protector -fno-stack-check -fno-builtin \
        -Wall -Wextra -Os "${flags[@]}" -c "$src/shim.c" -o "$out/$mode.o"
    "$linker" /subsystem:efi_application /entry:efi_main /machine:arm64 \
        /nodefaultlib /Brepro "/out:$out/$mode.EFI" "$out/$mode.o"
done
shasum -a 256 "$out/"*.EFI
