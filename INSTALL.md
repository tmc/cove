# Installing cove

cove runs on Apple Silicon Macs and uses Apple's Virtualization.framework.

## Requirements

- Apple Silicon Mac (M1 or newer).
- macOS 14.0+ (Sonoma or later).
- Xcode Command Line Tools.
- Enough free disk for the restore image and VM disk. A fresh macOS install commonly needs tens of GB.

## Install the CLI

Install from source for now:

```bash
go install github.com/tmc/cove/cmd/cove@latest
```

The Go module path is `github.com/tmc/cove`.

On first launch, cove signs the local binary with the Virtualization.framework
entitlements it needs. If autosigning fails, sign the binary manually:

```bash
codesign -s - -f --entitlements cmd/cove/vz.entitlements "$(command -v cove)"
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

Each provision that runs as your normal user triggers one of these admin
dialogs. Installing the privileged helper once (`sudo cove helper install`)
elevates through the helper instead, so subsequent provisions run without any
dialog. A non-interactive shell (tmux, ssh, sandboxed, or CI) cannot display
the native dialog at all, so the helper is required there: without it,
`cove up` stops early and tells you to install it or re-run from a normal
terminal. See [Helper Daemon](#helper-daemon).

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

Without the helper, every such operation prompts for administrator approval via
the native macOS admin dialog — one prompt per provision. With the helper
installed, those operations elevate through it and prompt zero times. The helper
is therefore optional in an interactive terminal but required in any shell that
cannot show the native dialog (tmux, ssh, sandboxed, or CI).

cove supports an optional privileged helper:

```bash
cove helper status
sudo cove helper install
```

`cove helper status` reports whether the installed helper binary is stale (its
SHA does not match the current `cove` build); refresh a stale helper by
re-running `sudo cove helper install`. `cove doctor host` flags the same stale
state.

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
go install github.com/tmc/cove/cmd/cove@latest
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

### Research build

For the experimental iOS backend, build and sign a separate binary:

```sh
make build RESEARCH=1 BINARY=cove-research
```

This selects the `cove_research` Go build tag and
`internal/ios/research.entitlements`. Autosigning and optional macgo bundling
preserve that profile. `make build` uses the public profile. An attached research
signature does not establish that host policy permits PV=3 guests; runtime
preflight and firmware qualification remain required.

Inspect static iOS prerequisites with `cove ios preflight`. Its JSON separates
signature validity, missing research entitlements, and descriptor ABI checks.
A zero exit status covers those checks only; model construction, VM validation,
and DFU discovery still require runtime qualification.

Prepare the pinned firmware toolchain sources:

```sh
cove ios setup source
cove ios setup source -check
```

This checks out the pinned vphone revision and recursive submodules in cove's
state directory, then records their commits and the requirements file digest.
Use `-repository /path/to/vphone-cli` to seed Git objects from a local checkout
without modifying it, or `-dir /path/to/cache` to select a cache. Existing caches
must match their source manifest; incomplete directories are rejected.
This step does not install Python dependencies, compile tools, or prepare firmware.

Create a blank iOS bundle and configure its inputs while stopped:

```sh
cove ios new phone
cove ios config phone -rom /absolute/path/to/avpbooter.rom -sep-rom /absolute/path/to/sep.rom
cove ios config phone -network none -scale 3
cove ios config phone
```

ROM paths may also be relative to the bundle. Configuration copies ROM bytes
into `roms/<sha256>.rom` and records bundle-relative references. These hashes
check file integrity; they do not establish firmware compatibility. Editing is
rejected while cove holds the bundle run lock. The command prints config JSON.

The experimental headless entry point is:

```sh
./cove-research ios run -initialize -force-dfu -timeout 2m phone
```

`-initialize` permits first identity creation. Ordinary runs preserve identity
and NVRAM. Serial output goes to stdout and diagnostics to stderr. Interactive
serial input, Finder routing, firmware preparation/restore and live DFU
qualification remain incomplete; this command is not a demonstrated boot recipe.

Inspect device endpoints without starting a VM:

```sh
cove ios devices
cove ios devices -libusb /opt/homebrew/lib/libusb-1.0.dylib
```

The optional library enables raw USB DFU/recovery discovery. The JSON keeps these
endpoints separate from normal/restored `usbmuxd` attachments. An empty list does
not qualify DFU boot or firmware restore. The required Apple packages are pinned
in `go.mod`; see [the package boundaries](docs/research/ios-device-packages-2026-09-07.md).
