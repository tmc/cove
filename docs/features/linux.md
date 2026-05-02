---
title: Linux VMs
---
# Linux VMs

Turnkey ARM64 Linux VMs with unattended installers for Ubuntu, Debian, Fedora, and Alpine.

## Quick Start

```bash
cove install -linux                                # auto-downloads Ubuntu Server 24.04 ARM64
cove install -linux -distro alpine                 # fast Alpine virt install
cove run -linux -gui
```

With custom user credentials:

```bash
cove install -linux -provision-user myuser -provision-password secret
cove run -linux -gui
```

Ubuntu Desktop:

```bash
cove up -linux -desktop -user myuser
```

Nested KVM on supported hosts:

```bash
cove up -linux -nested
cove vzscript run kvm-test
```

## Installation

The installer creates the right unattended seed for each distro:

### Ubuntu

Ubuntu uses Subiquity autoinstall with cloud-init NoCloud data.

```bash
cove install -linux                                # downloads ISO automatically
cove install -linux -iso ~/ubuntu-24.04-live-server-arm64.iso  # use local ISO
```

### Debian

Debian uses d-i preseed.

```bash
cove install -linux -distro debian
```

### Fedora

Fedora uses kickstart.

```bash
cove install -linux -distro fedora
```

### Alpine

Alpine uses a `setup-alpine` answers file loaded through an apkovl boot overlay.

```bash
cove install -linux -distro alpine
```

All four installers create the user, enable SSH, and partition the VM disk without prompting.

## Boot Modes

### EFI Boot (default)

Uses `VZEFIBootLoader` with NVRAM variable store. Required for ISO installation.

### Direct Kernel Boot

For direct boot without EFI:

```bash
cove run -linux \
  -kernel /path/to/vmlinuz \
  -initrd /path/to/initrd.img \
  -cmdline "console=tty0 console=hvc0 root=/dev/vda"
```

## Serial Console

Serial output goes to stdout by default:

```bash
cove run -linux                          # serial to stdout
cove run -linux -serial none             # disable serial
cove run -linux -serial /tmp/serial.log  # write to file
```

## Guest Shell (`-shell`)

`cove run -linux -shell` attaches the host terminal to an interactive shell
in the guest after the VM boots. It opens an `ExecStream` against the
guest agent with `tty=true`, allocates a PTY in the guest via the agent's
PTY support, and pipes the master side to the host terminal. Host
SIGWINCH forwards as `ResizeExecTTY`; host SIGINT forwards as
`SignalExec(SIGINT)` to the guest process group only -- the main cove
shutdown handler is detached from SIGINT for the duration of the shell so
your first Ctrl-C does not also stop the VM.

```bash
cove run -linux -shell                         # bash -l with PTY, GUI window
cove run -linux -shell -gui                    # explicit GUI mode
cove run -linux -shell -cpu 4 -memory 8        # bigger guest, same wrapper
```

Constraints:
- Requires `-linux`. Refused for macOS guests until the macOS user agent
  grows the same PTY path.
- Mutually exclusive with `-headless`. The host terminal is the shell, so
  cove needs a TTY to write to.
- The default guest command is `/bin/bash -l`.

Limitation in v0.2: stdin is **read-only**. The wrapper does not stream
host keystrokes into the guest because the agent's `ExecStream` RPC is
server-streaming only. Use this flag for tail-style observation -- watching
boot logs, following journald output, viewing a long-running guest process.
Bidirectional stdin and a standalone `cove shell <vm>` subcommand are
deferred to v0.2.1 / v0.3 under design 023 (Docker-shaped exec UX).

## Rosetta (x86-64 Translation)

Run x86-64 Linux binaries on ARM64 via Apple's Rosetta:

```bash
cove rosetta install
cove run -linux
```

Rosetta is attached by default for Linux guests. cove auto-mounts `/run/rosetta`
and registers the binfmt handler through the guest agent when it is available.
Disable it with `-rosetta=false` if the guest should not receive the Rosetta
share.

If Rosetta is not installed on the host, cove prints a warning and boots the VM
without x86-64 translation. Install it once:

```bash
cove rosetta install
```

After registration, x86-64 binaries run transparently.

Check status and install:

```bash
cove rosetta status
cove rosetta install
cove rosetta setup          # show guest setup instructions
```

## Nested KVM

Nested virtualization is available only on M3/M4 Apple Silicon hosts running macOS 15 or newer. M1/M2 hardware does not expose the required capability.

Use `-nested` on `cove run` or `cove up`:

```bash
cove run -linux -nested
```

If the host does not support nested virtualization, cove fails before VM start with:

```text
nested virtualization requires M3/M4 chip on macOS 15+. Run without --nested to boot a standard VM (KVM will be disabled).
```

Nested guests share memory and CPU with the outer Linux VM. A nested guest cannot itself nest another VM.

## VirtioFS Volumes

Tagged `-vol` mounts are auto-mounted in Linux guests under `/mnt/<tag>` when the
guest agent is running. cove adds `uid=<guest-user-uid>,gid=<guest-user-gid>` to
the Linux VirtioFS mount options, so the provisioned user can write host files
without `chmod`. New Linux VMs record `1000:1000` in `config.json`; older VMs
fall back to the same first-user convention.

Only the primary provisioned user receives write ownership on the auto-mapped
mount. Secondary users can read the files but need guest-side group permissions
or a separate mount with explicit `uid=`/`gid=` options.

## Architecture

Linux VMs use:
- **VZGenericPlatformConfiguration** -- generic platform for non-macOS guests
- **VZEFIBootLoader** -- EFI boot with NVRAM variable store
- **VZVirtioGraphicsDeviceConfiguration** -- Virtio GPU for display
- **Cloud-init, preseed, kickstart, setup-alpine** -- distro-native unattended install data

## Known Issues

| Issue | Cause | Solution |
|-------|-------|---------|
| Slow boot | EFI firmware initialization | Normal, wait ~30 seconds |
| No network | DHCP timeout | Check NAT networking settings |
| Black screen in GUI | Virtio GPU driver not loaded | Wait for kernel to load, or use serial console |
