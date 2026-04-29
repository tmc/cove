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

## Rosetta (x86-64 Translation)

Run x86-64 Linux binaries on ARM64 via Apple's Rosetta:

```bash
cove run -linux -rosetta
```

Guest setup (inside the Linux VM):

```bash
sudo mkdir -p /run/rosetta
sudo mount -t virtiofs rosetta /run/rosetta
sudo /run/rosetta/rosetta --register
```

After setup, x86-64 binaries run transparently.

Check status and install:

```bash
cove rosetta status
cove rosetta install
cove rosetta setup          # show guest setup instructions
```

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
