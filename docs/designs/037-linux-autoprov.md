# Linux Desktop Autoprovisioning

Issue #235 tracks first-boot Linux Desktop provisioning for the #122 carryover:
`cove up -linux -desktop -user X -password Y` should install Ubuntu Desktop and
arrive at a logged-in GNOME session without a manual password prompt.

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

## Gaps

The generated YAML needs focused coverage for the operator-facing contract:

- desktop autoinstall emits the GDM automatic login block for the requested
  user;
- non-desktop autoinstall does not emit the GDM block;
- desktop autologin also avoids the common GNOME login keyring prompt on the
  first GUI session.

The keyring prompt can be mitigated without live VM verification by removing the
installer-created login keyring in a desktop autologin late command. GNOME will
recreate it on first use instead of prompting for a stale keyring password:

```
rm -f /target/home/<user>/.local/share/keyrings/login.keyring || true
```

## Validation

Unit tests should render `generateUserData` for desktop and server variants and
assert the expected late-command strings. Runtime verification with a fresh
Desktop VM remains useful, but it is disk-heavy and should be a separate pass
unless the machine has enough free space for another Ubuntu Desktop install.
