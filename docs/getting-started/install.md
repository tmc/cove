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
