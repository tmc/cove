# Installing cove

cove runs on Apple Silicon Macs and uses Apple's Virtualization.framework.

## Requirements

- Apple Silicon Mac (M1 or newer).
- macOS 12 Monterey or newer.
- Xcode Command Line Tools.
- Enough free disk for the restore image and VM disk. A fresh macOS install commonly needs tens of GB.

## Install the CLI

Build from source for now:

```bash
git clone https://github.com/tmc/cove
cd cove
go build -o cove .
codesign -s - -f --entitlements internal/autosign/vz.entitlements ./cove
install -m 0755 cove ~/bin/cove
```

The Go module path remains `github.com/tmc/vz-macos` for compatibility, so
`go install github.com/tmc/vz-macos@latest` is still valid when the module proxy
has the release you want. The repository is named `cove`.

On first launch, cove signs the local binary with the Virtualization.framework entitlements it needs. If you build manually while developing cove, re-sign after each build:

```bash
go build -o cove .
codesign -s - -f --entitlements internal/autosign/vz.entitlements ./cove
```

The Homebrew formula is not the recommended first-run path yet. Packaged
installs should eventually feel like a normal macOS CLI install: they provide
the `cove` binary, and cove asks for guest account details only when you create
or provision a VM. There are no built-in guest credentials.

## First VM

Before creating a VM, run the host readiness check:

```bash
cove doctor host
```

Use `cove doctor host -json` when you need machine-readable output for a
support ticket or setup script.

```bash
cove up -user dev
```

To reuse a restore image you already downloaded:

```bash
cove up -user dev -ipsw ~/Downloads/RestoreImage.ipsw
```

The IPSW path is the best option on slow or firewalled networks because it lets you download once and reuse the file across VMs.

## Expected Prompts

`cove up -user dev` asks for the guest account password when `-password` is
not provided. Prefer the interactive prompt over putting passwords in shell
history.

macOS may show administrator prompts when cove needs to mount a guest disk,
write root-owned launchd files into the guest, or install/update the optional
helper. Those prompts authorize local disk preparation only; cove does not send
credentials to a remote service.

When a VM runs with the native GUI, cove also adds a macOS status item for that
VM. The status item shows the VM state and provides quick menu actions such as
opening or closing the window and requesting a clean stop. It is a per-run UI
control, not a background login item.

## Apple SLA Note

macOS guests are governed by Apple's macOS Software License Agreement, not just cove's MIT license. The current [macOS Tahoe 26 SLA](https://www.apple.com/legal/sla/docs/macOSTahoe.pdf) section 2B(iii) permits up to two additional virtualized macOS copies or instances on each Apple-branded computer you own or control, for the listed development, testing, macOS Server, or personal non-commercial purposes. Except as separately permitted by Apple, it also excludes service bureau, time-sharing, terminal sharing, relay service, and similar services.

cove does not work around that limit. A single Mac host means at most two additional macOS guest instances under the standard SLA language; a fleet scales by adding Apple hardware. Read the applicable SLA for the macOS version you run: <https://www.apple.com/legal/sla/>.

This section is a product disclosure, not legal advice. See
`docs/reference/license-comparison.md` for the cove, Lume, Tart, Orchard, and
tart-guest-agent license comparison.

## Helper Daemon

Most cove commands run as your normal user. Some disk-injection operations need root-owned files inside a mounted guest disk because launchd requires LaunchDaemon plists to be `root:wheel`.

cove supports an optional privileged helper:

```bash
cove helper status
sudo cove helper install
```

Use it only if you want to avoid repeated one-shot authorization prompts. `cove helper status` reports whether the installed helper binary is stale.

## Support Bundle

When reporting a problem, collect a redacted diagnostics archive:

```bash
cove support bundle
cove support bundle -vm dev
```

The bundle includes version/signing details, `cove doctor host`, helper and
daemon status, storage census, and recent run/recording metadata. With `-vm`,
it also includes VM-specific doctor and control-socket diagnostics. Bearer
tokens, passwords, usernames, and home-directory paths are redacted.

## Update

For source builds:

```bash
git pull
go build -o cove .
codesign -s - -f --entitlements internal/autosign/vz.entitlements ./cove
install -m 0755 cove ~/bin/cove
cove doctor host
cove helper status
```

If `cove helper status` reports a stale helper, reinstall it:

```bash
sudo cove helper install
```

When packaged installs become the recommended path, use the package manager's
upgrade command and then run the same doctor/helper checks.

## Uninstall

Choose the level of removal you want:

```bash
rm -f ~/bin/cove             # remove the source-built CLI
cove helper uninstall        # remove the optional privileged helper
cove daemon stop             # unload the per-user daemon if you started it
rm -rf ~/.vz                 # remove VMs, images, runs, cache, and store data
```

Do not remove `~/.vz` unless you are intentionally deleting local VM data.
