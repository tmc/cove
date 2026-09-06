---
title: Installation
description: Install cove from source, put it on PATH, and check the host before building a VM.
icon: rocket
---
# Installation

## Requirements

- Apple Silicon Mac (M1/M2/M3/M4)
- macOS 14.0+ (Sonoma or later)
- Xcode Command Line Tools (`xcode-select --install`)
- Disk space for the guest. `cove up` creates a 64 GB disk image by default,
  but it is sparse: a fresh macOS install consumed about 6 GB in our test run.
  Size for growth, not for the initial write.

## Windows guest prerequisite: QEMU

macOS and Linux guests need nothing beyond the requirements above. Windows
guests run on the direct QEMU/HVF backend, which shells out to
`qemu-system-aarch64` and `qemu-img` and boots from the EDK2 AArch64 pflash
images (`edk2-aarch64-code.fd`, `edk2-arm-vars.fd`) that ship in the same
formula:

```bash
brew install qemu
```

Check that cove finds all of it before creating a Windows VM:

```bash
cove doctor qemu
```

The check reports each tool and firmware file it resolved. `COVE_QEMU_SYSTEM_AARCH64`,
`COVE_QEMU_IMG`, `COVE_QEMU_EFI_CODE`, and `COVE_QEMU_EFI_VARS_TEMPLATE` override
the lookup when QEMU is installed somewhere off `PATH`.

## Build from a checkout

Building from a checkout is the only current install path.

```bash
git clone https://github.com/tmc/cove
cd cove
go build -o cove ./cmd/cove
```

Put the resulting binary on `PATH`, then verify it:

```bash
cove version
cove doctor host
```

> [!WARNING]
> The build needs an unreleased `github.com/tmc/apple` that provides
> `x/codesign` and `x/guest/portfwd`. No tagged version supplies them, so the
> checkout carries a `go.work` overlay pointing at a local
> `github.com/tmc/apple` checkout. Without that checkout the build stops at:
>
> ```text
> cmd/cove/autosign.go:8:2: no required module provides package github.com/tmc/apple/x/codesign
> internal/controlserver/port_forward.go:19:2: no required module provides package github.com/tmc/apple/x/guest/portfwd
> ```
>
> We have not verified a clean-machine build, because the overlay checkout is
> the only configuration we can currently build in.

`go install` does not work yet. The published module is `v0.6.0`, which
predates both the rename to `github.com/tmc/cove` and the move of the binary
to `cmd/cove`, so neither spelling resolves:

```text
$ go install github.com/tmc/cove/cmd/cove@latest
module github.com/tmc/cove@latest found (v0.6.0), but does not contain package github.com/tmc/cove/cmd/cove

$ go install github.com/tmc/cove@v0.6.0
module declares its path as: github.com/tmc/vz-macos
        but was required as: github.com/tmc/cove
```

`go install github.com/tmc/cove/cmd/cove@latest` starts working once a release
is tagged after the rename. The Homebrew formula is not a recommended path
either. Guest account setup remains explicit: the first VM asks for a username
and prompts for a password when `-password` is omitted. Cove does not create
fixed default guest credentials.

The Go module path is `github.com/tmc/cove`.

### Entitlements

cove auto-signs itself on first launch with the required Virtualization.framework entitlements. No manual step is needed for normal use.

> [!WARNING]
> Manual signing is only needed if autosigning fails or you bypass normal
> startup while developing cove.

If you need to sign manually:

```bash
codesign -s - -f --entitlements cmd/cove/vz.entitlements "$(command -v cove)"
```

Required entitlements:
- `com.apple.security.virtualization` -- basic VM capability
- `com.apple.security.network.client` and `com.apple.security.network.server` -- local control, gateway, and guest-service networking

## Verify Installation

```bash
cove version
cove doctor host
```

`cove doctor host -json` emits the same readiness checks as a JSON report.

## First-Run Prompts

`cove up -user <name>` prompts for the guest account password when `-password`
is omitted. Prefer the prompt so the password is not saved in shell history.

macOS may ask for administrator approval when cove mounts a guest disk, writes
root-owned launchd files into the guest, or installs/updates the optional
privileged helper. These prompts authorize local VM preparation on this Mac.

GUI VM runs add a macOS status item for the active VM. It shows the current VM
state and exposes quick actions for the native window and clean shutdown. The
status item exists for the running VM session; installing cove does not add a
background login item.

## Apple SLA Note

macOS guests are governed by Apple's macOS Software License Agreement, not just
cove's MIT license. The current
[macOS Tahoe 26 SLA](https://www.apple.com/legal/sla/docs/macOSTahoe.pdf)
section 2B(iii) permits up to two additional virtualized macOS copies or
instances on each Apple-branded computer you own or control, for the listed
development, testing, macOS Server, or personal non-commercial purposes. Except
as separately permitted by Apple, it also excludes service bureau, time-sharing,
terminal sharing, relay service, and similar services.

cove does not work around that limit. Read the applicable SLA for the macOS
version you run: <https://www.apple.com/legal/sla/>. This section is a
product disclosure, not legal advice.

## macOS Restore Image Cache

`cove install` caches Apple's IPSW restore image under `~/.vz/cache`. If a
download is interrupted, cove verifies the cached file before reuse and resumes
the transfer when possible. To resume manually, use the recovery command printed
by `cove install`:

```bash
curl -L -C - -o ~/.vz/cache/RestoreImage.ipsw <restore-image-url>
```

After the file is complete, re-run the same `cove install` or `cove up` command.

## Support Bundle

Use a support bundle when filing a bug or sharing setup diagnostics:

```bash
cove support bundle
cove support bundle -vm dev
```

The archive is redacted and includes version/signing details, host readiness,
helper and daemon status, storage census, recent run/recording metadata, and
optional VM-specific doctor/control diagnostics.
Screenshots are omitted by default because they cannot be redacted; use
`-include-screenshot` only when you intend to share the visible VM screen.

## Update

For source builds, rebuild from the checkout:

```bash
git pull
go build -o cove ./cmd/cove
cove doctor host
cove helper status
```

If the helper is stale after an upgrade:

```bash
sudo cove helper install
```

When packaged installs become the recommended path, use the package manager's
upgrade command and then run the same doctor/helper checks.

## Uninstall

Remove only the pieces you no longer want:

```bash
rm -f ~/bin/cove             # source-built CLI only
cove helper uninstall        # optional privileged helper
cove daemon stop             # user daemon, if running
rm -rf ~/.vz                 # VMs, images, caches, runs, and store data
```

Keep `~/.vz` if you want to preserve local VM data.

## Linux Guest Toolchains

Fresh desktop Linux images may not include a Go toolchain new enough for the
cove checkout or related Go projects. Install the project-required Go version
inside the guest before running validations; distro packages can lag behind the
`go.mod` requirement. For example:

```bash
curl -LO https://go.dev/dl/go1.24.3.linux-arm64.tar.gz
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf go1.24.3.linux-arm64.tar.gz
export PATH=/usr/local/go/bin:$PATH
go version
```

## Next steps

- [Quick start](quickstart.md) -- get a VM running now.
- [Tutorial: provision and snapshot your first VM](first-vm.md) -- learn the
  four moves every other workflow is built from.
- [Troubleshooting](../guides/troubleshooting.md) -- if the host check reports
  a problem.
