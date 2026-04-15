---
title: Installation
---
# Installation

## Homebrew (recommended)

```bash
brew install tmc/tap/cove
```

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

If you need to sign manually (e.g., after `go build` in development):

```bash
codesign -s - -f --entitlements internal/autosign/vz.entitlements ./cove
```

Required entitlements:
- `com.apple.security.virtualization` -- basic VM capability
- `com.apple.security.hypervisor` -- hypervisor access

## Requirements

- Apple Silicon Mac (M1/M2/M3/M4)
- macOS 12.0+ (Monterey or later)
- Xcode Command Line Tools (`xcode-select --install`)

## Verify Installation

```bash
cove version
```
