# WinPE startup receipt

The QEMU control booted modified Windows Setup ARM64 WIM index 2, wrote
`WINPE-RECEIPT.TXT` on the FAT USB disk before and after `wpeinit`, and launched
`X:\sources\setup.exe`. The recovered receipt reports Windows
10.0.26100.4349, ARM64, and `X:\windows`. Setup then displayed the expected
missing `install.wim` error; this media contains WinPE only.

`winpeshl.ini` launches `cmd.exe /c cove-receipt.cmd`. The script finds its
output volume by `COVEWINPE.TAG`, writes the explicit startup marker and
`ver` output, runs `wpeinit`, repeats the receipt, and invokes Setup. The
original WIM had `startnet.cmd` containing `wpeinit` and no `winpeshl.ini`.
A recovered receipt establishes execution of this WinPE userspace script.
An absent receipt does not establish that the kernel failed to boot: the
startup hook, storage visibility, writeback, or observation window may differ.
The MiniNT query produced no text in the QEMU receipt; no conclusion depends
on that query.

## Reproduction and fixed paths

The scripts are the tested scratch versions, preserved without refactoring.
They require macOS, Python 3, `wimlib-imagex` (tested 1.14.5), APFS clone support,
`hdiutil`, and the QEMU/EDK2 paths recorded in `qemu-command.json`.
`prepare.py` has these fixed inputs and output:

- Original WIM: `/tmp/cove-windows-20260906/winpe-tree/sources/boot.wim`.
- Original FAT disk: `/tmp/cove-windows-20260906/winpe-chainload-base.dmg`.
- Output directory: `/tmp/cove-winpe-receipt-20260907`.

For another run, change the script's input and output paths to external
scratch locations. Create a **new empty output directory** first; the script
does not reject existing outputs and must not be rerun over the evidence.
It clones the WIM and FAT disk, updates WIM index 2, mounts only the cloned
disk, replaces `sources/boot.wim`, adds the tag, then detaches it. Source
media must already have the `sources` directory and a bootable WIM index 2.
The preliminary `attach -nomount`/detach is redundant but was in the tested
script. Image binaries stay outside this repository.

The prepared master has not been booted and has no prior receipt. Clone it
for each experiment. The validated QEMU invocation was:

```sh
cp -c /tmp/cove-winpe-receipt-20260907/receipt-base.img /tmp/cove-winpe-receipt-20260907/qemu.img
python3 qemu-probe.py /tmp/cove-winpe-receipt-20260907/qemu.img /tmp/cove-winpe-receipt-20260907/qemu --seconds 30
```

Use fresh paths when repeating this command. The runner stops QEMU after
its bounded capture. Mount the stopped clone, recover the root
`WINPE-RECEIPT.TXT`, and detach it. Never mount a running VM's disk.

`manifest.json` records tested file sizes and hashes, including the original
WIM and prepared disk. It was extended after preparation with those two
hashes; the script itself initially records only the modified WIM and two
startup files. WIM metadata may vary on regeneration, so this is an evidence
manifest, not a claim of deterministic output. The QEMU receipt is copied
byte-for-byte. The command, status, and blockstats are also preserved; the
screenshot remains in external scratch at `qemu/screen.png`.

## Microsoft references

- [Winpeshl.ini reference](https://learn.microsoft.com/en-us/windows-hardware/manufacture/desktop/winpeshlini-reference-launching-an-app-when-winpe-starts?view=windows-11): `LaunchApps` runs executables sequentially with comma-separated arguments.
- [Wpeinit and Startnet.cmd](https://learn.microsoft.com/en-us/windows-hardware/manufacture/desktop/wpeinit-and-startnetcmd-using-winpe-startup-scripts?view=windows-11): `wpeinit` initializes Plug and Play devices and startup configuration.

Repository text copies use LF line endings and omit trailing blank lines.
Raw scratch receipts and Windows scripts remain byte-preserved at the recorded
paths; manifest hashes refer to those raw originals, not normalized copies.
