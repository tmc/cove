#!/bin/bash
set -euo pipefail
src=$(cd "$(dirname "$0")" && pwd)
out=${1:?usage: build.sh output-directory}
mkdir -p "$out"
linker=${LLD_LINK:-/opt/homebrew/opt/lld/bin/lld-link}
if [[ ! -x "$linker" ]]; then
    echo 'error: set LLD_LINK to lld-link or install Homebrew lld' >&2
    exit 1
fi
xcrun clang -target aarch64-unknown-windows -ffreestanding -fshort-wchar \
    -fno-stack-protector -fno-stack-check -fno-builtin -Wall -Wextra -Os \
    -c "$src/diag.c" -o "$out/diag.o"
"$linker" /subsystem:efi_application /entry:efi_main /machine:arm64 \
    /nodefaultlib "/out:$out/BOOTAA64.EFI" "$out/diag.o"
