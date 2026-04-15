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
sudo cove provision -user testuser -password secret -skip-setup-assistant
cove run -gui
```

**Sudo is required.** macOS launchd silently ignores LaunchDaemon plists not owned by root:wheel. Without sudo, provisioning files are created with your user's UID and the daemon never loads.

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
cove provision -user testuser -password secret -stage-only

# Phase 2: mount disk, copy files (needs admin)
sudo cove provision -apply
```

#### Provision Options

```bash
# Full provisioning
sudo cove provision -user me -password secret -skip-setup-assistant

# With SSH key
sudo cove provision -user me -password secret -ssh-key ~/.ssh/id_rsa.pub

# Enable SSH daemon
sudo cove provision -user me -password secret -enable-sshd

# Direct plist mode (advanced, bypasses sysadminctl)
sudo cove provision -user me -password secret -plist -uid 501

# Disable auto-login
sudo cove provision -user me -password secret -no-auto-login

# Disable recovery bootstrap
sudo cove provision -user me -password secret -no-bootstrap-recovery
```

### Method 2: GUI Automation

Use the `up` command to combine install, provision, and Setup Assistant automation:

```bash
cove up -user testuser -password secret -gui
```

Or drive Setup Assistant manually on a running VM:

```bash
cove ctl detect                          # check current screen
cove ctl setup-assist testuser secret    # automate Setup Assistant
```

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

If you see `WRONG_OWNER`, re-run provision with sudo.

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
| User not created on boot | LaunchDaemon not loaded | Re-run with sudo |
| Setup Assistant still appears | `.AppleSetupDone` missing | Use `-skip-setup-assistant` |
| Auto-login not working | Wrong kcpassword owner | Re-run with sudo |
| WRONG_OWNER in doctor | Files created without sudo | Re-run with sudo |

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
