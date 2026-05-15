---
title: Provisioning
---
# Provisioning

Provisioning creates a user account, configures auto-login, and injects the guest agent into the VM disk before first boot.

## Methods

### Method 1: Disk Injection (Recommended)

The `provision` command writes files directly into the VM disk image.

```bash
cove install -ipsw restore.ipsw
cove provision -user testuser -password <password> -skip-setup-assistant
cove run -gui
```

The disk apply step needs administrator privileges because macOS launchd
silently ignores LaunchDaemon plists not owned by root:wheel. Cove requests
those privileges with the native macOS admin dialog; you do not need to rerun
the command with `sudo` from a normal terminal.

Run the apply phase from a normal interactive terminal. Restricted automation
shells that cannot show the native admin dialog can stage files, but the apply
phase will fail before the VM has an agent. In that state the safest recovery is
to stop the VM and run:

```bash
cove -vm <vm> provision -apply
```

#### What It Does

1. Attaches the VM disk image using `hdiutil attach`
2. Mounts the APFS "Data" volume
3. Writes a provision script to `/var/db/vz-provision.sh` (root:wheel)
4. Writes a LaunchDaemon plist to `/Library/LaunchDaemons/com.vz.provision.plist` (root:wheel)
5. Creates `.AppleSetupDone` to skip Setup Assistant
6. Creates `/etc/kcpassword` and `loginwindow.plist` for auto-login
7. Cross-compiles and injects the vz-agent binary
8. Detaches the disk

On first boot, the LaunchDaemon creates the user with `sysadminctl`, configures auto-login, and self-cleans.

#### Two-Phase Provisioning

For CI or when building as non-root:

```bash
# Phase 1: build binaries, generate scripts (no admin needed)
cove provision -user testuser -password <password> -stage-only

# Phase 2: mount disk, copy files (native admin dialog)
cove provision -apply
```

#### Provision Options

```bash
# Full provisioning
cove provision -user me -password <password> -skip-setup-assistant

# With SSH key
cove provision -user me -password <password> -ssh-key ~/.ssh/id_rsa.pub

# Enable SSH daemon
cove provision -user me -password <password> -enable-sshd

# Direct plist mode (advanced, bypasses sysadminctl)
cove provision -user me -password <password> -plist -uid 501

# Disable auto-login
cove provision -user me -password <password> -no-auto-login

# Disable recovery bootstrap
cove provision -user me -password <password> -no-bootstrap-recovery
```

### Method 2: GUI Automation

Use the `up` command to combine install, provision, and Setup Assistant automation:

```bash
cove up -user testuser -password <password> -gui
```

Or drive Setup Assistant manually on a running VM:

```bash
cove ctl detect                          # check current screen
cove ctl setup-assist testuser <password>    # automate Setup Assistant
```

If `setup-assist` loses the control socket, first check whether the VM stopped:

```bash
cove list
cove ctl -vm <vm> gui status
```

The command reports the failed control action, but a stopped VM needs to be
started again before more keyboard or mouse automation can work.

### Method 3: One Command

The `up` command combines install, provision, and run:

```bash
cove up -user testuser
```

## Verifying Provisioning

```bash
cove doctor
```

Expected output for successful injection:

```
+ Library/LaunchDaemons/com.vz.provision.plist
    Status: OK
+ private/var/db/vz-provision.sh
    Status: OK
+ private/var/db/.AppleSetupDone
    Status: OK (uid=0 gid=0)
+ private/etc/kcpassword
    Status: OK
+ Library/Preferences/com.apple.loginwindow.plist
    Status: OK
```

If you see `WRONG_OWNER`, run `cove doctor --fix` and retry provisioning.

Auto-fix issues:

```bash
cove doctor --fix
```

## Boot Sequence

```
Kernel -> launchd -> LaunchDaemons (provision runs here) -> WindowServer -> loginwindow
                                                                             |
                                                                   .AppleSetupDone?
                                                                   Yes -> Login / Desktop
                                                                   No  -> Setup Assistant
```

## File Location Mapping

When the VM disk is mounted on the host:

| Guest Path | Host Path (on Data volume) |
|-----------|---------------------------|
| `/var/db/` | `/Volumes/Data/private/var/db/` |
| `/Library/` | `/Volumes/Data/Library/` |
| `/Users/` | `/Volumes/Data/Users/` |
| `/etc/` | `/Volumes/Data/private/etc/` |

## Auto-Login

Auto-login requires two files:

1. **kcpassword** (`/etc/kcpassword`): XOR-encoded password with key `0x7D, 0x89, 0x52, 0x23, 0xD2, 0xBC, 0xDD, 0xEA, 0xA3, 0xB9, 0x1F`, padded to multiple of 11 bytes
2. **loginwindow.plist** (`/Library/Preferences/com.apple.loginwindow.plist`): `autoLoginUser` key set to the username

## Troubleshooting

| Issue | Cause | Solution |
|-------|-------|---------|
| "Resource temporarily unavailable" | VM is running | Stop VM before provisioning |
| "could not find Data partition" | APFS detection failed | Use `-v` for verbose output |
| User not created on boot | LaunchDaemon not loaded | Run `cove doctor --fix`, then retry provisioning |
| Setup Assistant still appears | `.AppleSetupDone` missing | Use `-skip-setup-assistant` |
| Auto-login not working | Wrong kcpassword owner | Run `cove doctor --fix`, then retry provisioning |
| WRONG_OWNER in doctor | Files need root:wheel ownership | Run `cove doctor --fix` |
| No guest agent after install | Provision apply did not complete | Stop the VM, then run `cove -vm <vm> provision -apply` from a normal terminal |
| "Preparing macOS" overlay remains | macOS first boot is still finishing | Wait for the overlay to clear before using desktop automation; `ctl exec` needs the agent and will not work until provisioning completes |

## Manually Inspecting the Disk

```bash
hdiutil attach ~/.vz/vms/default/disk.img -nobrowse -nomount
diskutil list                          # find the Data volume
diskutil mount /dev/disk22s5           # mount it
ls /Volumes/Data/private/var/db/       # inspect files
cat /Volumes/Data/private/var/db/vz-provision.sh
diskutil unmount /dev/disk22s5
hdiutil detach /dev/disk22
```
