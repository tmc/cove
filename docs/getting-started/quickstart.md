---
title: Quick Start
---
# Quick Start

Three ways to get a VM running.

## One Command

Install, provision, and boot in a single step:

```bash
cove up -user myuser
```

This downloads the latest macOS IPSW, installs it, provisions a user account, and boots the VM with a GUI window.

> [!TIP]
> Add `-headless` if you don't need a GUI window.

Add vzscripts to configure the guest automatically:

```bash
cove up -user myuser -vzscripts homebrew,golang
```

## Step by Step

### 1. Install macOS

```bash
cove install
```

This downloads the latest supported IPSW restore image and installs macOS into `~/.vz/vms/default/`. To use a local IPSW:

```bash
cove install -ipsw ~/Downloads/UniversalMac_15.0_RestoreImage.ipsw
```

### 2. Provision a User

```bash
cove provision -user myuser -skip-setup-assistant
```

This mounts the VM disk, injects a LaunchDaemon that creates the user on first boot, configures auto-login, and skips Setup Assistant.

> [!NOTE]
> `provision` asks through the native macOS admin dialog so LaunchDaemon files
> can be written as root:wheel. launchd silently ignores plists without that
> ownership.

### 3. Boot the VM

```bash
cove run
```

The VM opens in a native macOS window. On subsequent launches, the VM resumes from its last suspend state.

## Linux VM

```bash
cove install -linux
cove run -linux -gui
```

Choose another distro:

```bash
cove install -linux -distro alpine
cove install -linux -distro debian
cove install -linux -distro fedora
```

Or with unattended provisioning:

```bash
cove install -linux -provision-user ubuntu -provision-password <password>
cove run -linux -gui
```

For Ubuntu Desktop:

```bash
cove up -linux -desktop -user myuser
```

## What Happens Next

- The VM directory is `~/.vz/vms/default/` (override with `-vm <name>`)
- A control socket is created at `~/.vz/vms/<name>/control.sock` for programmatic access
- On quit, the VM suspends to disk and resumes on next `cove run`
- Use `cove run -no-resume` for a cold boot
