# Installing cove

cove runs on Apple Silicon Macs and uses Apple's Virtualization.framework.

## Requirements

- Apple Silicon Mac (M1 or newer).
- macOS 12 Monterey or newer.
- Xcode Command Line Tools.
- Enough free disk for the restore image and VM disk. A fresh macOS install commonly needs tens of GB.

## Install the CLI

```bash
brew install tmc/tap/cove
```

Or build from source:

```bash
go install github.com/tmc/vz-macos@latest
```

On first launch, cove signs the local binary with the Virtualization.framework entitlements it needs. If you build manually while developing cove, re-sign after each build:

```bash
go build -o cove .
codesign -s - -f --entitlements internal/autosign/vz.entitlements ./cove
```

## First VM

```bash
cove up -user dev
```

To reuse a restore image you already downloaded:

```bash
cove up -user dev -ipsw ~/Downloads/RestoreImage.ipsw
```

The IPSW path is the best option on slow or firewalled networks because it lets you download once and reuse the file across VMs.

## Apple SLA Note

macOS guests are governed by Apple's macOS Software License Agreement, not just cove's MIT license. The current [macOS Tahoe 26 SLA](https://www.apple.com/legal/sla/docs/macOSTahoe.pdf) section 2B(iii) permits up to two additional virtualized macOS copies or instances on each Apple-branded computer you own or control, for the listed development, testing, macOS Server, or personal non-commercial purposes. Except as separately permitted by Apple, it also excludes service bureau, time-sharing, terminal sharing, relay service, and similar services.

cove does not work around that limit. A single Mac host means at most two additional macOS guest instances under the standard SLA language; a fleet scales by adding Apple hardware. Read the applicable SLA for the macOS version you run: <https://www.apple.com/legal/sla/>.

This section is a product disclosure, not legal advice.

## Helper Daemon

Most cove commands run as your normal user. Some disk-injection operations need root-owned files inside a mounted guest disk because launchd requires LaunchDaemon plists to be `root:wheel`.

cove supports an optional privileged helper:

```bash
cove helper status
sudo cove helper install
```

Use it only if you want to avoid repeated one-shot authorization prompts. `cove helper status` reports whether the installed helper binary is stale.
