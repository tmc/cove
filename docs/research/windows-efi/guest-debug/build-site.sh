#!/bin/bash
set -euo pipefail
src=$(cd "$(dirname "$0")" && pwd)
out=${1:?usage: build-site.sh output-directory breakpoint-rva [site|trace|fault]}
rva=${2:?usage: build-site.sh output-directory breakpoint-rva [site|trace|fault]}
kind=${3:-site}
case "$kind" in site|trace|fault) ;; *) echo 'kind must be site, trace, or fault' >&2; exit 1;; esac
mkdir -p "$out"
cc=(xcrun clang -target aarch64-unknown-windows -ffreestanding -fshort-wchar
    -fno-stack-protector -fno-stack-check -fno-builtin -Wall -Wextra -Os)
linker=${LLD_LINK:-/opt/homebrew/opt/lld/bin/lld-link}
"${cc[@]}" -DNO_SHIM "-DBREAK_RVA=$rva" -c "$src/$kind.c" -o "$out/$kind.o"
"${cc[@]}" -c "$src/$kind.S" -o "$out/$kind-vector.o"
"$linker" /subsystem:efi_application /entry:efi_main /machine:arm64 /nodefaultlib "/out:$out/BOOTAA64.EFI" "$out/$kind.o" "$out/$kind-vector.o"
