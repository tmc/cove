#!/bin/bash
set -euo pipefail
src=$(cd "$(dirname "$0")" && pwd)
out=${1:?usage: build-pci.sh output-directory}
mkdir -p "$out"
xcrun clang -target aarch64-unknown-windows -ffreestanding -fshort-wchar \
    -fno-stack-protector -fno-stack-check -fno-builtin -Wall -Wextra -Werror -Os \
    -c "$src/pci.c" -o "$out/pci.o"
"${LLD_LINK:-/opt/homebrew/opt/lld/bin/lld-link}" /subsystem:efi_application \
    /entry:efi_main /machine:arm64 /nodefaultlib /Brepro \
    "/out:$out/BOOTAA64.EFI" "$out/pci.o"
