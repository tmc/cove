---
title: Installation
---
# Installation

## Requirements

- Apple Silicon Mac (M1/M2/M3/M4)
- macOS 12.0+ (Monterey or later)
- Xcode Command Line Tools (`xcode-select --install`)
- ~20GB free disk space for a macOS VM

## Go Install

Install from source for now:

```bash
go install github.com/tmc/cove@latest
```

Make sure `$GOPATH/bin` or `$HOME/go/bin` is on `PATH`, then verify the
installed command:

```bash
cove version
cove doctor host
```

The Homebrew formula is not the recommended first-run path yet. Future packages
should install the `cove` binary only. Guest account setup remains explicit: the
first VM asks for a username and prompts for a password when `-password` is
omitted. Cove does not create fixed default guest credentials.

The Go module path is `github.com/tmc/cove`.

### Entitlements

cove auto-signs itself on first launch with the required Virtualization.framework entitlements. No manual step is needed for normal use.

> [!WARNING]
> Manual signing is only needed if autosigning fails or you bypass normal
> startup while developing cove.

If you need to sign manually:

```bash
codesign -s - -f --entitlements internal/autosign/vz.entitlements "$(command -v cove)"
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

## Support Bundle

Use a support bundle when filing a bug or sharing setup diagnostics:

```bash
cove support bundle
cove support bundle -vm dev
```

The archive is redacted and includes version/signing details, host readiness,
helper and daemon status, storage census, recent run/recording metadata, and
optional VM-specific doctor/control diagnostics.

## Update

For source builds:

```bash
go install github.com/tmc/cove@latest
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
