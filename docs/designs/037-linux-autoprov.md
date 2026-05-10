# Linux Desktop Autoprovisioning

Issue #235 tracks first-boot Linux Desktop provisioning for the #122 carryover:
`cove up -linux -desktop -user X -password Y` should install Ubuntu Desktop and
arrive at a logged-in GNOME session without a manual password prompt.

## Status

Shipped. The operator-facing contract documented below lands across:

- `f718d69` linux: enable GDM autologin and right-size GUI scanout
- `c624d3f` linux: add `-desktop-installer oem|server` flag
- `aebcf13` linux: provision OEM desktop users explicitly
- `c3f7ead` linux: disable cloud-init after OEM desktop provisioning
- `449cbfa` linux: cover desktop autologin late command (tests)
- `0cbc455` linux: clear autologin keyring prompt
- `4f71eb3` linux: suppress GNOME Initial Setup for OEM desktop

Validation gaps from the original draft are closed: `linux_installer_test.go`
covers the desktop GDM block, asserts the server variant omits it, and asserts
the keyring removal late command is present. Live first-boot reliability
follow-ups remain tracked in [`docs/benchmarks/disk-io.md`](../benchmarks/disk-io.md)
and the ROADMAP `done` row.

## Current Path

`up.go` maps `cove up -linux -desktop -user X -password Y` onto the global
Linux install flags:

- `linuxDesktop=true`, which selects `LinuxVariantDesktop`.
- `provisionUser=X` and `provisionPassword=Y`, which override the default
  provisioned Linux identity.
- `linuxDesktopInstaller=oem` by default, which keeps the Desktop ISO instead
  of booting the Server ISO and installing `ubuntu-desktop` through apt.

`linux_installer.go` builds the autoinstall `user-data`. The password is hashed
with SHA-512 before it is written to the autoinstall `identity.password` field.
The common path enables sshd and writes the partition, bootloader, and Network
Manager late commands. Server mode already creates the configured user and
enables password SSH login.

Desktop mode adds two pieces:

- `linuxDesktopUserLateCommands` creates or updates the requested user in OEM
  mode, adds the normal desktop groups, suppresses GNOME Initial Setup, and
  disables cloud-init after provisioning.
- `linuxAutoLoginLateCommand` writes `/target/etc/gdm3/custom.conf` with:

```
[daemon]
AutomaticLoginEnable=true
AutomaticLogin=<user>
```

The install flow sets `AutoLogin=true` only for `LinuxVariantDesktop`, so the
GDM block is not emitted for server installs.

## Gaps (closed)

The generated YAML now has focused coverage for the operator-facing contract:

- desktop autoinstall emits the GDM automatic login block for the requested
  user (`linuxAutoLoginLateCommand`, covered in `linux_installer_test.go`);
- non-desktop autoinstall does not emit the GDM block (server variant test);
- desktop autologin removes the installer-created login keyring so GNOME does
  not prompt on first GUI session (`0cbc455`).

## Validation

`linux_installer_test.go` renders `generateUserData` for desktop and server
variants and asserts the expected late-command strings, including the GDM
block, the keyring removal, and the server-variant negative assertions.
Runtime verification with a fresh Desktop VM remains a separate, disk-heavy
pass tracked in [`docs/benchmarks/disk-io.md`](../benchmarks/disk-io.md).

## Cross-references

- [`docs/benchmarks/disk-io.md`](../benchmarks/disk-io.md) for the validation
  gap that is still contaminating first-boot conclusions.
- [`docs/designs/036-nixos-guest-support.md`](036-nixos-guest-support.md) for
  the other Linux guest install path that shares the provisioning boundary.
- [`RELEASE-NOTES-v0.4.0.md`](../../RELEASE-NOTES-v0.4.0.md) for the release
  note that should keep the first-boot caveat visible.

## Verified 2026-05-10

- SHA chain `f718d69`, `c624d3f`, `aebcf13`, `c3f7ead`, `449cbfa`,
  `0cbc455`, `4f71eb3` all present on `origin/main`.
- `linux_installer.go:55` defines `LinuxVariantDesktop`; `up.go:193`
  wires `-desktop-installer oem|server` flag (default `oem`); `up.go:282`
  selects the desktop variant from `cove up -linux -desktop`.
- `linux_installer_test.go:49-57` asserts the server variant omits the
  desktop GDM block; `:193` covers the keyring-removal late command.
