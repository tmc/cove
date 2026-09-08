#!/bin/bash
set -euo pipefail
src=$(cd "$(dirname "$0")" && pwd)
out=${1:?usage: build-experiment.sh output-directory async|postread|hybrid [rva]}
kind=${2:?usage: build-experiment.sh output-directory async|postread|hybrid [rva]}
defines=(-DNO_SHIM)
case "$kind" in
    async) child=irq-child ;;
    postread) defines+=("-DSTOP_RVA=${3:?postread requires stop RVA}"); child=emu-child ;;
    hybrid) defines+=("-DBREAK_RVA=${3:?hybrid requires fault RVA}"); child=hybrid-child ;;
    *) echo 'kind must be async, postread, or hybrid' >&2; exit 1 ;;
esac
mkdir -p "$out"
cc=(xcrun clang -target aarch64-unknown-windows -ffreestanding -fshort-wchar
    -fno-stack-protector -fno-stack-check -fno-builtin -Wall -Wextra -Os)
linker=${LLD_LINK:-/opt/homebrew/opt/lld/bin/lld-link}
"${cc[@]}" "${defines[@]}" -c "$src/$kind.c" -o "$out/$kind.o"
"${cc[@]}" -c "$src/$kind.S" -o "$out/$kind-vector.o"
"$linker" /subsystem:efi_application /entry:efi_main /machine:arm64 /nodefaultlib "/out:$out/BOOTAA64.EFI" "$out/$kind.o" "$out/$kind-vector.o"
"${cc[@]}" -c "$src/$child.S" -o "$out/$child.o"
"$linker" /subsystem:efi_application /entry:efi_main /machine:arm64 /nodefaultlib "/out:$out/CHILD.EFI" "$out/$child.o"
