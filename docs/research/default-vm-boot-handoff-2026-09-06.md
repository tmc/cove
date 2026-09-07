# Handoff: default VM boot hang — deeper kernel debug

> Superseded by [the boot resolution](default-vm-boot-resolution-2026-09-06.md):
> disk corruption was confirmed and repaired. The conclusions below record the
> earlier investigation and are not the current diagnosis.

**Goal:** get the `default` VM launching and rendering correctly.

## Status: reinstall done, boot still hangs. Disk-corruption theory disproven.

Destructively reinstalled `~/.vz/vms/default.covevm` with macOS 26.6.2 (25G83, the only
host-supported IPSW per `VZMacOSRestoreImage.FetchLatestSupported`). Install completed clean
(22 GB, ASIF disk). **Every boot path hangs** with an identical signature:

- normal boot (cove `run`): black framebuffer, 100% CPU, flat ~174 MB RSS, no disk I/O, frozen 15+ min
- winbootgui `-macos` (vzkit.BuildMacVMConfig, 4 CPU/8 GB): **identical** hang
- recovery boot (cove `run -recovery`): **identical** hang (174 MB, 100% CPU, black)

Old (pre-reinstall) default hung earlier still — at firmware (white 512×424 GOP, ~28 MB RSS).

## What is ruled out
- Disk corruption (fresh clean install hangs too)
- cove config / cove itself (winbootgui+vzkit reproduces it)
- CPU/RAM (8/32 and 4/8 both hang)
- hw.model (byte-identical to working mlx-lm) and machine.id
- ASIF disk format (Apple `DICreateASIFParams`, legitimate)

## Working reference
`~/.vz/vms/mlx-lm.covevm` boots fine to login via cove. It was installed **months ago under an
earlier host OS** and is fully provisioned (has vz-agent). Config identical (8/32), hw.model identical.

## Leading theory
Virtualization.framework regression on this **macOS 27.0 pre-release seed (26A5425a)** that breaks
**fresh-guest first boot** (and even recovery), while pre-existing guests (mlx-lm) still run.

## Next debug steps (not yet done)
1. **Serial/virtio console capture** — attach a console device and capture guest EFI+kernel output;
   the framebuffer is black so the kernel log is the only window into where it stops. Check whether
   vzkit/cove can add a `VZVirtioConsoleDevice`/serial and tee it to a file. This is the highest-value step.
2. **Verbose boot-args (`-v`)** — needs a foothold to set guest NVRAM; recovery hangs so can't get a
   terminal. May need to set boot-args in the aux/NVRAM store offline if tooling exists.
3. **Fresh-install-elsewhere control** — install a brand-new throwaway macOS VM (not `default`) to
   confirm ALL fresh installs hang on this host (vs something bundle-specific). Space is tight (~17 GB free).
4. **Compare mlx-lm boot** live (CPU/RSS/screenshot trend) to calibrate the healthy signature on THIS host now.

## Environment / assets (this session)
- Scratchpad: `/private/tmp/claude-501/-Users-tmc2-go-src-github-com-tmc-cove-docs-research/bf03c6cf-f2ec-4af5-bb18-d41a72bced2c/scratchpad`
  - `cove` (adhoc-signed w/ vz entitlements, current build), `winbootgui` (signed), logs, screenshots
- Cached IPSW: `~/.vz/cache/RestoreImage.ipsw` (18 GB, 26.6.2/25G83 — reusable)
- Bundle identity backup: `default.covevm/reinstall-backup-*/` (aux, config, old snapshots)
- Sign any fresh cove build: `codesign -s - -f --entitlements internal/autosign/vz.entitlements <bin>`
- Host: macOS 27.0 seed 26A5425a, M4, 48 GB. Data volume ~17 GB free (deleted both Windows VMs to make room; mlx-lm preserved).
- Build state: cove `./...` green against regenerated `private/virtualization` bindings.

## Constraints (from CLAUDE.md)
- work in the main checkout; do not create commits (stage + `~/bin/git-auto-commit-message --auto`)
- don't stage binaries; never hand-write go.sum
- run sudo in an it2 split
- credits are LOW — be economical; don't spin on long boot waits.
