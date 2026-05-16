---
title: Installation
---
# Installation

## Requirements

- Apple Silicon Mac (M1/M2/M3/M4)
- macOS 12.0+ (Monterey or later)
- Xcode Command Line Tools (`xcode-select --install`)
- ~20GB free disk space for a macOS VM

## Homebrew (recommended)

```bash
brew install tmc/tap/cove
```

> [!NOTE]
> cove auto-signs itself with virtualization entitlements on first launch. No manual codesigning step is needed.

## Go Install

```bash
go install github.com/tmc/vz-macos@latest
```

The binary will be placed in `$GOPATH/bin` (or `$HOME/go/bin`).

## From Source

```bash
git clone https://github.com/tmc/vz-macos
cd vz-macos
# The repository is github.com/tmc/vz-macos but the binary is named "cove"
go build -o cove .
```

### Entitlements

cove auto-signs itself on first launch with the required Virtualization.framework entitlements. No manual step is needed for normal use.

> [!WARNING]
> You must re-sign after every `go build` during development.

If you need to sign manually:

```bash
codesign -s - -f --entitlements internal/autosign/vz.entitlements ./cove
```

Required entitlements:
- `com.apple.security.virtualization` -- basic VM capability
- `com.apple.security.hypervisor` -- hypervisor access

## Verify Installation

```bash
cove version
```

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
version you run: <https://www.apple.com/legal/sla/>. See
[License and Virtualization Limits](../reference/license-comparison.md) for the
cove, Lume, Tart, Orchard, and tart-guest-agent comparison. This section is a
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
